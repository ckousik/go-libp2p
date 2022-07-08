// Temporary until this this protocol can be merged into go-multiaddr
package libp2pwebrtc

import (
	"encoding/hex"
	"fmt"
	ma "github.com/multiformats/go-multiaddr"
)

const P_XWEBRTC = 0x115

func xwebrtcVal(b []byte) error {
	if len(b) != 32 {
		return fmt.Errorf("fingerprint should be 32 bytes, found: %d", len(b))
	}
	return nil
}

func xwebrtcStB(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

func xwebrtcBtS(b []byte) (string, error) {
	return hex.EncodeToString(b), nil
}

var TranscoderXWebRTC = ma.NewTranscoderFromFunctions(xwebrtcStB, xwebrtcBtS, xwebrtcVal)

var protocol = ma.Protocol{
	Name:  "x-webrtc",
	Code:  P_XWEBRTC,
	VCode: ma.CodeToVarint(P_XWEBRTC),
	Size:  8 * 32,
	Transcoder: TranscoderXWebRTC,
}

func init() {
	err := ma.AddProtocol(protocol)
	if err != nil {
		panic(err)
	}
}
