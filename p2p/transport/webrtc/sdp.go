package libp2pwebrtc

import (
	"fmt"
	"net"

	"github.com/multiformats/go-multihash"
)

type sdpArgs struct {
	Addr        *net.UDPAddr
	Ufrag       string
	Fingerprint *multihash.DecodedMultihash
}

const clientSDP string = `
v=0
o=- 0 0 IN %s %s
s=-
c=IN %s %s
t=0 0
m=application %d UDP/DTLS/SCTP webrtc-datachannel
a=mid:0
a=ice-options:trickle
a=ice-ufrag:%s
a=ice-pwd:%s
a=fingerprint:%s
a=setup:actpass
a=sctp-port:5000
a=max-message-size:100000
`

func renderClientSdp(args sdpArgs) string {
	ipVersion := "IP4"
	if args.Addr.IP.To4() == nil {
		ipVersion = "IP6"
	}
	return fmt.Sprintf(
		clientSDP,
		ipVersion,
		args.Addr.IP,
		ipVersion,
		args.Addr.IP,
		args.Addr.Port,
		args.Ufrag,
		args.Ufrag,
		fingerprintToSDP(args.Fingerprint),
	)
}

const serverSDP string = `
v=0
o=- 0 0 IN %s %s
s=-
t=0 0
a=ice-lite
m=application %d UDP/DTLS/SCTP webrtc-datachannel
c=IN %s %s
a=mid:0
a=ice-options:ice2
a=ice-ufrag:%s
a=ice-pwd:%s
a=fingerprint:%s
a=setup:passive
a=sctp-port:5000
a=max-message-size:100000
a=candidate:1 1 UDP 1 %s %d typ host
`

func renderServerSdp(args sdpArgs) string {
	ipVersion := "IP4"
	if args.Addr.IP.To4() == nil {
		ipVersion = "IP6"
	}
	fp := fingerprintToSDP(args.Fingerprint)
	return fmt.Sprintf(
		serverSDP,
		ipVersion,
		args.Addr.IP,
		args.Addr.Port,
		ipVersion,
		args.Addr.IP,
		args.Ufrag,
		args.Ufrag,
		fp,
		args.Addr.IP,
		args.Addr.Port,
	)
}
