package libp2pwebrtc

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"

	tpt "github.com/libp2p/go-libp2p-core/transport"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"

	"github.com/pion/dtls/v2/pkg/crypto/fingerprint"
	"github.com/pion/ice/v2"
	"github.com/pion/webrtc/v3"
)

var (
	ErrDataChannelTimeout    = errors.New("timed out waiting for datachannel")
	ErrNoiseHandshakeTimeout = errors.New("noise handshake timeout")
	ErrNoCertInConfig        = errors.New("no certificate configured in listener config")
)

var (
	defaultMultihash *multihash.DecodedMultihash = nil
)

func init() {
	// populate default multihash
	encoded, err := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	if err != nil {
		panic(err)
	}

	defaultMultihash = &multihash.DecodedMultihash{
		Code:   multihash.SHA2_256,
		Name:   multihash.Codes[multihash.SHA2_256],
		Digest: encoded,
		Length: len(encoded),
	}

}

/// implement net.Listener
type listener struct {
	transport                 *WebRTCTransport
	config                    webrtc.Configuration
	privKey                   crypto.PrivKey
	localFingerprint          webrtc.DTLSFingerprint
	localFingerprintMultibase string
	mux                       *UDPMuxNewAddr
	closeChan                 chan struct{}
	localMultiaddr            ma.Multiaddr
	connChan                  chan tpt.CapableConn
}

func newListener(transport *WebRTCTransport, laddr ma.Multiaddr, socket net.PacketConn, privKey crypto.PrivKey, config webrtc.Configuration) (*listener, error) {
	mux := NewUDPMuxNewAddr(ice.UDPMuxParams{UDPConn: socket}, make(chan candidateAddr, 1))
	localFingerprints, err := config.Certificates[0].GetFingerprints()
	if err != nil {
		return nil, err
	}

	localMh, err := hex.DecodeString(strings.ReplaceAll(localFingerprints[0].Value, ":", ""))
	if err != nil {
		return nil, err
	}
	localMhBuf, _ := multihash.EncodeName(localMh, sdpHashToMh[localFingerprints[0].Algorithm])
	localFpMultibase, _ := multibase.Encode(multibase.Base64, localMhBuf)

	l := &listener{
		mux:                       mux,
		transport:                 transport,
		privKey:                   privKey,
		config:                    config,
		localFingerprint:          localFingerprints[0],
		localFingerprintMultibase: localFpMultibase,
		closeChan:                 make(chan struct{}, 1),
		localMultiaddr:            laddr,
		connChan:                  make(chan tpt.CapableConn, 20),
	}
	go l.startAcceptLoop()
	return l, err
}

func (l *listener) startAcceptLoop() {
	for {
		select {
		case <-l.closeChan:
			return
		case addr := <-l.mux.newAddrChan:
			go func() {
				ctx, cancelFunc := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancelFunc()
				conn, err := l.accept(ctx, addr)
				if err != nil {
					log.Debugf("could not accept connection: %v", err)
					return
				}
				l.connChan <- conn
			}()
		}
	}
}

func (l *listener) Accept() (tpt.CapableConn, error) {
	select {
	case <-l.closeChan:
		return nil, os.ErrClosed
	case conn := <-l.connChan:
		return conn, nil
	}
}

func (l *listener) Close() error {
	select {
	case <-l.closeChan:
		return nil
	default:
	}
	close(l.closeChan)
	return nil
}

func (l *listener) Addr() net.Addr {
	return l.mux.LocalAddr()
}

func (l *listener) Multiaddr() ma.Multiaddr {
	return l.localMultiaddr
}

