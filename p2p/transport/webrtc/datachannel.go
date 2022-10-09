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
	"github.com/libp2p/go-msgio/protoio"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"

	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
)

var _ network.MuxedStream = &dataChannel{}

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

	ctx     context.Context
	cancel  context.CancelFunc
	m       sync.Mutex
	readBuf bytes.Buffer
	writer  protoio.Writer
	reader  protoio.Reader
}

func newDataChannel(
	channel *webrtc.DataChannel,
	rwc datachannel.ReadWriteCloser,
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
		writer:        protoio.NewDelimitedWriter(rwc),
		reader:        protoio.NewDelimitedReader(rwc, 1500),
	}

	// channel.OnMessage(result.handleMessage)

	return result
}

// func (d *dataChannel) handleMessage(msg webrtc.DataChannelMessage) {
// 	if msg.IsString {
// 		log.Warnf("received string message")
// 		return
// 	}

// 	var pbmsg pb.Message
// 	if err := pbmsg.Unmarshal(msg.Data); err != nil {
// 		log.Warnf("could not unmarshal protobuf message")
// 		return
// 	}

// 	if !d.isRemoteWriteClosed() && !d.isLocalReadClosed() {
// 		d.m.Lock()
// 		d.readBuf.Write(pbmsg.GetMessage())
// 		// n, err := d.readBuf.Write(pbmsg.GetMessage())
// 		// log.Warnf("wrote %d bytes to buffer, msg size: %d: %v", n, len(pbmsg.GetMessage()), err)
// 		d.m.Unlock()
// 		select {
// 		case d.readSignal <- struct{}{}:
// 		default:
// 		}
// 	}

// 	if pbmsg.Flag != nil {
// 		switch pbmsg.GetFlag() {
// 		case pb.Message_FIN:
// 			atomic.StoreUint32(&d.remoteWriteClosed, 1)
// 			select {
// 			case <-d.readSignal:
// 			default:
// 				close(d.readSignal)
// 			}

// 		case pb.Message_STOP_SENDING:
// 			atomic.StoreUint32(&d.remoteReadClosed, 1)
// 		case pb.Message_RESET:
// 			log.Errorf("remote reset")
// 			d.Close()
// 		}
// 	}

// }

// func (d *dataChannel) Read(b []byte) (int, error) {
// 	for {
// 		select {
// 		case <-d.readDeadline.wait():
// 			return 0, os.ErrDeadlineExceeded
// 		default:
// 		}

// 		d.m.Lock()
// 		read, err := d.readBuf.Read(b)
// 		d.m.Unlock()
// 		if err == io.EOF && d.isRemoteWriteClosed() {
// 			return read, io.EOF
// 		}
// 		// log.Warnf("read %d bytes: %s", read, string(b[:read]))
// 		if read > 0 {
// 			return read, nil
// 		}

// 		// log.Warnf("waiting for read")
// 		select {
// 		case <-d.readSignal:
// 		case <-d.ctx.Done():
// 			return 0, d.ctx.Err()
// 		case <-d.readDeadline.wait():
// 			return 0, os.ErrDeadlineExceeded
// 		}
// 	}
// }

func (d *dataChannel) processControlMessage(msg pb.Message) {
	switch msg.GetFlag() {
	case pb.Message_FIN:
		atomic.StoreUint32(&d.remoteWriteClosed, 1)
	case pb.Message_STOP_SENDING:
		atomic.StoreUint32(&d.remoteReadClosed, 1)
		// TODO: Process reset
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
		if read > 0 {
			return read, nil
		}

		// read until data message
		var msg pb.Message
		err = d.reader.ReadMsg(&msg)
		if err != nil {
			return 0, err
		}
		if !d.isRemoteWriteClosed() && !d.isLocalReadClosed() {
			d.readBuf.Write(msg.GetMessage())
		}
		// process control message
		if msg.Flag != nil {
			d.processControlMessage(msg)
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
	return len(b), d.writer.WriteMsg(msg)
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
		err = d.writer.WriteMsg(msg)
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
		err = d.writer.WriteMsg(msg)
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
