package libp2pwebrtc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	tpt "github.com/libp2p/go-libp2p/core/transport"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multihash"
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

func TestTransportWebRTCCanDial(t *testing.T) {
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

func TestTransportWebRTC_ListenFailsOnNonWebRTCMultiaddr(t *testing.T) {
	tr, _ := getTransport(t)
	testAddrs := []string{
		"/ip4/0.0.0.0/udp/0",
		"/ip4/0.0.0.0/tcp/0/wss",
	}
	for _, addr := range testAddrs {
		listenMultiaddr, err := multiaddr.NewMultiaddr(addr)
		require.NoError(t, err)
		listener, err := tr.Listen(listenMultiaddr)
		require.Error(t, err)
		require.Nil(t, listener)
	}
}

func TestTransportWebRTC_CanListenSingle(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	tr1, connectingPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/0/webrtc", listenerIp))
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	go func() {
		_, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
	}()

	conn, err := listener.Accept()
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, connectingPeer, conn.RemotePeer())
}

func TestTransportWebRTC_CanListenMultiple(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/0/webrtc", listenerIp))
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)
	count := 5

	for i := 0; i < count; i++ {
		go func() {
			ctr, _ := getTransport(t)
			conn, err := ctr.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
			require.NoError(t, err)
			require.Equal(t, conn.RemotePeer(), listeningPeer)
		}()
	}

	for i := 0; i < count; i++ {
		_, err := listener.Accept()
		require.NoError(t, err)
		t.Logf("listener accepted connection: %d", i)
	}
}

func TestTransportWebRTC_ListenerCanCreateStreams(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	tr1, connectingPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/0/webrtc", listenerIp))
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	go func() {
		conn, err := listener.Accept()
		require.NoError(t, err)
		t.Logf("listener accepted connection")

		require.Equal(t, connectingPeer, conn.RemotePeer())

		stream, err := conn.OpenStream(context.Background())
		require.NoError(t, err)
		t.Logf("listener opened stream")
		_, err = stream.Write([]byte("test"))
		require.NoError(t, err)
	}()

	streamChan := make(chan network.MuxedStream)
	go func() {
		conn, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
		t.Logf("connection opened by dialer")
		stream, err := conn.AcceptStream()
		require.NoError(t, err)
		t.Logf("dialer accepted stream")
		streamChan <- stream
	}()

	var stream network.MuxedStream
	select {
	case stream = <-streamChan:
	case <-time.After(3 * time.Second):
		t.Fatal("stream opening timed out")
	}
	buf := make([]byte, 100)
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := stream.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "test", string(buf[:n]))

}

func TestTransportWebRTC_DialerCanCreateStreams(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/0/webrtc", listenerIp))
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	tr1, connectingPeer := getTransport(t)
	done := make(chan struct{})

	go func() {
		lconn, err := listener.Accept()
		require.NoError(t, err)
		t.Logf("listener accepted connection")
		require.Equal(t, connectingPeer, lconn.RemotePeer())

		stream, err := lconn.AcceptStream()
		require.NoError(t, err)
		t.Logf("listener accepted stream")
		buf := make([]byte, 100)
		n, err := stream.Read(buf)
		require.NoError(t, err)
		require.Equal(t, "test", string(buf[:n]))

		done <- struct{}{}
	}()

	go func() {
		conn, err := tr1.Dial(context.Background(), listener.Multiaddr(), listeningPeer)
		require.NoError(t, err)
		t.Logf("dialer opened connection")
		stream, err := conn.OpenStream(context.Background())
		require.NoError(t, err)
		t.Logf("dialer opened stream")
		_, err = stream.Write([]byte("test"))
		require.NoError(t, err)

	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}

}

func TestTransportWebRTC_PeerConnectionDTLSFailed(t *testing.T) {
	tr, listeningPeer := getTransport(t)
	listenMultiaddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/0/webrtc", listenerIp))
	require.NoError(t, err)
	listener, err := tr.Listen(listenMultiaddr)
	require.NoError(t, err)

	tr1, _ := getTransport(t)

	go func() {
		listener.Accept()
	}()

	badMultiaddr, _ := multiaddr.SplitFunc(listener.Multiaddr(), func(component multiaddr.Component) bool {
		return component.Protocol().Code == multiaddr.P_CERTHASH
	})

	encodedCerthash, err := multihash.Encode(defaultMultihash.Digest, defaultMultihash.Code)
	require.NoError(t, err)
	badEncodedCerthash, err := multibase.Encode(multibase.Base58BTC, encodedCerthash)
	require.NoError(t, err)
	badCerthash, err := multiaddr.NewMultiaddr(fmt.Sprintf("/certhash/%s", badEncodedCerthash))
	require.NoError(t, err)
	badMultiaddr = badMultiaddr.Encapsulate(badCerthash)

	conn, err := tr1.Dial(context.Background(), badMultiaddr, listeningPeer)
	require.Nil(t, conn)
	require.Error(t, err)

	webrtcErr, ok := err.(*webRTCTransportError)
	require.True(t, ok, "could not cast to webRTCTransportError")
	require.Equal(t, webrtcErr.kind, errKindConnectionFailed)
	require.Contains(t, webrtcErr.message, "failed")

}
