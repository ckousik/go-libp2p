package libp2pwebrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeadlineExtend(t *testing.T) {
	dl := newDeadline()
	start := time.Now()
	dl.set(start.Add(500 * time.Millisecond))
	done := make(chan struct{})
	go func() {
		<-dl.wait()
		end := time.Now()
		d := end.Sub(start)
		require.GreaterOrEqual(t, d, 900*time.Millisecond)
		require.LessOrEqual(t, d, 1100*time.Millisecond)
		close(done)
	}()
	timer := time.AfterFunc(300*time.Millisecond, func() {
		dl.set(start.Add(1000 * time.Millisecond))
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal()
	}
	timer.Stop()
}

func TestDeadlineSetToPast(t *testing.T) {
	dl := newDeadline()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		<-dl.wait()
		end := time.Now()
		d := end.Sub(start)
		require.GreaterOrEqual(t, d, 200*time.Millisecond)
		require.LessOrEqual(t, d, 500*time.Millisecond)
		close(done)
	}()
	timer := time.AfterFunc(300*time.Millisecond, func() {
		dl.set(start.Add(200 * time.Millisecond))
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal()
	}
	timer.Stop()
}
