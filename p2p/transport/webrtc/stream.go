package libp2pwebrtc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-msgio/pbio"
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"

	pb "github.com/libp2p/go-libp2p/p2p/transport/webrtc/pb"
	"google.golang.org/protobuf/proto"
)

var _ network.MuxedStream = &webRTCStream{}

const (
	// maxMessageSize is limited to 16384 bytes in the SDP.
	maxMessageSize int = 16384
	// Pion SCTP association has an internal receive buffer of 1MB (roughly, 1MB per connection).
	// We can change this value in the SettingEngine before creating the peerconnection.
	// https://github.com/pion/webrtc/blob/v3.1.49/sctptransport.go#L341
	maxBufferedAmount int = 2 * maxMessageSize
	// bufferedAmountLowThreshold and maxBufferedAmount are bound
	// to a stream but congestion control is done on the whole
	// SCTP association. This means that a single stream can monopolize
	// the complete congestion control window (cwnd) if it does not
	// read stream data and it's remote continues to send. We can
	// add messages to the send buffer once there is space for 1 full
	// sized message.
	bufferedAmountLowThreshold uint64 = uint64(maxBufferedAmount) / 2

	// Proto overhead assumption is 5 bytes
	protoOverhead int = 5
	// Varint overhead is assumed to be 2 bytes. This is safe since
	// 1. This is only used and when writing message, and
	// 2. We only send messages in chunks of `maxMessageSize - varintOverhead`
	// which includes the data and the protobuf header. Since `maxMessageSize`
	// is less than or equal to 2 ^ 14, the varint will not be more than
	// 2 bytes in length.
	varintOverhead int = 2
)

// Using pbio.Writer concurrently is not safe since it could result in
// different message becoming mixed. `protectedWriter` wraps the writer
// to ensure only a single message is written at a time.
type protectedWriter struct {
	sync.Mutex
	w pbio.Writer
}

func (w *protectedWriter) Write(msg proto.Message) error {
	w.Lock()
	defer w.Unlock()
	return w.w.WriteMsg(msg)
}

// Package pion detached data channel into a net.Conn
// and then a network.MuxedStream
type webRTCStream struct {
	conn  *connection
	id    uint16
	rwc   datachannel.ReadWriteCloser
	laddr net.Addr
	raddr net.Addr

	closeWriteOnce sync.Once
	closeReadOnce  sync.Once
	closeOnce      sync.Once
	readLoopOnce   sync.Once

	reader pbio.Reader
	writer *protectedWriter

	m             sync.Mutex
	readBuf       []byte
	writeDeadline time.Time
	readDeadline  time.Time
	state         channelState
	// hack for closing the Read side using a deadline
	// in case `Read` does not return.
	closeErr error

	// simplifies signaling deadline updated and write available
	writeAvailable  *signal
	deadlineUpdated *signal

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func newStream(
	connection *connection,
	channel *webrtc.DataChannel,
	rwc datachannel.ReadWriteCloser,
	laddr, raddr net.Addr,
) *webRTCStream {
	ctx, cancel := context.WithCancel(context.Background())

	reader := bufio.NewReaderSize(rwc, maxMessageSize)

	result := &webRTCStream{
		conn:            connection,
		id:              *channel.ID(),
		rwc:             rwc,
		laddr:           laddr,
		raddr:           raddr,
		ctx:             ctx,
		cancel:          cancel,
		writeAvailable:  &signal{c: make(chan struct{})},
		deadlineUpdated: &signal{c: make(chan struct{})},
		reader:          pbio.NewDelimitedReader(reader, maxMessageSize),
		writer:          &protectedWriter{w: pbio.NewDelimitedWriter(rwc)},
	}

	channel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)
	channel.OnBufferedAmountLow(func() {
		result.writeAvailable.signal()
	})

	return result
}

