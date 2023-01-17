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
	"time"

	"github.com/libp2p/go-libp2p/core/connmgr"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/libp2p/go-libp2p/core/sec"
	tpt "github.com/libp2p/go-libp2p/core/transport"
	"github.com/libp2p/go-libp2p/p2p/security/noise"

	// "github.com/libp2p/go-libp2p/p2p/transport/webrtc/udpmux"

	logging "github.com/ipfs/go-log/v2"
	ma "github.com/multiformats/go-multiaddr"
	mafmt "github.com/multiformats/go-multiaddr-fmt"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/multiformats/go-multihash"

	// pionlogger "github.com/pion/logging"

	"github.com/pion/dtls/v2/pkg/crypto/fingerprint"
	"github.com/pion/webrtc/v3"
)

var log = logging.Logger("webrtc-transport")

var dialMatcher = mafmt.And(mafmt.IP, mafmt.Base(ma.P_UDP), mafmt.Base(ma.P_WEBRTC), mafmt.Base(ma.P_CERTHASH))

const (
	// hansdhakeChannelNegotiated is used to specify that the
	// handshake data channel does not need negotiation via DCEP.
	// A constant is used since the `DataChannelInit` struct takes
	// references instead of values.
	hansdhakeChannelNegotiated = true
	// handshakeChannelId is the agreed ID for the handshake data
	// channel.
	// A constant is used since the `DataChannelInit` struct takes
	// references instead of values. We specify the type here as this
	// value is only ever copied and passed by reference
	handshakeChannelId = uint16(0)
)

// timeout values for the peerconnection
// https://github.com/pion/webrtc/blob/v3.1.50/settingengine.go#L102-L109
const DefaultDisconnectedTimeout = 5 * time.Second
const DefaultFailedTimeout = 25 * time.Second
const DefaultKeepaliveTimeout = 2 * time.Second

type WebRTCTransport struct {
	webrtcConfig webrtc.Configuration
	rcmgr        network.ResourceManager
	privKey      ic.PrivKey
	noiseTpt     *noise.Transport
	localPeerId  peer.ID

	// timeouts
	peerConnectionFailedTimeout       time.Duration
	peerConnectionDisconnectedTimeout time.Duration
	peerConnectionKeepaliveTimeout    time.Duration

	// in-flight connections
	maxInFlightConnections uint32
}

var _ tpt.Transport = &WebRTCTransport{}

type Option func(*WebRTCTransport) error

// WithPeerConnectionIceTimeouts sets the ice disconnect, failure and keepalive timeouts
func WithPeerConnectionIceTimeouts(disconnect time.Duration, failed time.Duration, keepalive time.Duration) Option {
	return func(t *WebRTCTransport) error {
		if failed < disconnect {
			return fmt.Errorf("disconnect timeout cannot be greater than failed timeout")
		}
		if disconnect <= keepalive {
			return fmt.Errorf("keepalive timeout cannot be greater than or equal to failed timeout")
		}
		t.peerConnectionDisconnectedTimeout = disconnect
		t.peerConnectionFailedTimeout = failed
		t.peerConnectionKeepaliveTimeout = keepalive
		return nil
	}
}

// WithListenerMaxInFlightConnections sets the maximum number of connections that are in-flight, i.e
// they are being negotiated, or are waiting to be accepted.
func WithListenerMaxInFlightConnections(m uint32) Option {
	return func(t *WebRTCTransport) error {
		t.maxInFlightConnections = m
		return nil
	}
}

func New(privKey ic.PrivKey, psk pnet.PSK, gater connmgr.ConnectionGater, rcmgr network.ResourceManager, opts ...Option) (*WebRTCTransport, error) {
	if psk != nil {
		log.Error("WebRTC doesn't support private networks yet.")
		return nil, fmt.Errorf("WebRTC doesn't support private networks yet")
	}
	localPeerId, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("could not get local peer ID: %w", err)
	}
	// We use elliptic P-256 since it is widely supported by browsers.
	// See: https://github.com/libp2p/specs/pull/412#discussion_r968294244
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("could not generate key for cert: %w", err)
	}
	cert, err := webrtc.GenerateCertificate(pk)
	if err != nil {
		return nil, fmt.Errorf("could not generate certificate: %w", err)
	}
	config := webrtc.Configuration{
		Certificates: []webrtc.Certificate{*cert},
	}
	noiseTpt, err := noise.New(noise.ID, privKey, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to create noise transport: %w", err)
	}
	transport := &WebRTCTransport{
		rcmgr:        rcmgr,
		webrtcConfig: config,
		privKey:      privKey,
		noiseTpt:     noiseTpt,
		localPeerId:  localPeerId,

		peerConnectionDisconnectedTimeout: DefaultDisconnectedTimeout,
		peerConnectionFailedTimeout:       DefaultFailedTimeout,
		peerConnectionKeepaliveTimeout:    DefaultKeepaliveTimeout,

		maxInFlightConnections: DefaultMaxInFlightConnections,
	}
	for _, opt := range opts {
		if err := opt(transport); err != nil {
			return nil, err
		}
	}
	return transport, nil
}

