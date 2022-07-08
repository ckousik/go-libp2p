package libp2pwebrtc

import (
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"fmt"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

func maFingerprintToSdp(fp string) string {
	result := ""
	first := true
	for pos, char := range fp {
		if pos%2 == 0 {
			if first {
				first = false
			} else {
				result += ":"
			}
		}
		result += string(char)
	}
	return strings.ToUpper(result)
}

func maToAddrFingerprint(addr ma.Multiaddr) (*net.UDPAddr, string, error) {
	ip, err := manet.ToIP(addr)
	if err != nil {
		return nil, "", err
	}
	portS, err := addr.ValueForProtocol(ma.P_UDP)
	if err != nil {
		return nil, "", err
	}
	port, err := strconv.Atoi(portS)
	if err != nil {
		return nil, "", err
	}

	result := &net.UDPAddr{ IP: ip, Port: port }

	remoteFp, err := addr.ValueForProtocol(P_XWEBRTC)
	if err != nil {
		return nil, "", err
	}
	remoteFp = maFingerprintToSdp(remoteFp)

	return result, remoteFp, nil
}

func validateFingerprint(fp string) error {
	b, err := hex.DecodeString(fp)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("invalid fingerprint")
	}
	return nil
}
