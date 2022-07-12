// Temporary until this this protocol can be merged into go-multiaddr
package libp2pwebrtc

import (
	ma "github.com/multiformats/go-multiaddr"
)

const P_XWEBRTC = 0x115

var protocol = ma.Protocol{
	Name:  "webrtc",
	Code:  P_XWEBRTC,
	VCode: ma.CodeToVarint(P_XWEBRTC),
}

func init() {
	err := ma.AddProtocol(protocol)
	if err != nil {
		panic(err)
	}
}
