package libp2pwebrtc

import (
	"io"
	"net"
	"time"

	"github.com/pion/datachannel"
)

// Package pion detached data channel into a net.Conn
// and then a network.MuxedStream
type DataChannel struct {
	dc    datachannel.ReadWriteCloser
	readClosed, writeClosed chan struct{}
}

func newDataChannel(dc datachannel.ReadWriteCloser) *DataChannel {
	return &DataChannel {
		dc: dc,
		readClosed: make(chan struct{}),
		writeClosed: make(chan struct{}),
	}
}

func (d *DataChannel) Read(b []byte) (int, error) {
	select {
	case <-d.readClosed:
		return 0, io.EOF
	default:
	}
	return d.dc.Read(b)
}

func (d *DataChannel) Write(b []byte) (int, error) {
	select {
	case <-d.writeClosed:
		return 0, io.ErrClosedPipe
	default:
	}
	return d.dc.Write(b)
}

func (d *DataChannel) Close() error {
	return d.dc.Close()
}

func (d *DataChannel) LocalAddr() net.Addr {
	return nil
}

func (d *DataChannel) RemoteAddr() net.Addr {
	return nil
}

func (d *DataChannel) SetDeadline(t time.Time) error {
	return nil
}

func (d *DataChannel) SetReadDeadline(t time.Time) error {
	return nil
}

func (d *DataChannel) SetWriteDeadline(t time.Time) error {
	return nil
}

func (d *DataChannel) CloseRead() error {
	select {
	case <-d.readClosed:
	default:
		close(d.readClosed)
	}
	return nil
}

func (d *DataChannel) CloseWrite() error {
	select {
	case <-d.writeClosed:
	default:
		close(d.writeClosed)
	}
	return nil
}
