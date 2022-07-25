package libp2pwebrtc

import (
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/network"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
)

var _ network.MuxedStream = &dataChannel{}

// Package pion detached data channel into a net.Conn
// and then a network.MuxedStream
type dataChannel struct {
	// TODO: Are these circular references okay?
	pc            *webrtc.PeerConnection
	dc            datachannel.ReadWriteCloser
	laddr         net.Addr
	raddr         net.Addr
	closeRead     chan struct{}
	closeWrite    chan struct{}
	readDeadline  time.Time
	writeDeadline time.Time
	m             sync.Mutex
}

func newDataChannel(
	dc datachannel.ReadWriteCloser,
	pc *webrtc.PeerConnection,
	laddr, raddr net.Addr) *dataChannel {
	return &dataChannel{
		dc:         dc,
		laddr:      laddr,
		raddr:      raddr,
		closeRead:  make(chan struct{}, 1),
		closeWrite: make(chan struct{}, 1),
	}
}

func (d *dataChannel) Read(b []byte) (int, error) {
	select {
	case <-d.closeRead:
		return 0, io.EOF
	default:
	}
	d.m.Lock()
	readDeadline := d.readDeadline
	d.m.Unlock()

	if readDeadline.IsZero() {
		return d.dc.Read(b)
	} else if readDeadline.Before(time.Now()) {
		return 0, os.ErrDeadlineExceeded
	}

	done := make(chan struct {
		int
		error
	}, 1)
	go func() {
		n, err := d.dc.Read(b)
		log.Debug(string(b[:n]))
		done <- struct {
			int
			error
		}{n, err}
	}()

	select {
	case <-time.After(readDeadline.Sub(time.Now())):
		return 0, os.ErrDeadlineExceeded
	case result := <-done:
		return result.int, result.error
	}
}

func (d *dataChannel) Write(b []byte) (int, error) {
	select {
	case <-d.closeWrite:
		return 0, io.ErrClosedPipe
	default:
	}
	d.m.Lock()
	writeDeadline := d.writeDeadline
	d.m.Unlock()

	if writeDeadline.IsZero() {
		return d.dc.Write(b)
	} else if writeDeadline.Before(time.Now()) {
		return 0, os.ErrDeadlineExceeded
	}

	done := make(chan struct {
		int
		error
	}, 1)
	go func() {
		n, err := d.dc.Write(b)
		done <- struct {
			int
			error
		}{n, err}
	}()

	select {
	case <-time.After(writeDeadline.Sub(time.Now())):
		return 0, os.ErrDeadlineExceeded
	case result := <-done:
		return result.int, result.error

	}
}

func (d *dataChannel) Close() error {
	return d.dc.Close()
}

func (d *dataChannel) CloseRead() error {
	select {
	case <-d.closeRead:
	default:
		close(d.closeRead)
	}
	return nil
}

func (d *dataChannel) CloseWrite() error {
	select {
	case <-d.closeWrite:
	default:
		close(d.closeWrite)
	}
	return nil
}

func (d *dataChannel) LocalAddr() net.Addr {
	return d.laddr
}

func (d *dataChannel) RemoteAddr() net.Addr {
	return d.raddr
}

func (d *dataChannel) Reset() error {
	return d.Close()
}

func (d *dataChannel) SetDeadline(t time.Time) error {
	d.m.Lock()
	defer d.m.Unlock()
	d.readDeadline = t
	d.writeDeadline = t
	return nil
}

func (d *dataChannel) SetReadDeadline(t time.Time) error {
	d.m.Lock()
	defer d.m.Unlock()
	d.readDeadline = t
	return nil
}

func (d *dataChannel) SetWriteDeadline(t time.Time) error {
	d.m.Lock()
	defer d.m.Unlock()
	d.writeDeadline = t
	return nil
}
