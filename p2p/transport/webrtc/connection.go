package libp2pwebrtc

import (
	"context"
	"os"

	"github.com/google/uuid"
	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	tpt "github.com/libp2p/go-libp2p-core/transport"
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

	streams []network.MuxedStream

	accept chan network.MuxedStream

	ctx    context.Context
	cancel context.CancelFunc
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
	accept := make(chan network.MuxedStream, 1)

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
		streams:         []network.MuxedStream{},

		accept: accept,
	}
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Debugf("[%s] incoming datachannel: %s", localPeer, dc.Label())
		dc.OnOpen(func() {
			dcrwc, err := dc.Detach()
			if err != nil {
				// cannot accept a non-detached datachannel
				return
			}

			stream := newDataChannel(dcrwc, pc, nil, nil)
			conn.streams = append(conn.streams, stream)
			accept <- stream
		})
	})
	return conn, nil
}

// Implement network.MuxedConn

func (c *connection) Close() error {
	if c.IsClosed() {
		return nil
	}

	_ = c.pc.Close()
	c.scope.Done()
	c.cancel()
	// cleanup routine
	go func() {
		for _, stream := range c.streams {
			_ = stream.Close()
		}
	}()
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
	dc, err := c.pc.CreateDataChannel(label, nil)
	if err != nil {
		return nil, err
	}
	result := make(chan struct {
		network.MuxedStream
		error
	}, 1)
	dc.OnOpen(func() {
		log.Debugf("[%s] opened new datachannel: %s", c.localPeer, dc.Label())
		rwc, err := dc.Detach()
		if err != nil {
			result <- struct {
				network.MuxedStream
				error
			}{nil, err}
			return
		}

		stream := newDataChannel(rwc, c.pc, nil, nil)
		c.streams = append(c.streams, stream)
		result <- struct {
			network.MuxedStream
			error
		}{stream, err}
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
