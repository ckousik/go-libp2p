package libp2pwebrtc

import (
	"sync"
	"time"
)

type deadline struct {
	m         sync.Mutex
	timer     *time.Timer
	closeChan chan struct{}
}

func newDeadline() *deadline {
	return &deadline{
		timer:     nil,
		closeChan: make(chan struct{}),
	}
}

func (d *deadline) set(t time.Time) {
	d.m.Lock()
	defer d.m.Unlock()
	// if an existing timer is set, stop the timer
	// from firing and drain the channel
	if d.timer != nil {
		if !d.timer.Stop() {
			<-d.closeChan
		}
	}

	// remove reference to existing stopped timer
	// so it can be GC'd
	d.timer = nil

	closed := isClosed(d.closeChan)

	// no deadline
	if t.IsZero() {
		if closed {
			d.closeChan = make(chan struct{})
		}
		return
	}

	if duration := time.Until(t); duration < 0 {
		if !closed {
			close(d.closeChan)
		}
	} else {
		if closed {
			d.closeChan = make(chan struct{})
		}
		d.timer = time.AfterFunc(duration, func() {
			// check to ensure channel is not closed
			// twice
			select {
			case <-d.closeChan:
			default:
				close(d.closeChan)
			}
		})
	}
}

func (d *deadline) wait() <-chan struct{} {
	return d.closeChan
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
	}
	return false
}
