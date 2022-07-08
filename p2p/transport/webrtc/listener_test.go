package libp2pwebrtc

import (
	// "fmt"
	// "log"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/sec"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/pion/ice/v2"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v3"

	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
)

func setupMux() (*UDPMuxNewAddr, chan candidateAddr) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		panic(err)
	}

	loggerFactory := logging.NewDefaultLoggerFactory()
	loggerFactory.Writer = os.Stdout
	loggerFactory.DefaultLogLevel = logging.LogLevelDebug

	logger := loggerFactory.NewLogger("mux-test")

	newAddrChan := make(chan candidateAddr, 1)
	mux := NewUDPMuxNewAddr(ice.UDPMuxParams{UDPConn: serverConn, Logger: logger}, newAddrChan)
	return mux, newAddrChan
}

func setupCert() *webrtc.Certificate {
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	cert, err := webrtc.GenerateCertificate(pk)
	if err != nil {
		panic(err)
	}
	return cert
}

func fingerprintFromCert(certs []webrtc.Certificate) string {
	c, err := certs[0].GetFingerprints()
	if err != nil {
		panic(err)
	}
	return c[0].Value
}

func setupListener(mux *UDPMuxNewAddr, newAddrChan chan candidateAddr) (*listener, string, peer.ID) {
	privKey, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		panic(err)
	}

	cert := setupCert()
	config := webrtc.Configuration{
		Certificates: []webrtc.Certificate{*cert},
	}

	peerID, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		panic(err)
	}

	fp := strings.ReplaceAll(fingerprintFromCert(config.Certificates), ":", "")

	settingEngine := webrtc.SettingEngine{}
	settingEngine.DetachDataChannels()
	settingEngine.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
	settingEngine.SetICEUDPMux(mux)
	settingEngine.SetLite(true)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	return &listener{
		api:         api,
		config:      config,
		mux:         mux,
		newAddrChan: newAddrChan,
		idKey:       privKey,
	}, fp, peerID

}

func testDial(
	t *testing.T,
	mux *UDPMuxNewAddr,
	serverFp string,
	serverID peer.ID,
	handshakeFn func(*testing.T, sec.SecureConn, string) error,
) {
	// NOTE: Pion does not allow changing SDP once generated using
	// create offer, but chromium does allow this
	cert := setupCert()
	pcConfig := webrtc.Configuration{Certificates: []webrtc.Certificate{*cert}}
	localFp := fingerprintFromCert(pcConfig.Certificates)
	localFp = strings.ReplaceAll(localFp, ":", "")
	// setting engine
	se := webrtc.SettingEngine{}
	se.SetICECredentials(localFp, localFp)
	se.DetachDataChannels()
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(pcConfig)
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		t.Logf("ice conn state: %v", state)
	})
	if err != nil {
		panic(err)
	}

	dc, err := pc.CreateDataChannel("data", nil)
	if err != nil {
		panic(err)
	}

	opened := make(chan *DataChannel, 1)
	dc.OnOpen(func() {
		t.Log("datachannel")
		detached, err := dc.Detach()
		if err != nil {
			panic(err)
		}
		opened <- newDataChannel(detached)
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// t.Logf("%v", offer)
	err = pc.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}
	serverAddr, ok := mux.params.UDPConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		panic("could not get server address")
	}
	answerSdpString := renderServerSdp(sdpArgs{
		Addr:        serverAddr,
		Fingerprint: maFingerprintToSdp(serverFp),
		Ufrag:       localFp,
		Password:    serverFp,
	})

	answer := webrtc.SessionDescription{SDP: answerSdpString, Type: webrtc.SDPTypeAnswer}
	err = pc.SetRemoteDescription(answer)
	if err != nil {
		panic(err)
	}
	dataChannel := <-opened
	if dataChannel == nil {
		panic("data channel must not be nil")
	}
	t.Logf("opened data channel")
	privKey, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		panic(err)
	}
	noiseTpt, err := noise.New(privKey)
	if err != nil {
		panic(err)
	}
	secureConn, err := noiseTpt.SecureOutbound(context.Background(), dataChannel, serverID)
	if err != nil {
		panic(err)
	}

	err = handshakeFn(t, secureConn, localFp)
	if err != nil {
		panic(err)
	}
}

func TestAcceptInner(t *testing.T) {
	// setup mux
	mux, addrChan := setupMux()

	// setup listener
	// listener, lFp := setupListener(mux, addrChan)
	listener, serverFp, serverID := setupListener(mux, addrChan)

	done := make(chan struct{}, 1)

	go func() {
		addr := <-addrChan
		_, _, err := listener.accept(addr)
		if err != nil {
			panic(err)
		}
		done <- struct{}{}
	}()

	handshake := func(te *testing.T, conn sec.SecureConn, clientFp string) error {
		_, err := conn.Write([]byte(maFingerprintToSdp(clientFp)))
		if err != nil {
			return err
		}
		b := make([]byte, 300)
		n, err := conn.Read(b)
		remoteFp := string(b[:n])
		if remoteFp != maFingerprintToSdp(serverFp) {
			return fmt.Errorf("bad fingerprint")
		}
		return nil
	}
	testDial(t, mux, serverFp, serverID, handshake)
	<-done
}

func TestAcceptInnerBadFingerprint(t *testing.T) {
	// setup mux
	mux, addrChan := setupMux()

	// setup listener
	listener, serverFp, serverID := setupListener(mux, addrChan)

	done := make(chan struct{}, 1)

	go func() {
		addr := <-addrChan
		_, _, err := listener.accept(addr)
		t.Log(err)
		if err == nil {
			panic("should error with bad fingerprint")
		}
		done <- struct{}{}
	}()

	handshake := func(te *testing.T, conn sec.SecureConn, clientFp string) error {
		_, err := conn.Write([]byte("bad-fp"))
		if err != nil {
			return err
		}
		b := make([]byte, 300)
		n, err := conn.Read(b)
		remoteFp := string(b[:n])
		if remoteFp != maFingerprintToSdp(serverFp) {
			return fmt.Errorf("bad fingerprint")
		}
		return nil
	}
	testDial(t, mux, serverFp, serverID, handshake)
	<-done
}
