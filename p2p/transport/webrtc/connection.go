package libp2pwebrtc

import (
	"context"
	"os"
	"sync"

	"github.com/google/uuid"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	tpt "github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/webrtc/v3"
)

var _ tpt.CapableConn = &connection{}

type connection struct {
	pc        *webrtc.PeerConnection
	transport *WebRTCTransport
	scope     network.ConnManagementScope

	localPeer      peer.ID
	privKey        ic.PrivKey
	localMultiaddr ma.Multiaddr

	remotePeer      peer.ID
	remoteKey       ic.PubKey
	remoteMultiaddr ma.Multiaddr

	streams map[uint16]*dataChannel

	accept chan network.MuxedStream

	ctx    context.Context
	cancel context.CancelFunc
	m      sync.Mutex
}

func newConnection(
	pc *webrtc.PeerConnection,
	transport *WebRTCTransport,
	scope network.ConnManagementScope,

	localPeer peer.ID,
	privKey ic.PrivKey,
	localMultiaddr ma.Multiaddr,

	remotePeer peer.ID,
	remoteKey ic.PubKey,
	remoteMultiaddr ma.Multiaddr,
) (*connection, error) {
	accept := make(chan network.MuxedStream, 10)

	ctx, cancel := context.WithCancel(context.Background())

	conn := &connection{
		pc:        pc,
		transport: transport,
		scope:     scope,

		localPeer:      localPeer,
		privKey:        privKey,
		localMultiaddr: localMultiaddr,

		remotePeer:      remotePeer,
		remoteKey:       remoteKey,
		remoteMultiaddr: remoteMultiaddr,
		ctx:             ctx,
		cancel:          cancel,
		streams:         make(map[uint16]*dataChannel),

		accept: accept,
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Debugf("[%s] incoming datachannel: %s", localPeer, dc.Label())
		id := *dc.ID()
		stream := newDataChannel(dc, pc, nil, nil)
		dc.OnOpen(func() {
			conn.addStream(id, stream)
			accept <- stream
		})

		dc.OnClose(func() {
			stream.remoteClosed()
			conn.removeStream(id)
		})
	})

	return conn, nil
}

// Implement network.MuxedConn

func (c *connection) Close() error {
	if c.IsClosed() {
		return nil
	}

	c.scope.Done()
	// cleanup routine
	for _, stream := range c.streams {
		_ = stream.Close()
	}
	c.cancel()
	_ = c.pc.Close()
	return nil
}

func (c *connection) IsClosed() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
	}
	return false
}

func (c *connection) OpenStream(ctx context.Context) (network.MuxedStream, error) {
	if c.IsClosed() {
		return nil, os.ErrClosed
	}

	label := uuid.New().String()
	result := make(chan struct {
		network.MuxedStream
		error
	})
	dc, err := c.pc.CreateDataChannel(label, nil)
	if err != nil {
		return nil, err
	}

	streamId := *dc.ID()
	stream := newDataChannel(dc, c.pc, nil, nil)
	dc.OnOpen(func() {
		c.addStream(streamId, stream)
		result <- struct {
			network.MuxedStream
			error
		}{stream, err}
	})

	dc.OnClose(func() {
		stream.remoteClosed()
		c.removeStream(streamId)
	})

	select {
	case <-ctx.Done():
		_ = dc.Close()
		return nil, ctx.Err()
	case r := <-result:
		return r.MuxedStream, r.error
	}
}

func (c *connection) AcceptStream() (network.MuxedStream, error) {
	select {
	case <-c.ctx.Done():
		return nil, os.ErrClosed
	case stream := <-c.accept:
		return stream, nil
	}
}

// implement network.ConnSecurity
func (c *connection) LocalPeer() peer.ID {
	return c.localPeer
}

// only used during setup
func (c *connection) setRemotePeer(id peer.ID) {
	c.remotePeer = id
}

func (c *connection) LocalPrivateKey() ic.PrivKey {
	return c.privKey
}

func (c *connection) RemotePeer() peer.ID {
	return c.remotePeer
}

func (c *connection) RemotePublicKey() ic.PubKey {
	return c.remoteKey
}

func (c *connection) setRemotePublicKey(key ic.PubKey) {
	c.remoteKey = key
}

// implement network.ConnMultiaddrs
func (c *connection) LocalMultiaddr() ma.Multiaddr {
	return c.localMultiaddr
}

func (c *connection) RemoteMultiaddr() ma.Multiaddr {
	return c.remoteMultiaddr
}

// implement network.ConnScoper
func (c *connection) Scope() network.ConnScope {
	return c.scope
}

func (c *connection) Transport() tpt.Transport {
	return c.transport
}

func (c *connection) addStream(id uint16, stream *dataChannel) {
	c.m.Lock()
	defer c.m.Unlock()
	c.streams[id] = stream
}

func (c *connection) removeStream(id uint16) {
	c.m.Lock()
	defer c.m.Unlock()
	delete(c.streams, id)
}
