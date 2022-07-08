package libp2pwebrtc

import (
	"github.com/pion/webrtc/v3"
)

type transport struct {
	api *webrtc.API
}

func (t *transport) Protocols() []int {
	return []int{ P_XWEBRTC }
}

func (t *transport) Proxy() bool {
	return false
}
