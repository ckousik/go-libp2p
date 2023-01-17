package libp2pwebrtc

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multibase"
	mh "github.com/multiformats/go-multihash"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
	"google.golang.org/protobuf/proto"
)

func fingerprintToSDP(fp *mh.DecodedMultihash) string {
	if fp == nil {
		return ""
	}
	fpDigest := intersperse2(hex.EncodeToString(fp.Digest), ':', 2)
	return getSupportedSDPString(fp.Code) + " " + fpDigest
}

func decodeRemoteFingerprint(maddr ma.Multiaddr) (*mh.DecodedMultihash, error) {
	remoteFingerprintMultibase, err := maddr.ValueForProtocol(ma.P_CERTHASH)
	if err != nil {
		return nil, err
	}
	_, data, err := multibase.Decode(remoteFingerprintMultibase)
	if err != nil {
		return nil, err
	}
	return mh.Decode(data)
}

func encodeDTLSFingerprint(fp webrtc.DTLSFingerprint) (string, error) {
	digest, err := hex.DecodeString(strings.ReplaceAll(fp.Value, ":", ""))
	if err != nil {
		return "", err
	}
	encoded, err := mh.Encode(digest, mh.SHA2_256)
	if err != nil {
		return "", err
	}
	return multibase.Encode(multibase.Base64url, encoded)
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

func genUfrag(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// only use this if the datachannels are detached, since the OnOpen callback
// will be called immediately. Only use after the peerconnection is open.
// The context should close if the peerconnection underlying the datachannel
// is closed.
func getDetachedChannel(ctx context.Context, dc *webrtc.DataChannel) (rwc datachannel.ReadWriteCloser, err error) {
	done := make(chan struct{})
	dc.OnOpen(func() {
		rwc, err = dc.Detach()
		close(done)
	})
	// this is safe since for detached datachannels, the peerconnection runs the onOpen
	// callback immediately if the SCTP transport is also connected.
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return
}

func awaitPeerConnectionOpen(ufrag string, pc *webrtc.PeerConnection) <-chan error {
	errC := make(chan error)
	var once sync.Once
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			once.Do(func() { close(errC) })
			return
		}
		if state == webrtc.PeerConnectionStateFailed {
			once.Do(func() {
				// this ensures that we don't block this routine if the
				// listener goes away
				select {
				case errC <- fmt.Errorf("peerconnection failed: %s", ufrag):
					close(errC)
				default:
					log.Error("could not signal peerconnection failure")
				}
			})
		}
		if state == webrtc.PeerConnectionStateDisconnected {
			log.Warn("peerconnection disconnected")
		}
	})
	return errC
}

// writeMessage writes a length-prefixed protobuf message to the datachannel. It
// is preferred over protoio DelimitedWriter because it is thread safe, and allocates
// a smaller buffer
func writeMessage(rwc datachannel.ReadWriteCloser, msg *pb.Message) (int, error) {
	buf := make([]byte, 5)
	varintLen := binary.PutUvarint(buf, uint64(proto.Size(msg)))
	data, err := proto.Marshal(msg)
	if err != nil {
		return 0, err
	}
	buf = buf[:varintLen]
	buf = append(buf, data...)
	_, err = rwc.Write(buf)
	if err != nil {
		return 0, err
	}
	return len(msg.Message), nil
}
