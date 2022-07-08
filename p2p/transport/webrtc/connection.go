package libp2pwebrtc

import (
	"context"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/webrtc/v3"
)

type connection struct {
	conn *webrtc.PeerConnection
	// ConnSecurity
	idKey        crypto.PrivKey
	remotePeerID peer.ID

	// ConnMultiaddrs
	localMultiaddr, remoteMultiaddr ma.Multiaddr

	closeChan chan struct{}
}

func newConnection(conn *webrtc.PeerConnection) (*connection, error) {
	return nil, nil
}

// Implement network.ConnSecurity
func (c *connection) LocalPeer() peer.ID {
	id, err := peer.IDFromPrivateKey(c.idKey)
	if err != nil {
		return ""
	}
	return id
}

func (c *connection) LocalPrivateKey() crypto.PrivKey {
	return c.idKey
}

func (c *connection) RemotePeer() peer.ID {
	return c.remotePeerID
}

func (c *connection) RemotePublicKey() crypto.PubKey {
	key, err := c.remotePeerID.ExtractPublicKey()
	if err != nil {
		return nil
	}
	return key
}

// Implement network.ConnMultiaddrs
func (c *connection) LocalMultiaddr() ma.Multiaddr {
	return c.localMultiaddr
}

func (c *connection) RemoteMultiaddr() ma.Multiaddr {
	return c.remoteMultiaddr
}

// Implement network.MuxedConn

func (c *connection) Close() error {
	select {
	case <-c.closeChan:
		return nil
	default:
		close(c.closeChan)
	}
	return c.conn.Close()
}

func (c *connection) IsClosed() bool {
	select {
	case <-c.closeChan:
		return true
	default:
	}
	return false
}

func (c *connection) OpenStream(ctx context.Context) (network.MuxedStream, error) {
	return nil, nil
}

