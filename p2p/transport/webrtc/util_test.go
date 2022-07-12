package libp2pwebrtc

import (
	"testing"
)

func TestMaFingerprintToSdp(t *testing.T) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	result := maFingerprintToSdp(certhash)
	if result != expected {
		t.Fatalf("expected %s, found: %s", expected, result)
	}
}
