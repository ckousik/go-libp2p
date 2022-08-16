//go:build linux
// +build linux

package libp2pwebrtc

import "net"

var listenerIp = net.IPv4(0, 0, 0, 0)
var dialerIp = net.IPv4(127, 0, 0, 1)
