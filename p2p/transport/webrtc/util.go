package libp2pwebrtc

import (
	"encoding/hex"
	"strings"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multibase"
	mh "github.com/multiformats/go-multihash"
	"github.com/pion/webrtc/v3"
)

func mhToSdpHash(mh string) string {
	switch mh {
	case "sha1":
		return "sha1"
	case "sha2-256":
		return "sha-256"
	case "md5":
		return "md5"
	default:
		return ""
	}
}

func sdpHashToMh(sdpHash string) string {
	switch sdpHash {
	case "sha-256":
		return "sha2-256"
	case "sha1":
		return "sha1"
	case "md5":
		return "md5"
	default:
		return ""
	}
}

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
	return result
}

func fingerprintToSDP(fp *mh.DecodedMultihash) string {
	if fp == nil {
		return ""
	}
	fpDigest := maFingerprintToSdp(hex.EncodeToString(fp.Digest))
	fpAlgo := mhToSdpHash(strings.ToLower(fp.Name))
	if fpAlgo == "" {
		fpAlgo = strings.ToLower(fp.Name)
	}
	return fpAlgo + " " + fpDigest
}

func decodeRemoteFingerprint(maddr ma.Multiaddr) (*mh.DecodedMultihash, error) {
	remoteFingerprintMultibase, err := maddr.ValueForProtocol(ma.P_CERTHASH)
	if err != nil {
		return nil, err
	}
	_, data, err := multibase.Decode(remoteFingerprintMultibase)
	if err != nil {
		return nil, err
	}
	return mh.Decode(data)
}

func encodeDTLSFingerprint(fp webrtc.DTLSFingerprint) (string, error) {
	digest, err := hex.DecodeString(strings.ReplaceAll(fp.Value, ":", ""))
	if err != nil {
		return "", err
	}
	algo := sdpHashToMh(strings.ToLower(fp.Algorithm))
	if algo == "" {
		algo = fp.Algorithm
	}
	encoded, err := mh.EncodeName(digest, algo)
	if err != nil {
		return "", err
	}
	return multibase.Encode(multibase.Base16, encoded)
}
