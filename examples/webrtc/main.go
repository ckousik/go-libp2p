package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/libp2p/go-libp2p/p2p/protocol/ping"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	webrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
)

func main() {
	host := createHost()
	defer host.Close()
	// fmt.Println("listening on: ", host.Network().ListenAddresses())
	remoteInfo := peer.AddrInfo{
		ID:    host.ID(),
		Addrs: host.Network().ListenAddresses(),
	}

	remoteAddrs, _ := peer.AddrInfoToP2pAddrs(&remoteInfo)
	fmt.Println("p2p addr: ", remoteAddrs)

	// ctx, cancel := context.WithCancel(context.Background())

	// go dialHost(ctx, host)

	fmt.Println("press Ctrl+C to quit")
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch
	// cancel()
}

func dialHost(ctx context.Context, server host.Host) {
	client, err := libp2p.New(
		libp2p.Transport(webrtc.New),
		libp2p.DisableRelay(),
		libp2p.Ping(true),
	)
	if err != nil {
		panic(err)
	}

	if err = server.ID().Validate(); err != nil {
		panic(err)
	}

	remoteInfo := peer.AddrInfo{
		ID:    server.ID(),
		Addrs: server.Network().ListenAddresses(),
	}

	remoteAddrs, err := peer.AddrInfoToP2pAddrs(&remoteInfo)
	fmt.Println("p2p addr: ", remoteAddrs)

	fmt.Println("=========================== connecting ==============================")
	err = client.Connect(context.Background(), remoteInfo)
	if err != nil {
		panic(err)
	}
	fmt.Println("============================ connected ==============================")

	resultChan := ping.Ping(ctx, server, server.ID())

	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
		case result := <-resultChan:
			if result.Error != nil {
				fmt.Println("ping error", result.Error)
			} else {
				fmt.Println("pinged", remoteInfo.Addrs, " in ", result.RTT)
			}
		}

	}

}

func createHost() host.Host {
	h, err := libp2p.New(
		libp2p.Transport(webrtc.New),
		libp2p.ListenAddrStrings(
			// "/ip4/0.0.0.0/udp/0/webrtc", // a UDP endpoint for the WebRTC Transport
			"/ip4/192.168.1.16/udp/0/webrtc", // a UDP endpoint for the WebRTC Transport
		),
		libp2p.DisableRelay(),
		libp2p.Ping(true),
	)
	if err != nil {
		panic(err)
	}

	return h
}
