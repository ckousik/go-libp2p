package libp2pwebrtc

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/ice/v2"
	"github.com/pion/logging"
	"github.com/pion/stun"
)

func TestUdpMuxNewAddrNewStun(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: listenerIp, Port: 0})
	if err != nil {
		panic(err)
	}

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: dialerIp, Port: 0})
	loggerFactory := logging.NewDefaultLoggerFactory()
	loggerFactory.Writer = os.Stdout
	loggerFactory.DefaultLogLevel = logging.LogLevelDebug

	logger := loggerFactory.NewLogger("mux-test")

	newAddrChan := make(chan candidateAddr, 1)
	_ = NewUDPMuxNewAddr(ice.UDPMuxParams{UDPConn: serverConn, Logger: logger}, newAddrChan)

	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	go func() {
		<-time.After(1 * time.Second)
		msg := stun.MustBuild(
			stun.TransactionID,
			stun.BindingRequest,
			ice.AttrControl{Role: ice.Controlling},
			stun.NewUsername(fmt.Sprintf("%s:%s", certhash, certhash)),
		)
		msg.Encode()
		_, err := clientConn.WriteTo(msg.Raw, serverConn.LocalAddr())
		if err != nil {
			panic(err)
		}
	}()

	select {
	case addr := <-newAddrChan:
		hash := addr.ufrag
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(hash, certhash) {
			t.Fatalf("expected hash: %s, received: %s", certhash, hash)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("test timed out")
	}
}
