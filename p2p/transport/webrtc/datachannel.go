package libp2pwebrtc

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"

	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p-core/network"
	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
	protoio "github.com/libp2p/go-msgio/protoio"
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

const defaultReadBufferLen = 8192

// Package pion detached data channel into a net.Conn
// and then a network.MuxedStream
type dataChannel struct {
	// TODO: Are these circular references okay?
	dc            datachannel.ReadWriteCloser
	channel       *webrtc.DataChannel
	laddr         net.Addr
	raddr         net.Addr
	closeRead     chan struct{}
	closeWrite    chan struct{}
	readDeadline  *deadline
	writeDeadline *deadline
	readChan      chan *iorequest
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	m             sync.Mutex
	readBuf       bytes.Buffer
	wc            atomic.Bool
}

func newDataChannel(
	dc datachannel.ReadWriteCloser,
	channel *webrtc.DataChannel,
	pc *webrtc.PeerConnection,
	laddr, raddr net.Addr) *dataChannel {
	ctx, cancel := context.WithCancel(context.Background())

	result := &dataChannel{
		dc:            dc,
		channel:       channel,
		laddr:         laddr,
		raddr:         raddr,
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
		ctx:           ctx,
		cancel:        cancel,
		readChan:      make(chan *iorequest, 20),
		closeRead:     make(chan struct{}),
		closeWrite:    make(chan struct{}),
	}

	channel.OnClose(func() {
		log.Errorf("remote closed stream")
		// result.Close()
	})

	// channel.OnBufferedAmountLow(func() {
	// 	log.Infof("[%s] buffered amount low: amount: %d, threshold: %d", channel.Label(), channel.BufferedAmount(), channel.BufferedAmountLowThreshold())
	// })

	// result.wg.Add(1)
	// go result.readLoop()

	return result
}

// func (d *dataChannel) readLoop() {
// 	defer d.wg.Done()
// 	rdr := protoio.NewDelimitedReader(d.dc, defaultReadBufferLen)
// 	for {
// 		select {
// 		case <-d.ctx.Done():
// 			return
// 		case request := <-d.readChan:
// 			// bad request or readChan is closed
// 			if request == nil {
// 				return
// 			}
// 			// test for cancelled read
// 			select {
// 			case <-request.done:
// 				continue
// 			default:
// 			}

// 			var msg pb.Message
// 			err := rdr.ReadMsg(&msg)
// 			if err != nil && err != io.EOF {
// 				log.Errorf("%v", err)
// 				request.done <- ioresponse{0, err}
// 				continue
// 			}
// 			log.Warnf("flag: %v", msg.Flag)
// 			if msg.Flag != nil {
// 				switch msg.GetFlag() {
// 				case pb.Message_CLOSE_WRITE:
// 					log.Warnf("remote closed write")
// 					d.dc.Close()
// 				case pb.Message_CLOSE_READ:
// 				case pb.Message_RESET:
// 					log.Errorf("remote reset the connection")
// 				}
// 			}
// 			if err == io.EOF {
// 				select {
// 				case <-request.done:
// 					continue
// 				default:
// 				}
// 				request.done <- ioresponse {0, err}
// 				continue
// 			}
// 			if msg.Flag == nil && msg.GetMessage() == nil {
// 				panic("what??")
// 			}

// 			select {
// 			case <-d.closeRead:
// 				request.done <- ioresponse {0, io.EOF}
// 				continue
// 			// the read has been cancelled, so discard
// 			case <-request.done:
// 				continue
// 			default:
// 			}
// 			rcv := msg.Message
// 			if rcv == nil {
// 				rcv = []byte{}
// 			}
// 			copied := copy(request.chunk, rcv)
// 			request.done <- ioresponse{copied, nil}
// 		}
// 	}
// }

