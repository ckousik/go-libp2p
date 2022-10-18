package libp2pwebrtc

import (
	"encoding/hex"
	"strings"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multibase"
	mh "github.com/multiformats/go-multihash"
	"github.com/pion/webrtc/v3"
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
	return result
}

func fingerprintToSDP(fp *mh.DecodedMultihash) string {
	if fp == nil {
		return ""
	}
	fpDigest := maFingerprintToSdp(hex.EncodeToString(fp.Digest))
	return "sha-256 " + fpDigest
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
	encoded, err := mh.Encode(digest, mh.SHA2_256)
	if err != nil {
		return "", err
	}
	return multibase.Encode(multibase.Base58BTC, encoded)
}