// Read from the underlying datachannel. This also
// process sctp control messages such as DCEP, which is
// handled internally by pion, and stream closure which
// is signaled by `Read` on the datachannel returning
// io.EOF.
func (d *webRTCStream) Read(b []byte) (int, error) {
	for {
		if d.isClosed() {
			closeErr := d.getCloseErr()
			if closeErr != nil {
				return 0, closeErr
			}
			return 0, io.ErrClosedPipe
		}
		d.m.Lock()
		if !d.readDeadline.IsZero() && d.readDeadline.Before(time.Now()) {
			d.m.Unlock()
			log.Debugf("[1] deadline exceeded: closeErr: %v", d.getCloseErr())
			return 0, os.ErrDeadlineExceeded
		}
		read := copy(b, d.readBuf)
		d.readBuf = d.readBuf[read:]
		remaining := len(d.readBuf)
		d.m.Unlock()

		if remaining == 0 && !d.getState().allowRead() {
			closeErr := d.getCloseErr()
			log.Debugf("[2] stream closed or empty: closeErr: %v", closeErr)
			if closeErr != nil {
				return read, closeErr
			}
			return read, io.EOF
		}

		if read > 0 || read == len(b) {
			return read, nil
		}

		// read from datachannel
		var msg pb.Message
		err := d.reader.ReadMsg(&msg)
		if err != nil {
			// This case occurs when the remote node goes away
			// without writing a FIN message
			if errors.Is(err, io.EOF) {
				d.datachannelClosed()
				return 0, io.ErrClosedPipe
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// if the stream has been force closed or force reset
				// using SetReadDeadline, we check if closeErr was set.
				closeErr := d.getCloseErr()
				log.Debugf("closing stream, checking error: %v closeErr: %v", err, closeErr)
				if closeErr != nil {
					return 0, closeErr
				}
			}
			return 0, err
		}

		// append incoming data to read buffer
		d.m.Lock()
		if d.state.allowRead() && msg.Message != nil {
			d.readBuf = append(d.readBuf, msg.GetMessage()...)
		}
		d.m.Unlock()

		// process any flags on the message
		if msg.Flag != nil {
			d.processIncomingFlag(msg.GetFlag())
		}
	}
}

// NOTE: The `Write` method on the datachannel ReadWriteCloser
// is misleading since it does not return the number of bytes written
// to the remote but the number of bytes queued for sending. We can know
// how much data is in the queue using the `BufferedAmount` method.
func (d *webRTCStream) Write(b []byte) (int, error) {
	state := d.getState()
	if !state.allowWrite() {
		return 0, io.ErrClosedPipe
	}

	// Check if there is any message on the wire. This is used for control
	// messages only when the read side of the stream is closed
	if state == stateReadClosed {
		d.readLoopOnce.Do(func() {
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				// zero the read deadline, so read call only returns
				// when the underlying datachannel closes or there is
				// a message on the channel
				d.rwc.(*datachannel.DataChannel).SetReadDeadline(time.Time{})
				var msg pb.Message
				for {
					if state := d.getState(); state == stateClosed {
						return
					}
					err := d.reader.ReadMsg(&msg)
					if err != nil {
						if errors.Is(err, io.EOF) {
							d.datachannelClosed()
						}
						return
					}
					if msg.Flag != nil {
						d.processIncomingFlag(msg.GetFlag())
					}
				}
			}()
		})
	}

	var err error
	var (
		chunkSize = maxMessageSize - protoOverhead - varintOverhead
		n         = 0
	)

	for len(b) > 0 {
		end := min(chunkSize, len(b))

		written, err := d.partialWrite(b[:end])
		if err != nil {
			return n + written, err
		}
		b = b[end:]
		n += written
	}
	return n, err
}

func (d *webRTCStream) partialWrite(b []byte) (int, error) {
	// if the next message will add more data than we are willing to buffer,
	// block until we have sent enough bytes to reduce the amount of data buffered.
	timeout := make(chan struct{})
	var deadlineTimer *time.Timer
	for {
		if !d.getState().allowWrite() {
			return 0, io.ErrClosedPipe
		}
		// prepare waiting for writeAvailable signal
		// if write is blocked
		d.m.Lock()
		deadline := d.writeDeadline
		d.m.Unlock()
		deadlineUpdated := d.deadlineUpdated.wait()
		writeAvailable := d.writeAvailable.wait()

		if !deadline.IsZero() {
			// check if deadline exceeded
			if deadline.Before(time.Now()) {
				return 0, os.ErrDeadlineExceeded
			}

			if deadlineTimer == nil {
				deadlineTimer = time.AfterFunc(time.Until(deadline), func() { close(timeout) })
				defer deadlineTimer.Stop()
			}
			deadlineTimer.Reset(time.Until(deadline))
		}

		msg := &pb.Message{Message: b}
		bufferedAmount := int(d.rwc.(*datachannel.DataChannel).BufferedAmount())
		addedBuffer := bufferedAmount + varintOverhead + proto.Size(msg)
		if addedBuffer > maxBufferedAmount {
			select {
			case <-timeout:
				return 0, os.ErrDeadlineExceeded
			case <-writeAvailable:
				err := d.writer.Write(msg)
				if err != nil {
					return 0, err
				}
				return len(b), nil
			case <-d.ctx.Done():
				log.Debugf("closed pipe")
				return 0, io.ErrClosedPipe
			case <-deadlineUpdated:

			}
		} else {
			err := d.writer.Write(msg)
			if err != nil {
				return 0, err
			}
			return len(b), nil
		}
	}
}

func (d *webRTCStream) Close() error {
	return d.close(false, true)
}

