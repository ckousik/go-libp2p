package libp2pwebrtc

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaFingerprintToSdp(t *testing.T) {
	certhash := "496612170D1C91AE574CC636DDD597D27D62C99A7FB9A3F47003E7439173235E"
	expected := "49:66:12:17:0D:1C:91:AE:57:4C:C6:36:DD:D5:97:D2:7D:62:C9:9A:7F:B9:A3:F4:70:03:E7:43:91:73:23:5E"
	result := maFingerprintToSdp(certhash)
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
a=max-message-size:100000
a=candidate:1 1 UDP 1 0.0.0.0 37826 typ host
`

func TestRenderServerSDP(t *testing.T) {
	args := sdpArgs{
		Addr:        &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826},
		Ufrag:       "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581",
		Fingerprint: defaultMultihash,
	}

	sdp := renderServerSdp(args)
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
a=max-message-size:100000
`

func TestRenderClientSDP(t *testing.T) {
	args := sdpArgs{
		Addr:        &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 37826},
		Ufrag:       "d2c0fc07-8bb3-42ae-bae2-a6fce8a0b581",
		Fingerprint: defaultMultihash,
	}
	sdp := renderClientSdp(args)
	require.Equal(t, expectedClientSDP, sdp)
}
