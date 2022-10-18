package libp2pwebrtc

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	tpt "github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"

	"github.com/pion/ice/v2"
	"github.com/pion/webrtc/v3"
)

var (
	// since verification of the remote fingerprint is deferred until
	// the noise handshake, a multihash with an arbitrary value is considered
	// as the remote fingerprint during the intial PeerConnection connection
	// establishment.
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

// / implement net.Listener
type listener struct {
	transport                 *WebRTCTransport
	config                    webrtc.Configuration
	localFingerprint          webrtc.DTLSFingerprint
	localFingerprintMultibase string
	mux                       *udpMuxNewAddr
	ctx                       context.Context
	cancel                    context.CancelFunc
	localMultiaddr            ma.Multiaddr
	connChan                  chan tpt.CapableConn
	wg                        sync.WaitGroup
}

func newListener(transport *WebRTCTransport, laddr ma.Multiaddr, socket net.PacketConn, config webrtc.Configuration) (*listener, error) {
	mux := NewUDPMuxNewAddr(ice.UDPMuxParams{UDPConn: socket}, make(chan candidateAddr))
	localFingerprints, err := config.Certificates[0].GetFingerprints()
	if err != nil {
		return nil, err
	}

	localMh, err := hex.DecodeString(strings.ReplaceAll(localFingerprints[0].Value, ":", ""))
	if err != nil {
		return nil, err
	}
	localMhBuf, _ := multihash.Encode(localMh, multihash.SHA2_256)
	localFpMultibase, _ := multibase.Encode(multibase.Base58BTC, localMhBuf)

	ctx, cancel := context.WithCancel(context.Background())

	l := &listener{
		mux:                       mux,
		transport:                 transport,
		config:                    config,
		localFingerprint:          localFingerprints[0],
		localFingerprintMultibase: localFpMultibase,
		localMultiaddr:            laddr,
		ctx:                       ctx,
		cancel:                    cancel,
		connChan:                  make(chan tpt.CapableConn, 20),
	}

	l.wg.Add(1)
	go l.startAcceptLoop()
	return l, err
}

func (l *listener) startAcceptLoop() {
	defer l.wg.Done()
	for {
		select {
		case <-l.ctx.Done():
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
	case <-l.ctx.Done():
		return nil, os.ErrClosed
	case conn := <-l.connChan:
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
	return l.mux.LocalAddr()
}

func (l *listener) Multiaddr() ma.Multiaddr {
	return l.localMultiaddr
}

func (l *listener) accept(ctx context.Context, addr candidateAddr) (tpt.CapableConn, error) {
	var (
		scope network.ConnManagementScope
		pc    *webrtc.PeerConnection
	)

	cleanup := func() {
		if scope != nil {
			scope.Done()
		}
		if pc != nil {
			_ = pc.Close()
		}
	}

	remoteMultiaddr, err := manet.FromNetAddr(addr.raddr)
	if err != nil {
		return nil, err
	}

	scope, err = l.transport.rcmgr.OpenConnection(network.DirInbound, false, remoteMultiaddr)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
	settingEngine.SetICECredentials(addr.ufrag, addr.ufrag)
	settingEngine.SetLite(true)
	settingEngine.SetICEUDPMux(l.mux)
	settingEngine.DisableCertificateFingerprintVerification(true)
	settingEngine.DetachDataChannels()

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	pc, err = api.NewPeerConnection(l.config)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// signaling channel wraps an error in a struct to make
	// the error nullable.
	signalChan := make(chan struct{ error })
	var wrappedChannel *dataChannel
	var handshakeOnce sync.Once
	// this enforces that the correct data channel label is used
	// for the handshake
	handshakeChannel, err := pc.CreateDataChannel("handshake", &webrtc.DataChannelInit{
		Negotiated: func(v bool) *bool { return &v }(true),
		ID:         func(v uint16) *uint16 { return &v }(0),
	})
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// The raw datachannel is wrapped in the libp2p abstraction
	// as early as possible to allow any messages sent by the remote
	// to be buffered. This is done since the dialer leads the listener
	// in the handshake process, and a faster dialer could have set up
	// their connection and started sending Noise handshake messages before
	// the listener has set up the onmessage callback. In this use case,
	// since the data channels are negotiated out-of-band, they will be
	// instantly in `readyState=open` once the SCTP connection is set up.
	// Therefore, we wrap the datachannel before performing the
	// offer-answer exchange, so any messages sent from the remote get
	// buffered.
	handshakeChannel.OnOpen(func() {
		rwc, err := handshakeChannel.Detach()
		if err != nil {
			signalChan <- struct{ error }{errDatachannel("could not detach", err)}
			return
		}
		wrappedChannel = newDataChannel(
			handshakeChannel,
			rwc,
			pc,
			l.mux.LocalAddr(),
			addr.raddr,
		)
		handshakeOnce.Do(func() {
			signalChan <- struct{ error }{nil}
		})
	})

	// Checking the peerconnection state is not necessary in this case as any
	// error caused while accepting will trigger the onerror callback of the
	// handshake channel.
	handshakeChannel.OnError(func(e error) {
		handshakeOnce.Do(func() {
			signalChan <- struct{ error }{e}

		})
	})

	clientSdpString := renderClientSdp(sdpArgs{
		Addr:        addr.raddr,
		Fingerprint: defaultMultihash,
		Ufrag:       addr.ufrag,
	})
	clientSdp := webrtc.SessionDescription{SDP: clientSdpString, Type: webrtc.SDPTypeOffer}
	pc.SetRemoteDescription(clientSdp)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// await datachannel moving to open state
	select {
	case <-ctx.Done():
		defer cleanup()
		return nil, ctx.Err()
	case signal := <-signalChan:
		if signal.error != nil {
			defer cleanup()
			log.Debugf("datachannel: ", signal.error)
			return nil, errDatachannel("datachannel error", signal.error)
		}
	}

	// The connection is instantiated before performing the Noise handshake. This is
	// to handle the case where the remote is faster and attempts to initiate a stream
	// before the ondatachannel callback can be set.
	conn := newConnection(
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

	// we do not yet know A's peer ID so accept any inbound
	secureConn, err := l.transport.noiseHandshake(ctx, pc, wrappedChannel, "", true)
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// earliest point where we know the remote's peerID
	err = scope.SetPeer(secureConn.RemotePeer())
	if err != nil {
		defer cleanup()
		return nil, err
	}

	conn.setRemotePeer(secureConn.RemotePeer())
	conn.setRemotePublicKey(secureConn.RemotePublicKey())

	return conn, nil
}
