package libp2pwebrtc

import (
	"testing"
	ma "github.com/multiformats/go-multiaddr"
)

func TestMaFingerprintToSdp(t *testing.T) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E" 
	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E" 
	result := maFingerprintToSdp(certhash)
	t.Logf("result: %s", result)
	if result != expected {
		t.Fatalf("expected %s, found: %s", expected, result)
	}
}

func TestMaToAddrFingerprint(t *testing.T) {
	maddr, err := ma.NewMultiaddr("/ip4/127.0.0.1/udp/44218/x-webrtc/496612170d1c91ae574cc636ddd597d27d62c99a7fb9a3f47003e7439173235e")
	if err != nil {
		t.Fatal(err)
	}
	addr, fp, err := maToAddrFingerprint(maddr)
	if err != nil {
		t.Fatal(err)
	}
	if addr.IP.String() != "127.0.0.1" {
		t.Fatalf("expected localhost ipv4 address")
	}

	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E" 
	if fp != expected {
		t.Fatalf("expected %s, found: %s", expected, fp)
	}

}
