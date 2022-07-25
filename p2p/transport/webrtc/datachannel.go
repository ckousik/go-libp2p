package libp2pwebrtc

import (
	"bytes"
	"context"
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

type ioresponse struct {
	int
	error
}

type iorequest struct {
	chunk []byte
	done  chan ioresponse
}

const defaultReadBufferLen = 3000

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
	readDeadline  *deadline
	writeDeadline *deadline
	readBuffer    *bytes.Buffer
	readChan      chan *iorequest
	writeChan     chan *iorequest
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func newDataChannel(
	dc datachannel.ReadWriteCloser,
	pc *webrtc.PeerConnection,
	laddr, raddr net.Addr) *dataChannel {
	ctx, cancel := context.WithCancel(context.Background())

	var b bytes.Buffer
	b.Grow(defaultReadBufferLen)

	result := &dataChannel{
		dc:            dc,
		laddr:         laddr,
		raddr:         raddr,
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
		ctx:           ctx,
		cancel:        cancel,
		readBuffer:    &b,
		readChan:      make(chan *iorequest, 20),
		closeRead:     make(chan struct{}),
		closeWrite:    make(chan struct{}),
	}

	result.wg.Add(1)
	go result.readLoop()

	return result
}

func (d *dataChannel) readLoop() {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.closeRead:
			return
		case request := <-d.readChan:
			// bad request or readChan is closed
			if request == nil {
				return
			}
			// test for cancelled read
			select {
			case <-request.done:
				continue
			default:
			}

			// if we have data from a previous successful read, return that data
			if n, err := d.readBuffer.Read(request.chunk); err != io.EOF {
				request.done <- ioresponse{n, nil}
				continue
			}
			// read from the datachannel
			n, err := d.dc.Read(request.chunk)

			select {
			// the read has been cancelled, so write the data
			// to the buffer instead
			case <-request.done:
				d.readBuffer.Write(request.chunk)
				continue
			default:
			}
			request.done <- ioresponse{n, err}
		}
	}
}

func (d *dataChannel) Read(b []byte) (int, error) {
	select {
	case <-d.ctx.Done():
		return 0, os.ErrClosed
	case <-d.closeRead:
		return 0, io.EOF
	// case where deadline is in the past
	case <-d.readDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	default:
	}

	done := make(chan ioresponse)

	// try to queue the read
	select {
	case d.readChan <- &iorequest{chunk: b, done: done}:
	case <-d.ctx.Done():
		close(done)
		return 0, os.ErrClosed
	case <-d.readDeadline.wait():
		close(done)
		return 0, os.ErrDeadlineExceeded
	}

	// wait for read completion and cancel the read
	// request if it times out or the connection is
	// closed
	select {
	case <-d.ctx.Done():
		close(done)
		return 0, os.ErrClosed
	case <-d.readDeadline.wait():
		close(done)
		return 0, os.ErrDeadlineExceeded
	case result := <-done:
		return result.int, result.error
	}
}

func (d *dataChannel) Write(b []byte) (int, error) {
	select {
	case <-d.ctx.Done():
		return 0, os.ErrClosed
	case <-d.closeWrite:
		return 0, os.ErrClosed
	default:
	}

	done := make(chan ioresponse)

	go func() {
		n, err := d.dc.Write(b)
		done <- struct {
			int
			error
		}{n, err}
	}()

	select {
	case <-d.ctx.Done():
		return 0, os.ErrClosed
	case <-d.writeDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	case result := <-done:
		return result.int, result.error

	}
}

func (d *dataChannel) Close() error {
	d.cancel()
	_ = d.CloseRead()
	_ = d.CloseWrite()
	err := d.dc.Close()
	d.wg.Wait()
	return err
}

func (d *dataChannel) CloseRead() error {
	select {
	case <-d.ctx.Done():
	case <-d.closeRead:
	default:
		close(d.closeRead)
	}
	return nil
}

func (d *dataChannel) CloseWrite() error {
	select {
	case <-d.ctx.Done():
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
	d.SetReadDeadline(t)
	d.SetWriteDeadline(t)
	return nil
}

func (d *dataChannel) SetReadDeadline(t time.Time) error {
	d.readDeadline.set(t)
	return nil
}

func (d *dataChannel) SetWriteDeadline(t time.Time) error {
	d.writeDeadline.set(t)
	return nil
}
