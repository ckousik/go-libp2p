package libp2pwebrtc

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"

	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	tpt "github.com/libp2p/go-libp2p/core/transport"
	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
)

var _ tpt.CapableConn = &connection{}

const (
	maxAcceptQueueLen = 1000
)

type acceptStream struct {
	stream  datachannel.ReadWriteCloser
	channel *webrtc.DataChannel
}

type connection struct {
	// debug identifier for the connection
	dbgId     int
	pc        *webrtc.PeerConnection
	transport *WebRTCTransport
	scope     network.ConnManagementScope

	localPeer      peer.ID
	privKey        ic.PrivKey
	localMultiaddr ma.Multiaddr

	remotePeer      peer.ID
	remoteKey       ic.PubKey
	remoteMultiaddr ma.Multiaddr

	m       sync.Mutex
	streams map[uint16]*webRTCStream

	acceptQueue chan acceptStream

	ctx    context.Context
	cancel context.CancelFunc

	nextID uint32
}

func newConnection(
	role webrtc.DTLSRole,
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

	ctx, cancel := context.WithCancel(context.Background())

	nextID := 0
	if role == webrtc.DTLSRoleClient {
		nextID++
	}
	acceptQueue := make(chan acceptStream, maxAcceptQueueLen)
	conn := &connection{
		dbgId:     rand.Intn(65536),
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
		streams:         make(map[uint16]*webRTCStream),

		acceptQueue: acceptQueue,
		nextID:      uint32(nextID),
	}

	pc.OnConnectionStateChange(conn.onConnectionStateChange)
	pc.OnDataChannel(conn.handleDataChannel)

	return conn, nil
}

func (c *connection) handleDataChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		rwc, err := dc.Detach()
		if err != nil {
			log.Warnf("could not detach datachannel: id: %d", *dc.ID())
			return
		}
		select {
		case c.acceptQueue <- acceptStream{rwc, dc}:
		default:
			log.Warnf("connection busy, rejecting stream")
			// reject stream without instantiating a delimited writer
			_, _ = writeMessage(rwc, &pb.Message{Flag: pb.Message_RESET.Enum()})
			rwc.Close()
		}
	})

}

func (c *connection) resetStreams() {
	if c.IsClosed() {
		return
	}
	c.m.Lock()
	defer c.m.Unlock()
	for k, stream := range c.streams {
		// reset the streams, but we do not need to be notified
		// of stream closure
		stream.close(true, false)
		delete(c.streams, k)
	}

}

// ConnState implements transport.CapableConn
func (c *connection) ConnState() network.ConnectionState {
	return network.ConnectionState{
		Transport: "webrtc",
	}
}

// Close closes the underlying peerconnection.
func (c *connection) Close() error {
	if c.IsClosed() {
		return nil
	}

	c.scope.Done()
	c.cancel()
	return c.pc.Close()
}

func (c *connection) IsClosed() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

func (c *connection) OpenStream(ctx context.Context) (network.MuxedStream, error) {
	if c.IsClosed() {
		return nil, os.ErrClosed
	}

	// id := uint16(atomic.AddUint32(&c.nextID, 2))
	// dc, err := c.pc.CreateDataChannel("", &webrtc.DataChannelInit{ID: &id})
	dc, err := c.pc.CreateDataChannel("", &webrtc.DataChannelInit{ID: nil})
	if err != nil {
		return nil, err
	}
	rwc, err := c.detachChannel(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("could not open stream: %w", err)
	}
	stream := newStream(
		c,
		dc,
		rwc,
		nil,
		nil,
	)
	c.addStream(stream)
	return stream, nil
}

func (c *connection) AcceptStream() (network.MuxedStream, error) {
	select {
	case <-c.ctx.Done():
		return nil, os.ErrClosed
	case accStream := <-c.acceptQueue:
		stream := newStream(
			c,
			accStream.channel,
			accStream.stream,
			nil,
			nil,
		)
		c.addStream(stream)
		return stream, nil
	}
}

// implement network.ConnSecurity
func (c *connection) LocalPeer() peer.ID {
	return c.localPeer
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

func (c *connection) addStream(stream *webRTCStream) {
	c.m.Lock()
	defer c.m.Unlock()
	c.streams[stream.id] = stream
}

func (c *connection) removeStream(id uint16) {
	c.m.Lock()
	defer c.m.Unlock()
	delete(c.streams, id)
}

func (c *connection) onConnectionStateChange(state webrtc.PeerConnectionState) {
	log.Debugf("[%s][%d] handling peerconnection state: %s", c.localPeer, c.dbgId, state)
	if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
		// reset any streams
		c.resetStreams()
		c.cancel()
		// is done safe to call twice?
		c.scope.Done()
		c.pc.Close()
	}
}

// detachChannel detaches an outgoing channel by taking into account the context
// passed to `OpenStream` as well the closure of the underlying peerconnection
func (c *connection) detachChannel(ctx context.Context, dc *webrtc.DataChannel) (rwc datachannel.ReadWriteCloser, err error) {
	done := make(chan struct{})
	var cls sync.Once
	defer cls.Do(func() {
		close(done)
	})
	// OnOpen will return immediately for detached datachannels
	// refer: https://github.com/pion/webrtc/blob/7ab3174640b3ce15abebc2516a2ca3939b5f105f/datachannel.go#L278-L282
	dc.OnOpen(func() {
		rwc, err = dc.Detach()
		// this is safe since the function should return instantly if the peerconnection is closed
		cls.Do(func() { close(done) })
	})
	select {
	case <-c.ctx.Done():
		return nil, fmt.Errorf("connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
	}
	return
}

// only used during connection setup
func (c *connection) setRemotePeer(id peer.ID) {
	c.remotePeer = id
}

// only used during connection setup
func (c *connection) setRemotePublicKey(key ic.PubKey) {
	c.remoteKey = key
}
