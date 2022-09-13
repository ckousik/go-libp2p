package libp2pwebrtc

import (
	"fmt"
)

type errKind string

const (
	errKindConnectionFailed errKind = "peerconnection failed"
	errKindDatachannel      errKind = "datachannel"
	errKindMultiaddr        errKind = "bad multiaddr"
	errKindNoise            errKind = "noise"
	errKindInternal         errKind = "internal"
)

var (
	errDataChannelTimeout = errDatachannel("timed out waiting for datachannel", nil)
)

type webRTCTransportError struct {
	kind    errKind
	message string
	nested  error
}

func (e *webRTCTransportError) Error() string {
	return fmt.Sprintf("[webrtc-transport-error] %s : %s : %v", e.kind, e.message, e.nested)
}

func errConnectionFailed(msg string, err error) error {
	return &webRTCTransportError{kind: errKindConnectionFailed, message: msg, nested: err}
}

func errDatachannel(msg string, err error) error {
	return &webRTCTransportError{kind: errKindDatachannel, message: msg, nested: err}
}

func errMultiaddr(msg string, err error) error {
	return &webRTCTransportError{kind: errKindMultiaddr, message: msg, nested: err}
}

func errNoise(msg string, err error) error {
	return &webRTCTransportError{kind: errKindNoise, message: msg, nested: err}
}

func errInternal(msg string, err error) error {
	return &webRTCTransportError{kind: errKindInternal, message: msg, nested: err}
}