func (l *listener) accept(ctx context.Context, addr candidateAddr) (tpt.CapableConn, error) {
	localFingerprint := strings.ReplaceAll(l.localFingerprint.Value, ":", "")
	// signaling channel
	signalChan := make(chan struct{})

	se := webrtc.SettingEngine{}
	se.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
	se.DetachDataChannels()
	se.DisableCertificateFingerprintVerification(true)
	se.SetICECredentials(addr.ufrag, localFingerprint)
	se.SetLite(true)
	se.SetICEUDPMux(l.mux)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	clientSdpString := renderClientSdp(sdpArgs{
		Addr:        addr.raddr,
		Fingerprint: defaultMultihash,
		Ufrag:       addr.ufrag,
		Password:    addr.ufrag,
	})
	clientSdp := webrtc.SessionDescription{SDP: clientSdpString, Type: webrtc.SDPTypeOffer}
	pc, err := api.NewPeerConnection(l.config)
	if err != nil {
		return nil, err
	}

	var connectedOnce sync.Once
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			connectedOnce.Do(func() { signalChan <- struct{}{} })
		}
	})

	pc.SetRemoteDescription(clientSdp)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	pc.SetLocalDescription(answer)

	// await peerconnection connected state
	select {
	case <-signalChan:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// await openening of datachannel
	dcChan := make(chan *DataChannel)
	// this enforces that the correct data channel label is used
	// for the handshake
	handshakeChannel, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Negotiated: func(v bool) *bool { return &v }(true),
		ID:         func(v uint16) *uint16 { return &v }(1),
	})
	handshakeChannel.OnOpen(func() {
		detached, err := handshakeChannel.Detach()
		if err != nil {
			return
		}
		dcChan <- newDataChannel(
			detached,
			pc,
			l.mux.LocalAddr(),
			addr.raddr,
		)
	})

	timeout := 10 * time.Second
	var dc *DataChannel = nil
	select {
	case <-time.After(timeout):
		_ = pc.Close()
		return nil, ErrDataChannelTimeout
	case dc = <-dcChan:
		if dc == nil {
			_ = pc.Close()
			return nil, fmt.Errorf("should be unreachable")
		}
		// clear to ignore opening of future data channels
		// until noise handshake is complete
		pc.OnDataChannel(func(*webrtc.DataChannel) {})
	}

	// perform noise handshake
	noiseTpt, err := noise.New(l.privKey)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	// we do not yet know A's peer ID so accept any inbound
	secureConn, err := noiseTpt.SecureInbound(context.Background(), dc, "")
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	_, err = secureConn.Write([]byte(l.localFingerprintMultibase))
	if err != nil {
		return nil, err
	}

	// Ordinarily, we would do this with a ReadDeadline, but since the underlying datachannel.ReadWriteCloser
	// does not allow us to set ReadDeadline, we have to manually spawn a goroutine
	done := make(chan error)
	go func() {
		buf := make([]byte, 200)
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
			return nil, err
		}
	case <-ctx.Done():
		_ = pc.Close()
		return nil, ErrNoiseHandshakeTimeout
	}

	remoteMultiaddr, err := manet.FromNetAddr(addr.raddr)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	scope, err := l.transport.rcmgr.OpenConnection(network.DirInbound, false, remoteMultiaddr)
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

	conn, err := newConnection(
		pc,
		l.transport,
		scope,
		secureConn.LocalPeer(),
		secureConn.LocalPrivateKey(),
		l.localMultiaddr,
		secureConn.RemotePeer(),
		secureConn.RemotePublicKey(),
		remoteMultiaddr,
	)

	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	defer func() { _ = dc.Close() }()

	return conn, nil
}

func verifyRemoteFingerprint(raw []byte, remoteMultibaseMultihash string) bool {
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		log.Debugf("could not parse certificate: %v", err)
		return false
	}

	_, remoteData, err := multibase.Decode(remoteMultibaseMultihash)
	if err != nil {
		log.Debugf("could not decode multibase: %v", err)
		return false
	}
	decoded, err := multihash.Decode(remoteData)
	if err != nil {
		log.Debugf("could not decode multihash: %v", err)
		return false
	}
	remoteFingerprint := hex.EncodeToString(decoded.Digest)
	remoteFingerprint = maFingerprintToSdp(remoteFingerprint)

	// create fingerprint for remote certificate
	hashAlgoName, ok := mhToSdpHash[decoded.Name]
	if !ok {
		hashAlgoName = decoded.Name
	}

	hashAlgo, err := fingerprint.HashFromString(hashAlgoName)
	if err != nil {
		log.Debugf("could not find hash algo: %s %v", hashAlgoName, err)
		return false
	}
	fp, err := fingerprint.Fingerprint(cert, hashAlgo)

	return strings.EqualFold(fp, remoteFingerprint)
}
