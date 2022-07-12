package libp2pwebrtc

import (
	"encoding/hex"
	"strings"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/pion/webrtc/v3"

	"github.com/multiformats/go-multibase"
	mh "github.com/multiformats/go-multihash"
)

var mhToSdpHash = map[string]string{
	"sha1":     "sha1",
	"sha2-256": "sha-256",
	"md5":      "md5",
}

var sdpHashToMh = map[string]string{
	"sha-256": "sha2-256",
	"sha1":    "sha1",
	"md5":     "md5",
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

func fingerprintSDP(fp *mh.DecodedMultihash) string {
	if fp == nil {
		return ""
	}
	fpDigest := maFingerprintToSdp(hex.EncodeToString(fp.Digest))
	fpAlgo, ok := mhToSdpHash[strings.ToLower(fp.Name)]
	if !ok {
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
	algo, ok := sdpHashToMh[strings.ToLower(fp.Algorithm)]
	if !ok {
		algo = fp.Algorithm
	}
	encoded, err := mh.EncodeName(digest, algo)
	return multibase.Encode(multibase.Base16, encoded)
}
