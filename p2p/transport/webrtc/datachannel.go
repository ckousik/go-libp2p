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

const defaultReadBufferLen = 4096

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

	remoteWriteClosed uint32
	localWriteClosed  uint32

	remoteReadClosed uint32
	localReadClosed  uint32

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
	if msg.IsString {
		log.Warnf("received string message")
		return
	}

	var pbmsg pb.Message
	if err := pbmsg.Unmarshal(msg.Data); err != nil {
		log.Warnf("could not unmarshal protobuf message")
		return
	}

	if !d.isRemoteWriteClosed() && !d.isLocalReadClosed() {
		d.m.Lock()
		d.readBuf.Write(pbmsg.GetMessage())
		// n, err := d.readBuf.Write(pbmsg.GetMessage())
		// log.Warnf("wrote %d bytes to buffer, msg size: %d: %v", n, len(pbmsg.GetMessage()), err)
		d.m.Unlock()
		select {
		case d.readSignal <- struct{}{}:
		default:
		}
	}

	if pbmsg.Flag != nil {
		switch pbmsg.GetFlag() {
		case pb.Message_FIN:
			atomic.StoreUint32(&d.remoteWriteClosed, 1)
			select {
			case <-d.readSignal:
			default:
				close(d.readSignal)
			}

		case pb.Message_STOP_SENDING:
			atomic.StoreUint32(&d.remoteReadClosed, 1)
		case pb.Message_RESET:
			log.Errorf("remote reset")
			d.Close()
		}
	}

}

func (d *dataChannel) Read(b []byte) (int, error) {
	for {
		select {
		case <-d.readDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		default:
		}

		d.m.Lock()
		read, err := d.readBuf.Read(b)
		d.m.Unlock()
		if err == io.EOF && d.isRemoteWriteClosed() {
			return read, io.EOF
		}
		// log.Warnf("read %d bytes: %s", read, string(b[:read]))
		if read > 0 {
			return read, nil
		}

		// log.Warnf("waiting for read")
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
	if d.isLocalWriteClosed() || d.isRemoteReadClosed() {
		return 0, io.ErrClosedPipe
	}
	select {
	case <-d.writeDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	msg := &pb.Message{
		Message: b,
	}
	data, err := msg.Marshal()
	if err != nil {
		log.Warnf("write failed on datachannel: %s", err)
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
		atomic.StoreUint32(&d.localReadClosed, 1)
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
		atomic.StoreUint32(&d.localWriteClosed, 1)
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

func (d *dataChannel) isRemoteWriteClosed() bool {
	return atomic.LoadUint32(&d.remoteWriteClosed) == 1
}

func (d *dataChannel) isLocalWriteClosed() bool {
	return atomic.LoadUint32(&d.localWriteClosed) == 1
}

func (d *dataChannel) isRemoteReadClosed() bool {
	return atomic.LoadUint32(&d.remoteReadClosed) == 1
}

func (d *dataChannel) isLocalReadClosed() bool {
	return atomic.LoadUint32(&d.localReadClosed) == 1
}
