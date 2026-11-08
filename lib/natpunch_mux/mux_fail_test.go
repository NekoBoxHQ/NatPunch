package natpunch_mux

import (
	"net"
	"testing"
	"time"
)

func TestNewConnReturnsWhenPeerClosesDuringSetup(t *testing.T) {
	left, right := net.Pipe()
	muxLeft := NewMux(left, "tcp", 60)
	muxRight := NewMux(right, "tcp", 60)
	t.Cleanup(func() {
		_ = muxLeft.Close()
		_ = muxRight.Close()
	})

	type result struct {
		c   net.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := muxLeft.NewConn()
		done <- result{c: c, err: err}
	}()

	peer, err := muxRight.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = peer.Close()

	select {
	case got := <-done:
		if got.c != nil {
			_ = got.c.Close()
		}
		if got.err == nil {
			t.Fatal("NewConn succeeded after the peer closed the stream")
		}
	case <-time.After(time.Second):
		t.Fatal("NewConn did not wake after the peer closed the stream")
	}
}
