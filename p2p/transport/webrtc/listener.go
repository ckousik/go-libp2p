package libp2pwebrtc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/webrtc/v3"
)

type listener struct {
	api         *webrtc.API
	config      webrtc.Configuration
	mux         *UDPMuxNewAddr
	newAddrChan chan candidateAddr
	local       ma.Multiaddr
	idKey       crypto.PrivKey
}

var (
	ErrDataChannelTimeout    = errors.New("timed out waiting for datachannel")
	ErrNoiseHandshakeTimeout = errors.New("noise handshake timeout")
	ErrNoCertInConfig        = errors.New("no certificate configured in listener config")
)

func (l *listener) accept(addr candidateAddr) (peer.ID, *webrtc.PeerConnection, error) {

	// Get local fingerprint
	if len(l.config.Certificates) < 1 {
		return "", nil, ErrNoCertInConfig
	}

	localFps, err := l.config.Certificates[0].GetFingerprints()
	if err != nil || len(localFps) < 1 {
		return "", nil, err
	}
	localFp := strings.ReplaceAll(localFps[0].Value, ":", "")

	// get remote fingerprint
	remoteFp := maFingerprintToSdp(addr.fingerprint)

	se := webrtc.SettingEngine{}
	se.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
	se.DetachDataChannels()
	se.SetICECredentials(addr.fingerprint, localFp)
	se.SetLite(true)
	se.SetICEUDPMux(l.mux)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	clientSdpString := renderClientSdp(sdpArgs{
		Addr:        addr.raddr,
		Fingerprint: remoteFp,
		Ufrag:       addr.fingerprint,
		Password:    addr.fingerprint,
	})
	clientSdp := webrtc.SessionDescription{SDP: clientSdpString, Type: webrtc.SDPTypeOffer}
	pc, err := api.NewPeerConnection(l.config)
	if err != nil {
		return "", nil, err
	}
	pc.SetRemoteDescription(clientSdp)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", nil, err
	}
	pc.SetLocalDescription(answer)

	// await openening of datachannel
	dcChan := make(chan *DataChannel)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		// assert that the label of the first DataChannel is "data"
		// TODO: Make const and move into spec
		if dc.Label() != "data" {
			// warn closing data channel
			dc.Close()
			return
		}

		dc.OnOpen(func() {
			detached, err := dc.Detach()
			if err != nil {
				return
			}
			dcChan <- newDataChannel(detached)
		})

	})

	timeout := 10 * time.Second
	var dc *DataChannel = nil
	select {
	case <-time.After(timeout):
		return "", nil, ErrDataChannelTimeout
	case dc = <-dcChan:
		if dc == nil {
			return "", nil, fmt.Errorf("should be unreachable")
		}
		// clear to allow opening of future data channels
		pc.OnDataChannel(func (*webrtc.DataChannel) {})
	}
	// perform noise handshake
	noiseTpt, err := noise.New(l.idKey)
	if err != nil {
		return "", nil, err
	}
	// we do not yet know A's peer ID so accept any inbound
	secureConn, err := noiseTpt.SecureInbound(context.Background(), dc, "")
	if err != nil {
		return "", nil, err
	}

	_, err = secureConn.Write([]byte(maFingerprintToSdp(localFp)))
	if err != nil {
		return "", nil, err
	}

	buf := make([]byte, 200)
	n, err := secureConn.Read(buf)
	if err != nil {
		return "", nil, err
	}

	// Ordinarily, we would do this with a ReadDeadline, but since the underlying datachannel.ReadWriteCloser
	// does not allow us to set ReadDeadline, we have to manually spawn a goroutine
	done := make(chan error, 1)
	go func() {
		if string(buf[:n]) != remoteFp {
			done <- fmt.Errorf("could not verify remote fingerprint: expected: %s, got: %s", remoteFp, string(buf[:n]))
		}
		close(done)
	}()

	select {
	case err = <-done:
		if done != nil {
			return "", nil, err
		}
	case <-time.After(10 * time.Second):
		return "", nil, ErrNoiseHandshakeTimeout
	}

	// return the peerID and PeerConnection object
	return secureConn.RemotePeer(), pc, nil
}
