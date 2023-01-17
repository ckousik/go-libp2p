package udpmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	pool "github.com/libp2p/go-buffer-pool"
)

var _ net.PacketConn = &muxedConnection{}

const maxPacketsInQueue = 128

var (
	errTooManyPackets       = fmt.Errorf("too many packets in queue; dropping")
	errAlreadyClosed  error = errors.New("already closed")
)

// muxedConnection provides a net.PacketConn abstraction
// over packetQueue and adds the ability to store addresses
// from which this connection (indexed by ufrag) received
// data.
type muxedConnection struct {
	ctx    context.Context
	cancel context.CancelFunc
	pq     *packetQueue
	// list of remote addresses associated with this connection.
	// this is useful as a mapping from [address] -> ufrag
	addresses []string
	ufrag     string
	mux       *udpMux
}

func newMuxedConnection(mux *udpMux, ufrag string) *muxedConnection {
	ctx, cancel := context.WithCancel(context.Background())
	return &muxedConnection{
		ctx:    ctx,
		cancel: cancel,
		pq:     newPacketQueue(),
		ufrag:  ufrag,
		mux:    mux,
	}
}

func (conn *muxedConnection) push(buf []byte, addr net.Addr) error {
	return conn.pq.push(buf, addr)
}

// Close implements net.PacketConn
func (conn *muxedConnection) Close() error {
	_ = conn.closeConnection()
	conn.mux.RemoveConnByUfrag(conn.ufrag)
	return nil
}

// LocalAddr implements net.PacketConn
func (conn *muxedConnection) LocalAddr() net.Addr {
	return conn.mux.socket.LocalAddr()
}

// ReadFrom implements net.PacketConn
func (conn *muxedConnection) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return conn.pq.pop(conn.ctx, p)
}

// SetDeadline implements net.PacketConn
func (*muxedConnection) SetDeadline(t time.Time) error {
	return nil
}

// SetReadDeadline implements net.PacketConn
func (*muxedConnection) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline implements net.PacketConn
func (*muxedConnection) SetWriteDeadline(t time.Time) error {
	return nil
}

// WriteTo implements net.PacketConn
func (conn *muxedConnection) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return conn.mux.writeTo(p, addr)
}

func (conn *muxedConnection) closeConnection() error {
	select {
	case <-conn.ctx.Done():
		return errAlreadyClosed
	default:
	}
	conn.pq.close()
	conn.cancel()
	return nil
}

type packet struct {
	addr net.Addr
	buf  []byte
}

// just a convenience wrapper around a channel
type packetQueue struct {
	sync.Mutex
	pkts   chan packet
	ctx    context.Context
	cancel context.CancelFunc
}

func newPacketQueue() *packetQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &packetQueue{
		ctx:    ctx,
		cancel: cancel,
		pkts:   make(chan packet, maxPacketsInQueue),
	}
}

// pop reads a packet from the packetQueue or blocks until
// either a packet becomes available or the buffer is closed.
func (pq *packetQueue) pop(ctx context.Context, buf []byte) (int, net.Addr, error) {
	select {
	case p, ok := <-pq.pkts:
		if !ok {
			return 0, nil, io.EOF
		}
		n := copy(buf, p.buf)
		var err error
		if n < len(p.buf) {
			err = io.ErrShortBuffer
		}
		p.buf = p.buf[:cap(p.buf)]
		pool.Put(p.buf)
		return n, p.addr, err
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

// push adds a packet to the packetQueue
func (pq *packetQueue) push(buf []byte, addr net.Addr) error {
	// we acquire a lock when sending on the channel to prevent
	// closing when a send operation could be happening. This
	// is caused by send usually being triggered by a different
	// goroutine
	pq.Lock()
	defer pq.Unlock()
	// priority select channel closure over sending.
	// this prevents a send on closed channel panic
	select {
	case <-pq.ctx.Done():
		return io.ErrClosedPipe
	default:
		select {
		case pq.pkts <- packet{addr, buf}:
			return nil
		default:
			return errTooManyPackets
		}
	}
}

// discard all packets in the queue and return
// buffers
func (pq *packetQueue) close() {
	select {
	case <-pq.ctx.Done():
		return
	default:
	}
	pq.cancel()
	pq.Lock()
	defer pq.Unlock()
	close(pq.pkts)
}