func (d *dataChannel) Read(b []byte) (int, error) {
	// select {
	// case <-d.ctx.Done():
	// 	return 0, os.ErrClosed
	// case <-d.closeRead:
	// 	return 0, io.EOF
	// // case where deadline is in the past
	// case <-d.readDeadline.wait():
	// 	return 0, os.ErrDeadlineExceeded
	// default:
	// }

	// 	done := make(chan ioresponse)

	// 	// try to queue the read
	// 	select {
	// 	case d.readChan <- &iorequest{chunk: b, done: done}:
	// 	case <-d.ctx.Done():
	// 		close(done)
	// 		return 0, os.ErrClosed
	// 	case <-d.readDeadline.wait():
	// 		close(done)
	// 		return 0, os.ErrDeadlineExceeded
	// 	}

	// 	// wait for read completion and cancel the read
	// 	// request if it times out or the connection is
	// 	// closed
	// 	select {
	// 	case <-d.ctx.Done():
	// 		close(done)
	// 		return 0, os.ErrClosed
	// 	case <-d.readDeadline.wait():
	// 		close(done)
	// 		return 0, os.ErrDeadlineExceeded
	// 	case result := <-done:
	// 		// log.Infof("read result: %d %v", result.int, result.error)
	// 		// log.Debugf("read: %s" ,string(b[:result.int]))
	// 		return result.int, result.error
	// 	}

	// method 2
	initialRead, ierr := d.readBuf.Read(b)
	if ierr == io.EOF && d.wc.Load() {
		return initialRead, io.EOF
	}
	if initialRead > 0 {
		return initialRead, nil
	}
	var msg pb.Message
	rdr := protoio.NewDelimitedReader(d.dc, 1500)
	err := rdr.ReadMsg(&msg)
	if err != nil {
		return initialRead, err
	}
	copied := copy(b[initialRead:], msg.GetMessage())
	d.readBuf.Write(msg.GetMessage()[copied:])
	if msg.Flag != nil && msg.GetFlag() == pb.Message_CLOSE_WRITE {
		d.wc.Store(true)
		return copied, nil
	}
	return copied, err
}

// func (d *dataChannel) Read(b []byte) (int, error) {
// 	for {
// 		d.bufm.Lock()
// 		n, err := d.readBuffer.Read(b)
// 		// log.Infof("reading from buffer: %d %v", n, err)
// 		d.bufm.Unlock()
// 		if err == io.EOF && !d.isReadClosed() {
// 			err = nil
// 		}
// 		if n > 0 {
// 			log.Infof("received %d bytes: %s", n, string(b))
// 			return n, err
// 		}
// 		if n == 0 {
// 			select {
// 			case <-d.ctx.Done():
// 				return 0, io.EOF
// 			case <-d.closeRead:
// 				return n, io.EOF
// 			case <-d.readDeadline.wait():
// 				return 0, os.ErrDeadlineExceeded
// 			case <-d.msgChan:

// 			}
// 		}
// 	}
// }

func (d *dataChannel) Write(b []byte) (int, error) {
	select {
	case <-d.ctx.Done():
		return 0, os.ErrClosed
	case <-d.writeDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	case <-d.closeWrite:
		return 0, os.ErrClosed
	default:
	}

	// done := make(chan ioresponse)

	// go func() {
	// 	wr := protoio.NewDelimitedWriter(d.dc)
	// 	err := wr.WriteMsg(
	// 		&pb.Message{
	// 			Message: b,
	// 		})
	// 	if err != nil {
	// 		log.Warnf("write error: %v", err)
	// 	}
	// 	done <- struct {
	// 		int
	// 		error
	// 	}{len(b), err}
	// }()

	// select {
	// case <-d.ctx.Done():
	// 	return 0, os.ErrClosed
	// case <-d.writeDeadline.wait():
	// 	return 0, os.ErrDeadlineExceeded
	// case result := <-done:
	// 	fmt.Printf("[%s] wrote: %s\n", d.uuid, string(b))
	// 	return result.int, result.error

	// }

	// fmt.Printf("[%s][%s] writing: %s\n", d.uuid, d.channel.Label(), string(b))
	wr := protoio.NewDelimitedWriter(d.dc)
	_ = wr.WriteMsg(
		&pb.Message{
			Message: b,
		})
	// fmt.Printf("[%s][%s] wrote: %s\n", d.uuid, d.channel.Label(), string(b))
	return len(b), nil
}

func (d *dataChannel) Close() error {
	select {
	case <-d.ctx.Done():
		return nil
	default:
	}
	err := d.channel.Close()
	d.CloseWrite()
	d.cancel()
	// d.wg.Wait()
	return err
}

func (d *dataChannel) CloseRead() error {
	select {
	case <-d.ctx.Done():
		return nil
	case <-d.closeRead:
		return nil
	default:
		close(d.closeRead)
	}

	return nil
}

func (d *dataChannel) CloseWrite() error {
	select {
	case <-d.ctx.Done():
		return nil
	case <-d.closeWrite:
		return nil
	default:
		close(d.closeWrite)
	}
	wr := protoio.NewDelimitedWriter(d.dc)
	err := wr.WriteMsg(&pb.Message{
		Flag: pb.Message_CLOSE_WRITE.Enum(),
	})
	return err
}

func (d *dataChannel) LocalAddr() net.Addr {
	return d.laddr
}

func (d *dataChannel) RemoteAddr() net.Addr {
	return d.raddr
}

func (d *dataChannel) Reset() error {
	log.Errorf("stream reset")
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

func (d *dataChannel) isReadClosed() bool {
	select {
	case <-d.closeRead:
		return true
	default:
	}
	return false
}
