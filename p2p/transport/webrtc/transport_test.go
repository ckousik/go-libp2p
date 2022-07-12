package libp2pwebrtc

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	tpt "github.com/libp2p/go-libp2p-core/transport"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

func getTransport(t *testing.T) (tpt.Transport, peer.ID) {
	privKey, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	require.NoError(t, err)
	rcmgr := network.NullResourceManager
	transport, err := New(privKey, rcmgr)
	require.NoError(t, err)
	peerID, err := peer.IDFromPrivateKey(privKey)
	require.NoError(t, err)
	return transport, peerID
}

func TestTransportCanDial(t *testing.T) {
	tr, _ := getTransport(t)
	invalid := []string{
		"/ip4/1.2.3.4/udp/1234/webrtc",
		"/dns/test.test/udp/1234/webrtc",
		"/dns/test.test/udp/1234/webrtc/certhash/uEiAsGPzpiPGQzSlVHRXrUCT5EkTV7YFrV4VZ3hpEKTd_zg",
	}

	valid := []string{
		"/ip4/1.2.3.4/udp/1234/webrtc/certhash/uEiAsGPzpiPGQzSlVHRXrUCT5EkTV7YFrV4VZ3hpEKTd_zg",
		"/ip6/0:0:0:0:0:0:0:1/udp/1234/webrtc/certhash/uEiAsGPzpiPGQzSlVHRXrUCT5EkTV7YFrV4VZ3hpEKTd_zg",
		"/ip6/::1/udp/1234/webrtc/certhash/uEiAsGPzpiPGQzSlVHRXrUCT5EkTV7YFrV4VZ3hpEKTd_zg",
	}

	for _, addr := range invalid {
		ma, err := multiaddr.NewMultiaddr(addr)
		require.NoError(t, err)
		require.Equal(t, false, tr.CanDial(ma))
	}

	for _, addr := range valid {
		ma, err := multiaddr.NewMultiaddr(addr)
		require.NoError(t, err)
		require.Equal(t, true, tr.CanDial(ma), addr)
	}
}

func TestTransportCanListen(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/udp/0/webrtc")
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	tr1, connectingPeer := getTransport(t)

	go func() {
		_, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
	}()

	conn, err := listener.Accept()
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, connectingPeer, conn.RemotePeer())
}

func TestTransportCanListenMultiple(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/udp/0/webrtc")
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		go func() {
			ctr, _ := getTransport(t)
			conn, err := ctr.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
			require.NoError(t, err)
			require.Equal(t, conn.RemotePeer(), listeningPeer)
		}()
	}

	for i := 0; i < 10; i++ {
		_, err := listener.Accept()
		require.NoError(t, err)
	}
}

func TestTransportListenerCanCreateStreams(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/udp/0/webrtc")
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	tr1, connectingPeer := getTransport(t)

	go func() {
		conn, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
		stream, err := conn.AcceptStream()
		require.NoError(t, err)
		buf := make([]byte, 100)
		n, err := stream.Read(buf)
		require.NoError(t, err)
		require.Equal(t, "test", string(buf[:n]))
	}()

	conn, err := listener.Accept()
	require.NoError(t, err)

	require.Equal(t, connectingPeer, conn.RemotePeer())

	stream, err := conn.OpenStream(context.Background())
	require.NoError(t, err)
	_, err = stream.Write([]byte("test"))
	require.NoError(t, err)
}

func TestTransportDialerCanCreateStreams(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/udp/0/webrtc")
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	tr1, connectingPeer := getTransport(t)

	go func() {
		conn, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
		stream, err := conn.OpenStream(context.Background())
		require.NoError(t, err)
		_, err = stream.Write([]byte("test"))
		require.NoError(t, err)
	}()

	lconn, err := listener.Accept()
	require.NoError(t, err)
	require.Equal(t, connectingPeer, lconn.RemotePeer())

	stream, err := lconn.AcceptStream()
	require.NoError(t, err)
	buf := make([]byte, 100)
	n, err := stream.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "test", string(buf[:n]))
}
