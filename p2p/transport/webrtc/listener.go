package libp2pwebrtc

import (
	"context"
	"crypto"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/transport/webrtc/udpmux"

	tpt "github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"

	"github.com/pion/ice/v2"
	// pionlogger "github.com/pion/logging"
	"github.com/pion/webrtc/v3"
)

var _ tpt.Listener = &listener{}

const (
	candidateSetupTimeout         = 20 * time.Second
	DefaultMaxInFlightConnections = 10
)

type candidateAddr struct {
	ufrag string
	raddr *net.UDPAddr
}

type listener struct {
	transport *WebRTCTransport

	mux ice.UDPMux

	config                    webrtc.Configuration
	localFingerprint          webrtc.DTLSFingerprint
	localFingerprintMultibase string

	localAddr      net.Addr
	localMultiaddr ma.Multiaddr

	// buffered incoming connections
	acceptQueue chan tpt.CapableConn
	// Accepting a connection requires instantiating a peerconnection
	// and a noise connection which is expensive. We therefore limit
	// the number of in-flight connection requests. A connection
	// is considered to be in flight from the instant it is handled
	// until it is dequed by a call to Accept, or errors out in some
	// way.
	//
	inFlightConnections    uint32
	maxInFlightConnections uint32

	// used to control the lifecycle of the listener
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newListener(transport *WebRTCTransport, laddr ma.Multiaddr, socket net.PacketConn, config webrtc.Configuration) (*listener, error) {
	candidateChan := make(chan candidateAddr, transport.maxInFlightConnections)
	localFingerprints, err := config.Certificates[0].GetFingerprints()
	if err != nil {
		return nil, err
	}

	localMh, err := hex.DecodeString(strings.ReplaceAll(localFingerprints[0].Value, ":", ""))
	if err != nil {
		return nil, err
	}
	localMhBuf, _ := multihash.Encode(localMh, multihash.SHA2_256)
	localFpMultibase, _ := multibase.Encode(multibase.Base64url, localMhBuf)

	ctx, cancel := context.WithCancel(context.Background())
	mux := udpmux.NewUDPMux(socket, func(ufrag string, addr net.Addr) {
		// Push to the candidateChan asynchronously to avoid blocking the mux goroutine
		// on candidates being processed. This can cause new connections to fail at high
		// throughput but will allow packets for existing connections to be processed.
		select {
		case candidateChan <- candidateAddr{ufrag: ufrag, raddr: addr.(*net.UDPAddr)}:
		default:
			log.Debug("candidate chan full, dropping incoming candidate")
		}
	})

	l := &listener{
		mux:                       mux,
		transport:                 transport,
		config:                    config,
		localFingerprint:          localFingerprints[0],
		localFingerprintMultibase: localFpMultibase,
		localMultiaddr:            laddr,
		ctx:                       ctx,
		cancel:                    cancel,
		localAddr:                 socket.LocalAddr(),
		acceptQueue:               make(chan tpt.CapableConn, transport.maxInFlightConnections),
		inFlightConnections:       0,
		maxInFlightConnections:    transport.maxInFlightConnections,
	}

	l.wg.Add(1)
	go l.handleIncomingCandidates(candidateChan)
	return l, err
}

func (l *listener) handleIncomingCandidates(candidateChan chan candidateAddr) {
	defer l.wg.Done()
	for {
		select {
		case <-l.ctx.Done():
			return
		case addr := <-candidateChan:
			if atomic.LoadUint32(&l.inFlightConnections) >= l.maxInFlightConnections {
				// TODO: should we send an error STUN response here? It seems like Pion and browsers will retry
				// STUN binding requests even when an error response is received.
				// Refer: https://github.com/pion/ice/blob/master/agent.go#L1045-L1131
				log.Warnf("server is busy, rejecting incoming connection from: %s", addr.raddr)
				continue
			}
			atomic.AddUint32(&l.inFlightConnections, 1)
			go func() {
				ctx, cancel := context.WithTimeout(l.ctx, candidateSetupTimeout)
				defer cancel()
				conn, err := l.handleCandidate(ctx, addr)
				if err != nil {
					log.Debugf("could not accept connection: %s: %v", addr.ufrag, err)
					atomic.AddUint32(&l.inFlightConnections, ^uint32(0))
					return
				}
				select {
				case l.acceptQueue <- conn:
				default:
					log.Warnf("could not push connection")
					atomic.AddUint32(&l.inFlightConnections, ^uint32(0))
					conn.Close()
				}
			}()
		}
	}
}

func (l *listener) Accept() (tpt.CapableConn, error) {
	select {
	case <-l.ctx.Done():
		return nil, os.ErrClosed
	case conn := <-l.acceptQueue:
		atomic.AddUint32(&l.inFlightConnections, ^uint32(0))
		return conn, nil
	}
}

func (l *listener) Close() error {
	select {
	case <-l.ctx.Done():
		return nil
	default:
	}
	l.cancel()
	l.wg.Wait()
	return nil
}

func (l *listener) Addr() net.Addr {
	return l.localAddr
}

func (l *listener) Multiaddr() ma.Multiaddr {
	return l.localMultiaddr
}

func (l *listener) handleCandidate(ctx context.Context, addr candidateAddr) (tpt.CapableConn, error) {
	remoteMultiaddr, err := manet.FromNetAddr(addr.raddr)
	if err != nil {
		return nil, err
	}
	scope, err := l.transport.rcmgr.OpenConnection(network.DirInbound, false, remoteMultiaddr)
	if err != nil {
		return nil, err
	}
	pc, conn, err := l.setupConnection(ctx, scope, remoteMultiaddr, addr)
	if err != nil {
		scope.Done()
		if pc != nil {
			_ = pc.Close()
		}
		return nil, err
	}
	return conn, nil
}

func (l *listener) setupConnection(ctx context.Context, scope network.ConnManagementScope, remoteMultiaddr ma.Multiaddr, addr candidateAddr) (*webrtc.PeerConnection, tpt.CapableConn, error) {
	settingEngine := webrtc.SettingEngine{}

	// suppress pion logs
	// loggerFactory := pionlogger.NewDefaultLoggerFactory()
	// loggerFactory.DefaultLogLevel = pionlogger.LogLevelDisabled
	// settingEngine.LoggerFactory = loggerFactory

	settingEngine.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
	settingEngine.SetICECredentials(addr.ufrag, addr.ufrag)
	settingEngine.SetLite(true)
	settingEngine.SetICEUDPMux(l.mux)
	settingEngine.DisableCertificateFingerprintVerification(true)
	settingEngine.SetICETimeouts(l.transport.peerConnectionDisconnectedTimeout, l.transport.peerConnectionFailedTimeout, l.transport.peerConnectionKeepaliveTimeout)
	settingEngine.DetachDataChannels()

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	pc, err := api.NewPeerConnection(l.config)
	if err != nil {
		return pc, nil, err
	}

	negotiated, id := hansdhakeChannelNegotiated, handshakeChannelId
	rawDatachannel, err := pc.CreateDataChannel("", &webrtc.DataChannelInit{
		Negotiated: &negotiated,
		ID:         &id,
	})
	if err != nil {
		return pc, nil, err
	}

	errC := awaitPeerConnectionOpen(addr.ufrag, pc)
	// we infer the client sdp from the incoming STUN connectivity check
	// by setting the ice-ufrag equal to the incoming check.
	clientSdpString := renderClientSdp(addr.raddr, addr.ufrag)
	clientSdp := webrtc.SessionDescription{SDP: clientSdpString, Type: webrtc.SDPTypeOffer}
	pc.SetRemoteDescription(clientSdp)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return pc, nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		return pc, nil, err
	}

	select {
	case <-ctx.Done():
		return pc, nil, ctx.Err()
	case err := <-errC:
		if err != nil {
			return pc, nil, fmt.Errorf("peerconnection error: %w", err)
		}

	}

	rwc, err := getDetachedChannel(ctx, rawDatachannel)
	if err != nil {
		return pc, nil, err
	}

	handshakeChannel := newStream(nil, rawDatachannel, rwc, l.localAddr, addr.raddr)
	// The connection is instantiated before performing the Noise handshake. This is
	// to handle the case where the remote is faster and attempts to initiate a stream
	// before the ondatachannel callback can be set.
	conn, err := newConnection(
		webrtc.DTLSRoleServer,
		pc,
		l.transport,
		scope,
		l.transport.localPeerId,
		l.transport.privKey,
		l.localMultiaddr,
		"",
		nil,
		remoteMultiaddr,
	)
	if err != nil {
		return pc, nil, err
	}

	// we do not yet know A's peer ID so accept any inbound
	secureConn, err := l.transport.noiseHandshake(ctx, pc, handshakeChannel, "", crypto.SHA256, true)
	handshakeChannel.Close()
	if err != nil {
		return pc, nil, err
	}

	// earliest point where we know the remote's peerID
	err = scope.SetPeer(secureConn.RemotePeer())
	if err != nil {
		return pc, nil, err
	}

	conn.setRemotePeer(secureConn.RemotePeer())
	conn.setRemotePublicKey(secureConn.RemotePublicKey())

	return pc, conn, err
}
