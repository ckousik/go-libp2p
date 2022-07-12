package libp2pwebrtc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
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
}

type Option func(*WebRTCTransport) error

func New(privKey ic.PrivKey, rcmgr network.ResourceManager, opts ...Option) (*WebRTCTransport, error) {
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Debugf("could not generate rsa key for cert: %v", err)
		return nil, err
	}
	cert, err := webrtc.GenerateCertificate(pk)
	if err != nil {
		log.Debugf("could not generate certificate: %v", err)
		return nil, err
	}
	config := webrtc.Configuration{
		Certificates: []webrtc.Certificate{*cert},
	}
	return &WebRTCTransport{rcmgr: rcmgr, webrtcConfig: config, privKey: privKey}, nil
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
		log.Debugf("listener could not fetch dialargs: %v", err)
	}
	udpAddr, err := net.ResolveUDPAddr(nw, host)
	if err != nil {
		log.Debugf("listener could not resolve udp address: %v", err)
		return nil, err
	}

	socket, err := net.ListenUDP(nw, udpAddr)
	if err != nil {
		log.Debugf("could not listen on udp: %v", err)
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
		t.privKey,
		t.webrtcConfig,
	)
}

func (t *WebRTCTransport) Dial(
	ctx context.Context,
	remoteMultiaddr ma.Multiaddr,
	p peer.ID,
) (tpt.CapableConn, error) {
	remoteMultihash, err := decodeRemoteFingerprint(remoteMultiaddr)
	if err != nil {
		log.Debugf("could not decode remote multiaddr: %v", err)
		return nil, err
	}

	rnw, rhost, err := manet.DialArgs(remoteMultiaddr)
	if err != nil {
		log.Debugf("could not generate dialargs: %v", err)
		return nil, err
	}

	raddr, err := net.ResolveUDPAddr(rnw, rhost)
	if err != nil {
		log.Debugf("could not resolve udp address: %v", err)
		return nil, err
	}

	localFingerprint, err := t.getCertificateFingerprint()
	if err != nil {
		log.Debugf("could not get local fingerprint: %v", err)
		return nil, err
	}
	localFingerprintStr := strings.ReplaceAll(localFingerprint.Value, ":", "")

	se := webrtc.SettingEngine{}
	se.DetachDataChannels()
	se.SetICECredentials(localFingerprintStr, localFingerprintStr)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	pc, err := api.NewPeerConnection(t.webrtcConfig)
	if err != nil {
		log.Debugf("could not instantiate peerconnection: %v", err)
		return nil, err
	}

	// We need to set negotiated = true for this channel on both
	// the client and server to avoid DCEP errors.
	dc, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Negotiated: func(v bool) *bool { return &v }(true),
		ID:         func(v uint16) *uint16 { return &v }(1),
	})

	if err != nil {
		_ = pc.Close()
		log.Debugf("could not create datachannel: %v", err)
		return nil, err
	}

	opened := make(chan *DataChannel, 1)
	errChan := make(chan error, 1)
	dc.OnOpen(func() {
		detached, err := dc.Detach()
		if err != nil {
			log.Debugf("could not detach datachannel: %v", err)
			errChan <- err
		}
		cp, err := dc.Transport().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil {
			log.Debugf("could not fetch selected candidate pair: %v", err)
			errChan <- err
			return
		}

		laddr := &net.UDPAddr{IP: net.ParseIP(cp.Local.Address), Port: int(cp.Local.Port)}
		opened <- newDataChannel(detached, pc, laddr, raddr)
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		log.Debugf("could not create offer: %v", err)
		return nil, err
	}

	err = pc.SetLocalDescription(offer)
	if err != nil {
		_ = pc.Close()
		log.Debugf("could not set local description: %v", err)
		return nil, err
	}

	answerSdpString := renderServerSdp(sdpArgs{
		Addr:        raddr,
		Fingerprint: remoteMultihash,
		Ufrag:       localFingerprintStr,
		Password:    hex.EncodeToString(remoteMultihash.Digest),
	})

	answer := webrtc.SessionDescription{SDP: answerSdpString, Type: webrtc.SDPTypeAnswer}
	err = pc.SetRemoteDescription(answer)
	if err != nil {
		_ = pc.Close()
		log.Debugf("could not set remote description: %v", err)
		return nil, err
	}

	var dataChannel *DataChannel = nil
	select {
	case dataChannel = <-opened:
	case err = <-errChan:
		_ = pc.Close()
		return nil, err
	case <-ctx.Done():
		_ = pc.Close()
		return nil, ErrDataChannelTimeout
	}

	// create noise transport for auth
	noiseTpt, err := noise.New(t.privKey)
	if err != nil {
		log.Debugf("could not create noise transport: %v", err)
		return nil, err
	}

	mb, err := encodeDTLSFingerprint(localFingerprint)
	if err != nil {
		log.Debugf("could not encode local fingerprint: %v", err)
		return nil, err
	}
	secureConn, err := noiseTpt.SecureOutbound(context.Background(), dataChannel, p)
	if err != nil {
		log.Debugf("could not create secure outbound transport: %v", err)
		return nil, err
	}

	// noise handshake
	_, err = secureConn.Write([]byte(mb))
	if err != nil {
		_ = pc.Close()
		log.Debugf("could not write auth data: %v", err)
		return nil, err
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
			_ = pc.Close()
			log.Debugf("dialed: read failed: %v", err)
			return nil, err
		}
	case <-time.After(10 * time.Second):
		_ = pc.Close()
		return nil, ErrNoiseHandshakeTimeout
	}

	scope, err := t.rcmgr.OpenConnection(network.DirOutbound, false, remoteMultiaddr)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	err = scope.SetPeer(secureConn.RemotePeer())
	if err != nil {
		_ = pc.Close()
		scope.Done()
		return nil, err
	}

	localAddr, err := manet.FromNetAddr(dataChannel.LocalAddr())
	if err != nil {
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
		_ = pc.Close()
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
