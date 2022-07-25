package libp2pwebrtc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/google/uuid"
	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/sec"
	tpt "github.com/libp2p/go-libp2p-core/transport"
	"github.com/libp2p/go-libp2p/p2p/security/noise"

	logging "github.com/ipfs/go-log/v2"
	ma "github.com/multiformats/go-multiaddr"
	mafmt "github.com/multiformats/go-multiaddr-fmt"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/pion/webrtc/v3"
)

var log = logging.Logger("webrtc-transport")

var dialMatcher = mafmt.And(mafmt.IP, mafmt.Base(ma.P_UDP), mafmt.Base(P_XWEBRTC), mafmt.Base(ma.P_CERTHASH))

type WebRTCTransport struct {
	webrtcConfig webrtc.Configuration
	rcmgr        network.ResourceManager
	privKey      ic.PrivKey
	noiseTpt     *noise.Transport
}

var _ tpt.Transport = &WebRTCTransport{}

type Option func(*WebRTCTransport) error

func New(privKey ic.PrivKey, rcmgr network.ResourceManager, opts ...Option) (*WebRTCTransport, error) {
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("could not generate rsa key for cert: %v", err)
	}
	cert, err := webrtc.GenerateCertificate(pk)
	if err != nil {
		return nil, fmt.Errorf("could not generate certificate: %v", err)
	}
	config := webrtc.Configuration{
		Certificates: []webrtc.Certificate{*cert},
	}
	noiseTpt, err := noise.New(privKey)
	if err != nil {
		return nil, fmt.Errorf("unable to create the noise transport")
	}
	return &WebRTCTransport{rcmgr: rcmgr, webrtcConfig: config, privKey: privKey, noiseTpt: noiseTpt}, nil
}

func (t *WebRTCTransport) Protocols() []int {
	return []int{P_XWEBRTC}
}

func (t *WebRTCTransport) Proxy() bool {
	return false
}

func (t *WebRTCTransport) CanDial(addr ma.Multiaddr) bool {
	return dialMatcher.Matches(addr)
}

func (t *WebRTCTransport) Listen(addr ma.Multiaddr) (tpt.Listener, error) {
	nw, host, err := manet.DialArgs(addr)
	if err != nil {
		return nil, fmt.Errorf("listener could not fetch dialargs: %v", err)
	}
	udpAddr, err := net.ResolveUDPAddr(nw, host)
	if err != nil {
		return nil, fmt.Errorf("listener could not resolve udp address: %v", err)
	}

	socket, err := net.ListenUDP(nw, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("could not listen on udp: %v", err)
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
	var pc *webrtc.PeerConnection
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
		return nil, fmt.Errorf("could not decode remote multiaddr: %v", err)
	}

	rnw, rhost, err := manet.DialArgs(remoteMultiaddr)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not generate dialargs: %v", err)
	}

	raddr, err := net.ResolveUDPAddr(rnw, rhost)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not resolve udp address: %v", err)
	}

	// Instead of encoding the local fingerprint we
	// instead generate a random uuid as the connection ufrag.
	// The only requirement here is that the ufrag and password
	// must be equal, which will allow the server to determine
	// the password using the STUN message.
	ufrag := uuid.New().String()

	se := webrtc.SettingEngine{}
	se.DetachDataChannels()
	se.SetICECredentials(ufrag, ufrag)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	pc, err = api.NewPeerConnection(t.webrtcConfig)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not instantiate peerconnection: %v", err)
	}

	// We need to set negotiated = true for this channel on both
	// the client and server to avoid DCEP errors.
	dc, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Negotiated: func(v bool) *bool { return &v }(true),
		ID:         func(v uint16) *uint16 { return &v }(1),
	})

	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not create datachannel: %v", err)
	}

	opened := make(chan *dataChannel, 1)
	errChan := make(chan error, 1)
	dc.OnOpen(func() {
		detached, err := dc.Detach()
		if err != nil {
			err = fmt.Errorf("could not detach datachannel: %v", err)
			errChan <- err
			return
		}
		cp, err := dc.Transport().Transport().ICETransport().GetSelectedCandidatePair()
		if cp == nil || err != nil {
			err = fmt.Errorf("could not fetch selected candidate pair: %v", err)
			errChan <- err
			return
		}

		laddr := &net.UDPAddr{IP: net.ParseIP(cp.Local.Address), Port: int(cp.Local.Port)}
		opened <- newDataChannel(detached, pc, laddr, raddr)
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not create offer: %v", err)
	}

	err = pc.SetLocalDescription(offer)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not set local description: %v", err)
	}

	answerSdpString := renderServerSdp(sdpArgs{
		Addr:        raddr,
		Fingerprint: remoteMultihash,
		Ufrag:       ufrag,
		Password:    hex.EncodeToString(remoteMultihash.Digest),
	})

	answer := webrtc.SessionDescription{SDP: answerSdpString, Type: webrtc.SDPTypeAnswer}
	err = pc.SetRemoteDescription(answer)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("could not set remote description: %v", err)
	}

	var dataChannel *dataChannel = nil
	select {
	case dataChannel = <-opened:
	case err = <-errChan:
		defer cleanup()
		return nil, err
	case <-ctx.Done():
		scope.Done()
		defer cleanup()
		return nil, ErrDataChannelTimeout
	}

	secureConn, err := t.noiseHandshake(ctx, pc, dataChannel, p, false)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	localAddr, err := manet.FromNetAddr(dataChannel.LocalAddr())
	if err != nil {
		defer cleanup()
		return nil, err
	}
	conn, err := newConnection(
		pc,
		t,
		scope,
		secureConn.LocalPeer(),
		secureConn.LocalPrivateKey(),
		localAddr,
		secureConn.RemotePeer(),
		secureConn.RemotePublicKey(),
		remoteMultiaddr,
	)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	return conn, nil
}

func (t *WebRTCTransport) getCertificateFingerprint() (webrtc.DTLSFingerprint, error) {
	fps, err := t.webrtcConfig.Certificates[0].GetFingerprints()
	if err != nil {
		return webrtc.DTLSFingerprint{}, err
	}
	return fps[0], nil
}

func (t *WebRTCTransport) noiseHandshake(ctx context.Context, pc *webrtc.PeerConnection, datachannel *dataChannel, peer peer.ID, inbound bool) (secureConn sec.SecureConn, err error) {
	if inbound {
		secureConn, err = t.noiseTpt.SecureInbound(ctx, datachannel, peer)
		if err != nil {
			return
		}
	} else {
		secureConn, err = t.noiseTpt.SecureOutbound(ctx, datachannel, peer)
		if err != nil {
			return
		}
	}
	localFingerprint, err := t.getCertificateFingerprint()
	if err != nil {
		return
	}
	encodedMultibase, err := encodeDTLSFingerprint(localFingerprint)
	if err != nil {
		return
	}

	_, err = secureConn.Write([]byte(encodedMultibase))
	if err != nil {
		return
	}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 2048)
		n, err := secureConn.Read(buf)
		if err != nil {
			done <- err
		}
		remoteFpMultibase := string(buf[:n])
		if !verifyRemoteFingerprint(pc.SCTP().Transport().GetRemoteCertificate(), remoteFpMultibase) {
			done <- fmt.Errorf("could not verify remote fingerprint")
		}
		close(done)
	}()

	select {
	case err = <-done:
		if err != nil {
			err = fmt.Errorf("dialed: read failed: %v", err)
			return
		}
	case <-ctx.Done():
		return nil, ErrNoiseHandshakeTimeout
	}
	return secureConn, nil
}
