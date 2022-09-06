package libp2pwebrtc

import (
	"bytes"
	"context"
	"io"
	"os"

	// "io"
	"net"

	"sync"
	"time"

	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pion/webrtc/v3"

	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
)

var _ network.MuxedStream = &dataChannel{}

const defaultReadBufferLen = 8192

// Package pion detached data channel into a net.Conn
// and then a network.MuxedStream
type dataChannel struct {
	// TODO: Are these circular references okay?
	channel       *webrtc.DataChannel
	laddr         net.Addr
	raddr         net.Addr
	readDeadline  *deadline
	writeDeadline *deadline

	closeWriteOnce sync.Once
	closeReadOnce  sync.Once
	resetOnce      sync.Once

	remoteWriteClosed atomic.Bool
	localWriteClosed  atomic.Bool

	remoteReadClosed atomic.Bool
	localReadClosed  atomic.Bool

	ctx        context.Context
	cancel     context.CancelFunc
	m          sync.Mutex
	readBuf    bytes.Buffer
	readSignal chan struct{}
}

func newDataChannel(
	channel *webrtc.DataChannel,
	pc *webrtc.PeerConnection,
	laddr, raddr net.Addr) *dataChannel {
	ctx, cancel := context.WithCancel(context.Background())

	result := &dataChannel{
		channel:       channel,
		laddr:         laddr,
		raddr:         raddr,
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
		ctx:           ctx,
		cancel:        cancel,
		readSignal:    make(chan struct{}),
	}
	result.readBuf.Grow(defaultReadBufferLen)

	channel.OnMessage(result.handleMessage)

	return result
}

func (d *dataChannel) handleMessage(msg webrtc.DataChannelMessage) {
	d.m.Lock()
	defer d.m.Unlock()
	if msg.IsString {
		return
	}

	var pbmsg pb.Message
	if err := pbmsg.Unmarshal(msg.Data); err != nil {
		log.Warnf("could not unmarshal protobuf message")
		return
	}

	if !d.remoteWriteClosed.Load() && !d.localReadClosed.Load() {
		d.readBuf.Write(pbmsg.GetMessage())
		select {
		case d.readSignal <- struct{}{}:
		default:
		}
	}

	if pbmsg.Flag != nil {
		switch pbmsg.GetFlag() {
		case pb.Message_FIN:
			d.remoteWriteClosed.Store(true)
			select {
			case <-d.readSignal:
			default:
				close(d.readSignal)
			}

		case pb.Message_STOP_SENDING:
			d.remoteReadClosed.Store(true)
		case pb.Message_RESET:
			log.Errorf("remote reset")
			d.Close()
		}
	}

}

func (d *dataChannel) Read(b []byte) (int, error) {
	for {

		d.m.Lock()
		read, err := d.readBuf.Read(b)
		d.m.Unlock()
		if err == io.EOF && d.remoteWriteClosed.Load() {
			return read, io.EOF
		}
		// log.Warnf("read %d bytes: %s", read, string(b[:read]))
		if read > 0 {
			return read, nil
		}

		select {
		case <-d.readSignal:
		case <-d.ctx.Done():
			return 0, d.ctx.Err()
		case <-d.readDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func (d *dataChannel) Write(b []byte) (int, error) {
	if d.localWriteClosed.Load() || d.remoteReadClosed.Load() {
		return 0, io.ErrClosedPipe
	}
	msg := &pb.Message{
		Message: b,
	}
	data, err := msg.Marshal()
	if err != nil {
		return 0, err
	}
	d.channel.Send(data)
	return len(b), nil
}

func (d *dataChannel) Close() error {
	select {
	case <-d.ctx.Done():
		return nil
	default:
	}
	d.cancel()
	d.CloseWrite()
	_ = d.channel.Close()
	return nil
}

func (d *dataChannel) CloseRead() error {
	var err error
	d.closeReadOnce.Do(func() {
		d.localReadClosed.Store(true)
		msg := &pb.Message{
			Flag: pb.Message_STOP_SENDING.Enum(),
		}
		data, err := msg.Marshal()
		if err != nil {
			return
		}
		err = d.channel.Send(data)
		if err != nil {
			return
		}
	})
	return err

}

func (d *dataChannel) remoteClosed() {
	d.cancel()
}

func (d *dataChannel) CloseWrite() error {
	var err error
	d.closeWriteOnce.Do(func() {
		d.localWriteClosed.Store(true)
		msg := &pb.Message{
			Flag: pb.Message_FIN.Enum(),
		}
		data, err := msg.Marshal()
		if err != nil {
			return
		}
		err = d.channel.Send(data)
		if err != nil {
			return
		}
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
	var err error
	d.resetOnce.Do(func() {
		msg := &pb.Message{Flag: pb.Message_RESET.Enum()}
		data, err := msg.Marshal()
		if err != nil {
			return
		}
		err = d.channel.Send(data)
		if err != nil {
			return
		}
		d.channel.Close()
	})
	return err
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
