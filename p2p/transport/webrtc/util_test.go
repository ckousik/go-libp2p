package libp2pwebrtc

import (
	"encoding/hex"
	"net"
	"strings"
	"testing"

	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

func TestMaFingerprintToSdp(t *testing.T) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	result := maFingerprintToSdp(certhash)
	require.Equal(t, expected, result)
}

func TestReplaceAll(t *testing.T) {
	fingerprint := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	expected := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	result := removeAll(fingerprint, byte(':'))
	require.Equal(t, expected, result)
}
func TestIntersperse2(t *testing.T) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	result := intersperse2(certhash, byte(':'), 2)
	require.Equal(t, expected, result)
}

const expectedServerSDP = `
v=0
o=- 0 0 IN IP4 0.0.0.0
s=-
t=0 0
a=ice-lite
m=application 37826 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
a=mid:0
a=ice-options:ice2
a=ice-ufrag:d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581
a=ice-pwd:d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581
a=fingerprint:sha-256 ba:78:16:bf:8f:01:cf:ea:41:41:40:de:5d:ae:22:23:b0:03:61:a3:96:17:7a:9c:b4:10:ff:61:f2:00:15:ad
a=setup:passive
a=sctp-port:5000
a=max-message-size:16384
a=candidate:1 1 UDP 1 0.0.0.0 37826 typ host
`

func TestRenderServerSDP(t *testing.T) {
	encoded, err := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	require.NoError(t, err)

	testMultihash := &multihash.DecodedMultihash{
		Code:   multihash.SHA2_256,
		Name:   multihash.Codes[multihash.SHA2_256],
		Digest: encoded,
		Length: len(encoded),
	}
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"
	fingerprint := testMultihash

	sdp := renderServerSdp(addr, ufrag, fingerprint)
	require.Equal(t, expectedServerSDP, sdp)
}

const expectedClientSDP = `
v=0
o=- 0 0 IN IP4 0.0.0.0
s=-
c=IN IP4 0.0.0.0
t=0 0
m=application 37826 UDP/DTLS/SCTP webrtc-datachannel
a=mid:0
a=ice-options:trickle
a=ice-ufrag:d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581
a=ice-pwd:d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581
a=fingerprint:sha-256 ba:78:16:bf:8f:01:cf:ea:41:41:40:de:5d:ae:22:23:b0:03:61:a3:96:17:7a:9c:b4:10:ff:61:f2:00:15:ad
a=setup:actpass
a=sctp-port:5000
a=max-message-size:16384
`

func TestRenderClientSDP(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"
	sdp := renderClientSdp(addr, ufrag)
	require.Equal(t, expectedClientSDP, sdp)
}

func TestRenderClientSDP2(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"
	sdp := renderClientSdp2(addr, ufrag)
	require.Equal(t, expectedClientSDP, sdp)
}

func BenchmarkMaFingerprintToSdpIntersperse(b *testing.B) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	for i := 0; i < b.N; i++ {
		maFingerprintToSdp(certhash)
	}
}

func BenchmarkIntersperseSDP(b *testing.B) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	for i := 0; i < b.N; i++ {
		intersperse(certhash, ':', 2)
	}
}

func BenchmarkIntersperse2(b *testing.B) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	for i := 0; i < b.N; i++ {
		intersperse2(certhash, byte(':'), 2)
	}
}

func BenchmarkStringsReplaceAll(b *testing.B) {
	fingerprint := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	for i := 0; i < b.N; i++ {
		strings.ReplaceAll(fingerprint, ":", "")
	}
}

func BenchmarkReplaceAll(b *testing.B) {
	fingerprint := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	for i := 0; i < b.N; i++ {
		removeAll(fingerprint, byte(':'))
	}
}

func BenchmarkRenderClientSDP(b *testing.B) {
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"

	for i := 0; i < b.N; i++ {
		renderClientSdp(addr, ufrag)
	}
}

func BenchmarkRenderClientSDPTemplate(b *testing.B) {
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"

	for i := 0; i < b.N; i++ {
		renderClientSdp2(addr, ufrag)
	}
}

func BenchmarkRenderServerSDP(b *testing.B) {
	encoded, _ := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")

	testMultihash := &multihash.DecodedMultihash{
		Code:   multihash.SHA2_256,
		Name:   multihash.Codes[multihash.SHA2_256],
		Digest: encoded,
		Length: len(encoded),
	}
	addr := &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826}
	ufrag := "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581"
	fingerprint := testMultihash

	for i := 0; i < b.N; i++ {
		renderServerSdp(addr, ufrag, fingerprint)
	}

}
