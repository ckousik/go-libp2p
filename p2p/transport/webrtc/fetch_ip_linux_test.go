//go:build linux
// +build linux

package libp2pwebrtc

import "net"

func getListenerAndDialerIP() (net.IP, net.IP) {
	return net.IPv4(0, 0, 0, 0), net.IPv4(127, 0, 0, 1)
}
