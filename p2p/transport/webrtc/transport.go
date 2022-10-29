package libp2pwebrtc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/sec"
	tpt "github.com/libp2p/go-libp2p/core/transport"
	"github.com/libp2p/go-libp2p/p2p/security/noise"

	logging "github.com/ipfs/go-log/v2"
	ma "github.com/multiformats/go-multiaddr"
	mafmt "github.com/multiformats/go-multiaddr-fmt"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/multiformats/go-multihash"

	"github.com/pion/dtls/v2/pkg/crypto/fingerprint"
	"github.com/pion/webrtc/v3"
)

var log = logging.Logger("webrtc-transport")

var dialMatcher = mafmt.And(mafmt.IP, mafmt.Base(ma.P_UDP), mafmt.Base(ma.P_WEBRTC), mafmt.Base(ma.P_CERTHASH))

type WebRTCTransport struct {
	webrtcConfig webrtc.Configuration
	rcmgr        network.ResourceManager
	privKey      ic.PrivKey
	noiseTpt     *noise.Transport
	localPeerId  peer.ID
}

var _ tpt.Transport = &WebRTCTransport{}

type Option func(*WebRTCTransport) error

func New(privKey ic.PrivKey, rcmgr network.ResourceManager, opts ...Option) (*WebRTCTransport, error) {
	localPeerId, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		return nil, errInternal("could not get local peer ID", err)
	}
	// We use elliptic P-256 since it is widely supported by browsers.
	// See: https://github.com/libp2p/specs/pull/412#discussion_r968294244
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, errInternal("could not generate key for cert", err)
	}
	cert, err := webrtc.GenerateCertificate(pk)
	if err != nil {
		return nil, errInternal("could not generate certificate", err)
	}
	config := webrtc.Configuration{
		Certificates: []webrtc.Certificate{*cert},
	}
	noiseTpt, err := noise.New(privKey, []protocol.ID{})
	if err != nil {
		return nil, errInternal("unable to create noise transport", err)
	}
	return &WebRTCTransport{rcmgr: rcmgr, webrtcConfig: config, privKey: privKey, noiseTpt: noiseTpt, localPeerId: localPeerId}, nil
}

func (t *WebRTCTransport) Protocols() []int {
	return []int{ma.P_WEBRTC, ma.P_CERTHASH}
}

func (t *WebRTCTransport) Proxy() bool {
	return false
}

func (t *WebRTCTransport) CanDial(addr ma.Multiaddr) bool {
	return dialMatcher.Matches(addr)
}

func (t *WebRTCTransport) Listen(addr ma.Multiaddr) (tpt.Listener, error) {
	addr, wrtcComponent := ma.SplitLast(addr)
	isWebrtc := wrtcComponent.Equal(ma.StringCast("/webrtc"))
	if !isWebrtc {
		return nil, errMultiaddr("must listen on webrtc multiaddr", nil)
	}
	nw, host, err := manet.DialArgs(addr)
	if err != nil {
		return nil, errMultiaddr("listener could not fetch dialargs", err)
	}
	udpAddr, err := net.ResolveUDPAddr(nw, host)
	if err != nil {
		return nil, errMultiaddr("listener could not resolve udp address", err)
	}

	socket, err := net.ListenUDP(nw, udpAddr)
	if err != nil {
		return nil, errInternal("could not listen on udp", err)
	}

	// construct multiaddr
	listenerMultiaddr, err := manet.FromNetAddr(socket.LocalAddr())
	if err != nil {
		_ = socket.Close()
		return nil, err
	}

	listenerFingerprint, err := t.getCertificateFingerprint()
	if err != nil {
		_ = socket.Close()
		return nil, err
	}

	encodedLocalFingerprint, err := encodeDTLSFingerprint(listenerFingerprint)
	if err != nil {
		_ = socket.Close()
		return nil, err
	}

	certMultiaddress, err := ma.NewMultiaddr(fmt.Sprintf("/webrtc/certhash/%s", encodedLocalFingerprint))
	if err != nil {
		_ = socket.Close()
		return nil, err
	}

	listenerMultiaddr = listenerMultiaddr.Encapsulate(certMultiaddress)

	// log.Debugf("can be dialed at: %s", listenerMultiaddr.Encapsulate(ma.StringCast(fmt.Sprintf("/p2p/%s", t.localPeerId))))

	return newListener(
		t,
		listenerMultiaddr,
		socket,
		t.webrtcConfig,
	)
}

