package noise

import (
	"context"
	"net"

	"github.com/libp2p/go-libp2p/core/canonicallog"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/sec"

	manet "github.com/multiformats/go-multiaddr/net"
)

// ID is the protocol ID for noise
const ID = "/noise"

var _ sec.SecureTransport = &Transport{}

// Transport implements the interface sec.SecureTransport
// https://godoc.org/github.com/libp2p/go-libp2p/core/sec#SecureConn
type Transport struct {
	localID    peer.ID
	privateKey crypto.PrivKey
}

// New creates a new Noise transport using the given private key as its
// libp2p identity key.
func New(privkey crypto.PrivKey) (*Transport, error) {
	localID, err := peer.IDFromPrivateKey(privkey)
	if err != nil {
		return nil, err
	}

	return &Transport{
		localID:    localID,
		privateKey: privkey,
	}, nil
}

// SecureInbound runs the Noise handshake as the responder.
// if p is empty accept any peer ID.
func (t *Transport) SecureInbound(ctx context.Context, insecure net.Conn, p peer.ID) (sec.SecureConn, error) {
	c, err := newSecureSession(t, ctx, insecure, p, nil, nil, nil, false, p != "")
	if err != nil {
		addr, maErr := manet.FromNetAddr(insecure.RemoteAddr())
		if maErr == nil {
			canonicallog.LogPeerStatus(100, p, addr, "handshake_failure", "noise", "err", err.Error())
		}
	}
	return c, err
}

// SecureOutbound runs the Noise handshake as the initiator.
func (t *Transport) SecureOutbound(ctx context.Context, insecure net.Conn, p peer.ID) (sec.SecureConn, error) {
	return newSecureSession(t, ctx, insecure, p, nil, nil, nil, true, true)
}

// SecureOutboundForAnyPeerID runs the Noise handshake as the initiator but does not check
// the remote's peer ID. This is the outbound equivalent of calling `SecureInbound` with an empty
// peer ID.
func (t *Transport) SecureOutboundForAnyPeerID(ctx context.Context, insecure net.Conn) (sec.SecureConn, error) {
	return newSecureSession(t, ctx, insecure, "", nil, nil, nil, true, false)
}

func (t *Transport) WithSessionOptions(opts ...SessionOption) (*SessionTransport, error) {
	st := &SessionTransport{t: t}
	for _, opt := range opts {
		if err := opt(st); err != nil {
			return nil, err
		}
	}
	return st, nil
}
