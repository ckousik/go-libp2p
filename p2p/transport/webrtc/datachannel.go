package libp2pwebrtc

import (
	"bytes"
	"context"
	"io"
	"os"

	"net"

	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-msgio/protoio"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"

	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
)

var _ network.MuxedStream = &dataChannel{}

const (
	// bufferedAmountLowThreshold and maxBufferedAmount are bound
	// to a stream but congestion control is done on the whole
	// SCTP association. This means that a single stream can monopolize
	// the complete congestion control window (cwnd) if it does not
	// read stream data and it's remote continues to send.
	bufferedAmountLowThreshold uint64 = 1024
	// Max message size limit in Pion is 2^16
	maxBufferedAmount uint64 = 65536
)

const (
	stateOpen uint32 = iota
	stateReadClosed
	stateWriteClosed
	stateClosed
)

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

	state uint32

	ctx            context.Context
	cancel         context.CancelFunc
	m              sync.Mutex
	readBuf        bytes.Buffer
	writeAvailable chan struct{}

	writer protoio.Writer
	reader protoio.Reader
}

func newDataChannel(
	channel *webrtc.DataChannel,
	rwc datachannel.ReadWriteCloser,
	pc *webrtc.PeerConnection,
	laddr, raddr net.Addr) *dataChannel {
	ctx, cancel := context.WithCancel(context.Background())

	result := &dataChannel{
		channel:        channel,
		laddr:          laddr,
		raddr:          raddr,
		readDeadline:   newDeadline(),
		writeDeadline:  newDeadline(),
		ctx:            ctx,
		cancel:         cancel,
		writer:         protoio.NewDelimitedWriter(rwc),
		reader:         protoio.NewDelimitedReader(rwc, 1500),
		writeAvailable: make(chan struct{}),
	}

	// channel.OnMessage(result.handleMessage)
	channel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)
	channel.OnBufferedAmountLow(func() {
		result.writeAvailable <- struct{}{}
	})

	return result
}

func (d *dataChannel) processControlMessage(msg pb.Message) {
	d.m.Lock()
	defer d.m.Unlock()
	if d.state == stateClosed {
		return
	}
	switch msg.GetFlag() {
	case pb.Message_FIN:
		if d.state == stateWriteClosed {
			d.Close()
			return
		}
		d.state = stateReadClosed
	case pb.Message_STOP_SENDING:
		if d.state == stateReadClosed {
			d.Close()
			return
		}
		d.state = stateWriteClosed
	case pb.Message_RESET:
		d.channel.Close()
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
		if state := d.getState(); err == io.EOF && (state == stateReadClosed || state == stateClosed) {
			return read, io.EOF
		}
		if read > 0 {
			return read, nil
		}

		// read until data message
		var msg pb.Message
		signal := make(chan struct {
			error
		})

		// read in a separate goroutine to enable read deadlines
		go func() {
			err = d.reader.ReadMsg(&msg)
			if err != nil {
				signal <- struct {
					error
				}{err}
				return
			}
			if state := d.getState(); state != stateClosed && state != stateReadClosed {
				d.m.Lock()
				d.readBuf.Write(msg.GetMessage())
				d.m.Unlock()
			}
			// process control message
			if msg.Flag != nil {
				d.processControlMessage(msg)
			}
			signal <- struct{ error }{nil}

		}()
		select {
		case sig := <-signal:
			if sig.error != nil {
				return 0, sig.error
			}
		case <-d.readDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}

	}
}

func (d *dataChannel) Write(b []byte) (int, error) {
	if s := d.getState(); s == stateWriteClosed || s == stateClosed {
		return 0, io.ErrClosedPipe
	}

	var err error
	var (
		start     int = 0
		end           = 0
		written       = 0
		chunkSize     = 1024*1024 - 10
		n             = 0
	)

	for start < len(b) {
		end = len(b)
		if start+chunkSize < end {
			end = start + chunkSize
		}
		chunk := b[start:end]
		n, err = d.partialWrite(chunk)
		if err != nil {
			break
		}
		written += n
		start = end
	}
	return written, err

}

func (d *dataChannel) partialWrite(b []byte) (int, error) {
	if s := d.getState(); s == stateWriteClosed || s == stateClosed {
		return 0, io.ErrClosedPipe
	}
	select {
	case <-d.writeDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	msg := &pb.Message{Message: b}
	// approximate overhead
	if d.channel.BufferedAmount()+uint64(len(b))+10 > maxBufferedAmount {
		select {
		case <-d.writeAvailable:
		case <-d.writeDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
	return len(b), d.writer.WriteMsg(msg)
}

func (d *dataChannel) Close() error {
	select {
	case <-d.ctx.Done():
		return nil
	default:
	}

	d.m.Lock()
	d.state = stateClosed
	d.m.Unlock()

	d.cancel()
	d.CloseWrite()
	_ = d.channel.Close()
	return nil
}

func (d *dataChannel) CloseRead() error {
	var err error
	d.closeReadOnce.Do(func() {
		d.m.Lock()
		if d.state != stateClosed {
			d.state = stateReadClosed
		}
		d.m.Unlock()
		msg := &pb.Message{
			Flag: pb.Message_STOP_SENDING.Enum(),
		}
		err = d.writer.WriteMsg(msg)
	})
	return err

}

func (d *dataChannel) remoteClosed() {
	d.m.Lock()
	defer d.m.Unlock()
	d.state = stateClosed
	d.cancel()

}

func (d *dataChannel) CloseWrite() error {
	var err error
	d.closeWriteOnce.Do(func() {
		d.m.Lock()
		if d.state != stateClosed {
			d.state = stateWriteClosed
		}
		d.m.Unlock()
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
		err = d.writer.WriteMsg(msg)
		d.Close()
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

func (d *dataChannel) getState() uint32 {
	d.m.Lock()
	defer d.m.Unlock()
	return d.state
}