func (t *WebRTCTransport) Dial(
	ctx context.Context,
	remoteMultiaddr ma.Multiaddr,
	p peer.ID,
) (tpt.CapableConn, error) {
	var (
		pc             *webrtc.PeerConnection
		wrappedChannel *dataChannel
	)
	scope, err := t.rcmgr.OpenConnection(network.DirOutbound, false, remoteMultiaddr)

	cleanup := func() {
		if pc != nil {
			_ = pc.Close()
		}
		if scope != nil {
			scope.Done()
		}
	}

	if err != nil {
		defer cleanup()
		return nil, err
	}

	err = scope.SetPeer(p)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	remoteMultihash, err := decodeRemoteFingerprint(remoteMultiaddr)
	if err != nil {
		defer cleanup()
		return nil, errMultiaddr("could not decode fingerprint", err)
	}

	rnw, rhost, err := manet.DialArgs(remoteMultiaddr)
	if err != nil {
		defer cleanup()
		return nil, errMultiaddr("could not generate dial args", err)
	}

	raddr, err := net.ResolveUDPAddr(rnw, rhost)
	if err != nil {
		defer cleanup()
		return nil, errMultiaddr("could not resolve udp address", err)
	}

	// Instead of encoding the local fingerprint we
	// instead generate a random uuid as the connection ufrag.
	// The only requirement here is that the ufrag and password
	// must be equal, which will allow the server to determine
	// the password using the STUN message.
	ufrag := uuid.New().String()

	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetICECredentials(ufrag, ufrag)
	settingEngine.SetLite(false)
	settingEngine.DetachDataChannels()
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	pc, err = api.NewPeerConnection(t.webrtcConfig)
	if err != nil {
		defer cleanup()
		return nil, errInternal("could not instantiate peerconnection", err)
	}

	signalChan := make(chan struct{ error })
	var connectedOnce sync.Once

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			connectedOnce.Do(func() {
				signalChan <- struct{ error }{nil}
			})
		case webrtc.PeerConnectionStateFailed:
			connectedOnce.Do(func() {
				err := errConnectionFailed("peerconnection move to failed state", nil)
				signalChan <- struct{ error }{err}
			})
		}
	})

	// We need to set negotiated = true for this channel on both
	// the client and server to avoid DCEP errors.
	handshakeChannel, err := pc.CreateDataChannel("handshake", &webrtc.DataChannelInit{
		Negotiated: func(v bool) *bool { return &v }(true),
		ID:         func(v uint16) *uint16 { return &v }(0),
	})
	if err != nil {
		defer cleanup()
		return nil, errDatachannel("could not create", err)
	}
	handshakeChannel.OnOpen(func() {
		rwc, err := handshakeChannel.Detach()
		if err != nil {
			signalChan <- struct{ error }{err}
			return
		}
		wrappedChannel = newDataChannel(handshakeChannel, rwc, pc, nil, raddr)
		cp, err := handshakeChannel.Transport().Transport().ICETransport().GetSelectedCandidatePair()
		if cp == nil || err != nil {
			err = errDatachannel("could not fetch selected candidate pair", err)
			signalChan <- struct{ error }{err}
			return
		}

		laddr := &net.UDPAddr{IP: net.ParseIP(cp.Local.Address), Port: int(cp.Local.Port)}
		wrappedChannel.laddr = laddr
		signalChan <- struct{ error }{nil}
	})

	// do offer-answer exchange
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		defer cleanup()
		return nil, errConnectionFailed("could not create offer", err)
	}

	err = pc.SetLocalDescription(offer)
	if err != nil {
		defer cleanup()
		return nil, errConnectionFailed("could not set local description", err)
	}

	answerSdpString := renderServerSdp(sdpArgs{
		Addr:        raddr,
		Fingerprint: remoteMultihash,
		Ufrag:       ufrag,
	})

	answer := webrtc.SessionDescription{SDP: answerSdpString, Type: webrtc.SDPTypeAnswer}
	err = pc.SetRemoteDescription(answer)
	if err != nil {
		defer cleanup()
		return nil, errConnectionFailed("could not set remote description", err)
	}

	// await peerconnection opening
	select {
	case signal := <-signalChan:
		if signal.error != nil {
			defer cleanup()
			return nil, signal.error
		}
	case <-ctx.Done():
		scope.Done()
		defer cleanup()
		return nil, errDataChannelTimeout
	}

	// await datachannel opening
	select {
	case signal := <-signalChan:
		if signal.error != nil {
			defer cleanup()
			return nil, signal.error
		}
	case <-ctx.Done():
		scope.Done()
		defer cleanup()
		return nil, errDataChannelTimeout
	}

	// the local address of the selected candidate pair should be the
	// local address for the connection, since different datachannels
	// are multiplexed over the same SCTP connection
	localAddr, err := manet.FromNetAddr(wrappedChannel.LocalAddr())
	if err != nil {
		defer cleanup()
		return nil, err
	}

	conn := newConnection(
		pc,
		t,
		scope,
		t.localPeerId,
		t.privKey,
		localAddr,
		p,
		nil,
		remoteMultiaddr,
	)
	tctx, cancel := context.WithTimeout(context.Background(), 2 * time.Second)
	defer cancel()
	secureConn, err := t.noiseHandshake(tctx, pc, wrappedChannel, p, false)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	conn.setRemotePublicKey(secureConn.RemotePublicKey())

	return conn, nil
}