func (t *WebRTCTransport) Protocols() []int {
	return []int{ma.P_WEBRTC}
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
		return nil, fmt.Errorf("must listen on webrtc multiaddr")
	}
	nw, host, err := manet.DialArgs(addr)
	if err != nil {
		return nil, fmt.Errorf("listener could not fetch dialargs: %w", err)
	}
	udpAddr, err := net.ResolveUDPAddr(nw, host)
	if err != nil {
		return nil, fmt.Errorf("listener could not resolve udp address: %w", err)
	}

	socket, err := net.ListenUDP(nw, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("could not listen on udp: %w", err)
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

func (t *WebRTCTransport) Dial(ctx context.Context, remoteMultiaddr ma.Multiaddr, p peer.ID) (tpt.CapableConn, error) {
	scope, err := t.rcmgr.OpenConnection(network.DirOutbound, false, remoteMultiaddr)
	if err != nil {
		return nil, err
	}
	err = scope.SetPeer(p)
	if err != nil {
		return nil, err
	}
	pc, conn, err := t.dial(ctx, scope, remoteMultiaddr, p)
	if err != nil {
		scope.Done()
		if pc != nil {
			_ = pc.Close()
		}
		return nil, err
	}
	return conn, nil
}

func (t *WebRTCTransport) dial(
	ctx context.Context,
	scope network.ConnManagementScope,
	remoteMultiaddr ma.Multiaddr,
	p peer.ID,
) (*webrtc.PeerConnection, tpt.CapableConn, error) {
	var pc *webrtc.PeerConnection

	remoteMultihash, err := decodeRemoteFingerprint(remoteMultiaddr)
	if err != nil {
		return pc, nil, fmt.Errorf("could not decode fingerprint: %w", err)
	}
	remoteHashFunction, ok := getSupportedSDPHash(remoteMultihash.Code)
	if !ok {
		return pc, nil, fmt.Errorf("unsupported hash function: %w", nil)
	}

	rnw, rhost, err := manet.DialArgs(remoteMultiaddr)
	if err != nil {
		return pc, nil, fmt.Errorf("could not generate dial args: %w", err)
	}

	raddr, err := net.ResolveUDPAddr(rnw, rhost)
	if err != nil {
		return pc, nil, fmt.Errorf("could not resolve udp address: %w", err)
	}

	// Instead of encoding the local fingerprint we
	// instead generate a random uuid as the connection ufrag.
	// The only requirement here is that the ufrag and password
	// must be equal, which will allow the server to determine
	// the password using the STUN message.
	ufrag := "libp2p+webrtc+v1/" + genUfrag(32)

	settingEngine := webrtc.SettingEngine{}
	// suppress pion logs
	// loggerFactory := pionlogger.NewDefaultLoggerFactory()
	// loggerFactory.DefaultLogLevel = pionlogger.LogLevelDisabled
	// settingEngine.LoggerFactory = loggerFactory

	settingEngine.SetICECredentials(ufrag, ufrag)
	settingEngine.DetachDataChannels()
	settingEngine.SetICETimeouts(t.peerConnectionDisconnectedTimeout, t.peerConnectionFailedTimeout, t.peerConnectionKeepaliveTimeout)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	pc, err = api.NewPeerConnection(t.webrtcConfig)
	if err != nil {
		return pc, nil, fmt.Errorf("could not instantiate peerconnection: %w", err)
	}

	errC := awaitPeerConnectionOpen(ufrag, pc)
	// We need to set negotiated = true for this channel on both
	// the client and server to avoid DCEP errors.
	negotiated, id := hansdhakeChannelNegotiated, handshakeChannelId
	rawHandshakeChannel, err := pc.CreateDataChannel("", &webrtc.DataChannelInit{
		Negotiated: &negotiated,
		ID:         &id,
	})
	if err != nil {
		return pc, nil, fmt.Errorf("could not create datachannel: %w", err)
	}

	// do offer-answer exchange
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return pc, nil, fmt.Errorf("could not create offer: %w", err)
	}

	err = pc.SetLocalDescription(offer)
	if err != nil {
		return pc, nil, fmt.Errorf("could not set local description: %w", err)
	}

	answerSdpString := renderServerSdp(raddr, ufrag, remoteMultihash)

	answer := webrtc.SessionDescription{SDP: answerSdpString, Type: webrtc.SDPTypeAnswer}
	err = pc.SetRemoteDescription(answer)
	if err != nil {
		return pc, nil, fmt.Errorf("could not set remote description: %w", err)
	}

	// await peerconnection opening
	select {
	case err := <-errC:
		if err != nil {
			return pc, nil, err
		}
	case <-ctx.Done():
		return pc, nil, fmt.Errorf("peerconnection opening timed out")
	}

	detached, err := getDetachedChannel(ctx, rawHandshakeChannel)
	if err != nil {
		return pc, nil, err
	}
	// set the local address from the candidate pair
	cp, err := rawHandshakeChannel.Transport().Transport().ICETransport().GetSelectedCandidatePair()
	if cp == nil || err != nil {
		return pc, nil, fmt.Errorf("ice connection did not have selected candidate pair")
	}
	laddr := &net.UDPAddr{IP: net.ParseIP(cp.Local.Address), Port: int(cp.Local.Port)}

	channel := newStream(nil, rawHandshakeChannel, detached, laddr, raddr)
	// the local address of the selected candidate pair should be the
	// local address for the connection, since different datachannels
	// are multiplexed over the same SCTP connection
	localAddr, err := manet.FromNetAddr(channel.LocalAddr())
	if err != nil {
		return pc, nil, err
	}

	// we can only know the remote public key after the noise handshake,
	// but need to set up the callbacks on the peerconnection
	conn, err := newConnection(
		webrtc.DTLSRoleClient,
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
	if err != nil {
		return pc, nil, err
	}

	secConn, err := t.noiseHandshake(ctx, pc, channel, p, remoteHashFunction, false)
	channel.Close()
	if err != nil {
		return pc, conn, err
	}
	conn.setRemotePublicKey(secConn.RemotePublicKey())
	return pc, conn, err
}

func (t *WebRTCTransport) getCertificateFingerprint() (webrtc.DTLSFingerprint, error) {
	fps, err := t.webrtcConfig.Certificates[0].GetFingerprints()
	if err != nil {
		return webrtc.DTLSFingerprint{}, err
	}
	return fps[0], nil
}

func (t *WebRTCTransport) generateNoisePrologue(pc *webrtc.PeerConnection, hash crypto.Hash, inbound bool) ([]byte, error) {
	raw := pc.SCTP().Transport().GetRemoteCertificate()
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return nil, err
	}

	localFp, err := t.getCertificateFingerprint()
	if err != nil {
		return nil, err
	}

	remoteFp, err := fingerprint.Fingerprint(cert, hash)
	if err != nil {
		return nil, err
	}
	remoteFp = replaceAll(strings.ToLower(remoteFp), byte(':'))
	remoteFpBytes, err := hex.DecodeString(remoteFp)
	if err != nil {
		return nil, err
	}

	local := replaceAll(localFp.Value, byte(':'))
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

func (t *WebRTCTransport) noiseHandshake(ctx context.Context, pc *webrtc.PeerConnection, datachannel *webRTCStream, peer peer.ID, hash crypto.Hash, inbound bool) (secureConn sec.SecureConn, err error) {
	prologue, err := t.generateNoisePrologue(pc, hash, inbound)
	if err != nil {
		return nil, fmt.Errorf("could not generate prologue: %w", err)
	}
	sessionTransport, err := t.noiseTpt.WithSessionOptions(
		noise.Prologue(prologue),
		noise.DisablePeerIDCheck(),
	)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate transport: %w", err)
	}
	if inbound {
		secureConn, err = sessionTransport.SecureOutbound(ctx, datachannel, peer)
		if err != nil {
			err = fmt.Errorf("failed to secure inbound [noise outbound]: %w %v", err, ctx.Value("id"))
			return
		}
	} else {
		secureConn, err = sessionTransport.SecureInbound(ctx, datachannel, peer)
		if err != nil {
			err = fmt.Errorf("failed to secure outbound [noise inbound]: %w %v", err, ctx.Value("id"))
			return
		}
	}
	return secureConn, nil
}