func (d *webRTCStream) CloseRead() error {
	if d.isClosed() {
		return nil
	}
	var err error
	d.closeReadOnce.Do(func() {
		err = d.writer.Write(&pb.Message{Flag: pb.Message_STOP_SENDING.Enum()})
		if err != nil {
			log.Debug("could not write STOP_SENDING message")
			err = fmt.Errorf("could not close stream for reading: %w", err)
			return
		}
		d.m.Lock()
		current := d.state
		next := d.state.processOutgoingFlag(pb.Message_STOP_SENDING)
		d.state = next
		d.m.Unlock()

		// check if closure required
		if current != next && next == stateClosed {
			d.close(false, true)
		}
	})
	return err
}

func (d *webRTCStream) CloseWrite() error {
	if d.isClosed() {
		return nil
	}
	var err error
	d.closeWriteOnce.Do(func() {
		err = d.writer.Write(&pb.Message{Flag: pb.Message_FIN.Enum()})
		if err != nil {
			log.Debug("could not write FIN message")
			err = fmt.Errorf("could not close stream for writing: %w", err)
			return
		}
		// if successfully written, process the outgoing flag
		d.m.Lock()
		current := d.state
		next := d.state.processOutgoingFlag(pb.Message_FIN)
		d.state = next
		d.m.Unlock()
		// unblock and fail any ongoing writes
		d.writeAvailable.signal()

		// check if closure required
		if current != next && next == stateClosed {
			d.close(false, true)
		}
	})
	return err
}

func (d *webRTCStream) LocalAddr() net.Addr {
	return d.laddr
}

func (d *webRTCStream) RemoteAddr() net.Addr {
	return d.raddr
}

func (d *webRTCStream) Reset() error {
	return d.close(true, true)
}

func (d *webRTCStream) SetDeadline(t time.Time) error {
	d.m.Lock()
	defer d.m.Unlock()
	d.writeDeadline = t
	return nil
}

func (d *webRTCStream) SetReadDeadline(t time.Time) error {
	if d.isClosed() {
		return nil
	}
	return d.setReadDeadline(t)
}

func (d *webRTCStream) SetWriteDeadline(t time.Time) error {
	d.m.Lock()
	d.writeDeadline = t
	d.m.Unlock()
	d.deadlineUpdated.signal()
	return nil
}

func (d *webRTCStream) setReadDeadline(t time.Time) error {
	d.m.Lock()
	defer d.m.Unlock()
	d.readDeadline = t
	return d.rwc.(*datachannel.DataChannel).SetReadDeadline(t)
}

func (d *webRTCStream) getState() channelState {
	d.m.Lock()
	defer d.m.Unlock()
	return d.state
}

func (d *webRTCStream) getCloseErr() error {
	d.m.Lock()
	defer d.m.Unlock()
	return d.closeErr
}

// datachannelClosed is called when the remote closes
// the datachannel, or disconnects.
func (d *webRTCStream) datachannelClosed() {
	d.close(true, true)
}

// this is used to force reset a stream
func (d *webRTCStream) close(isReset bool, notifyConnection bool) error {
	if d.isClosed() {
		return nil
	}
	var err error
	d.closeOnce.Do(func() {
		log.Warnf("closing: reset: %v, notify: %v", isReset, notifyConnection)
		d.m.Lock()
		d.state = stateClosed
		if d.closeErr == nil {
			d.closeErr = io.EOF
			if isReset {
				d.closeErr = io.ErrClosedPipe
			}

		}
		d.m.Unlock()
		// force close reads
		d.setReadDeadline(time.Now().Add(-100 * time.Millisecond))
		if isReset {
			// write the RESET message. The error is explicitly ignored
			// because we do not know if the remote is still connected
			_ = d.writer.Write(&pb.Message{Flag: pb.Message_RESET.Enum()})
		} else {
			// write a FIN message for standard stream closure
			_ = d.writer.Write(&pb.Message{Flag: pb.Message_FIN.Enum()})
		}
		// unblock any writes
		// d.writeAvailable.signal()
		// close the context
		d.cancel()
		// close the channel. We do not care about the error message in
		// this case
		err = d.rwc.Close()
		if notifyConnection && d.conn != nil {
			d.conn.removeStream(d.id)
		}
	})

	return err
}

func (d *webRTCStream) processIncomingFlag(flag pb.Message_Flag) {
	if d.isClosed() {
		return
	}
	d.m.Lock()
	current := d.state
	next := d.state.handleIncomingFlag(flag)
	d.state = next
	d.m.Unlock()

	if current != next && next == stateClosed {
		log.Warnf("closing: received flag: %v", flag)
		defer d.close(flag == pb.Message_RESET, true)
	}
}

func (d *webRTCStream) isClosed() bool {
	select {
	case <-d.ctx.Done():
		return true
	default:
		return false
	}
}

// signal struct
type signal struct {
	sync.Mutex
	c chan struct{}
}

func (s *signal) wait() <-chan struct{} {
	s.Lock()
	defer s.Unlock()
	return s.c
}

func (s *signal) signal() {
	s.Lock()
	c := s.c
	s.c = make(chan struct{})
	s.Unlock()
	close(c)
}