func (t *WebRTCTransport) getCertificateFingerprint() (webrtc.DTLSFingerprint, error) {
	fps, err := t.webrtcConfig.Certificates[0].GetFingerprints()
	if err != nil {
		return webrtc.DTLSFingerprint{}, err
	}
	return fps[0], nil
}

func (t *WebRTCTransport) generateNoisePrologue(pc *webrtc.PeerConnection, inbound bool) ([]byte, error) {
	raw := pc.SCTP().Transport().GetRemoteCertificate()
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return nil, err
	}
	// guess hash algorithm
	localFp, err := t.getCertificateFingerprint()
	if err != nil {
		return nil, err
	}

	remoteFp, err := fingerprint.Fingerprint(cert, crypto.SHA256)
	if err != nil {
		return nil, err
	}
	remoteFp = strings.ReplaceAll(strings.ToLower(remoteFp), ":", "")
	remoteFpBytes, err := hex.DecodeString(remoteFp)
	if err != nil {
		return nil, err
	}

	local := strings.ReplaceAll(localFp.Value, ":", "")
	localBytes, err := hex.DecodeString(local)
	if err != nil {
		return nil, err
	}

	localEncoded, err := multihash.Encode(localBytes, multihash.SHA2_256)
	if err != nil {
		log.Debugf("could not encode multihash for local fingerprint")
		return nil, err
	}
	remoteEncoded, err := multihash.Encode(remoteFpBytes, multihash.SHA2_256)
	if err != nil {
		log.Debugf("could not encode multihash for remote fingerprint")
		return nil, err
	}

	result := []byte("libp2p-webrtc-noise:")
	if inbound {
		result = append(result, remoteEncoded...)
		result = append(result, localEncoded...)
	} else {
		result = append(result, localEncoded...)
		result = append(result, remoteEncoded...)
	}
	return result, nil
}

func (t *WebRTCTransport) noiseHandshake(ctx context.Context, pc *webrtc.PeerConnection, datachannel *dataChannel, peer peer.ID, inbound bool) (secureConn sec.SecureConn, err error) {
	prologue, err := t.generateNoisePrologue(pc, inbound)
	if err != nil {
		return nil, errNoise("could not generate prologue", err)
	}
	sessionTransport, err := t.noiseTpt.WithSessionOptions(noise.Prologue(prologue))
	if err != nil {
		return nil, errNoise("could not instantiate transport", err)
	}
	if inbound {
		secureConn, err = sessionTransport.SecureOutboundForAnyPeerID(ctx, datachannel)
		if err != nil {
			err = errNoise("failed to secure inbound", err)
			return
		}
	} else {
		secureConn, err = sessionTransport.SecureInbound(ctx, datachannel, peer)
		if err != nil {
			err = errNoise("failed to secure outbound", err)
			return
		}
	}
	return secureConn, nil
}
