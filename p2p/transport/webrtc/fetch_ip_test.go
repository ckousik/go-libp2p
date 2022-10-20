//go:build !linux
// +build !linux

package libp2pwebrtc

import "net"

// non-linux builds need to bind to a non-loopback interface
// to accept incoming connections. 0.0.0.0 does not work since
// Pion will bind to a local interface which is not loopback
// and there may not be a route from, say 192.168.0.0/16 to 0.0.0.0.

func getListenerAndDialerIP() (listenerIp net.IP, dialerIp net.IP) {
	listenerIp = net.IPv4(0, 0, 0, 0)
	dialerIp = net.IPv4(0, 0, 0, 0)
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		log.Debugf("checking interface: %s", iface.Name)
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.IsPrivate() {
				if ipnet.IP.To4() != nil {
					listenerIp = ipnet.IP.To4()
					dialerIp = listenerIp
					return
				}
			}
		}
	}
	return
}
