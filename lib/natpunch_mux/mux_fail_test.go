package natpunch_mux

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestNewConnReturnsWhenPeerClosesDuringSetup(t *testing.T) {
	left, right := net.Pipe()
	muxLeft := NewMux(left, "tcp", 60)
	t.Cleanup(func() {
		_ = muxLeft.Close()
		_ = right.Close()
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

	// 在远端返回 muxNewConnOk 之前，直接对该流发送 muxConnClose。
	// 这正是旧实现会白等两分钟的分支。
	if err := waitAndRejectNewConn(right); err != nil {
		t.Fatalf("inject close: %v", err)
	}

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

func waitAndRejectNewConn(remote net.Conn) error {
	_ = remote.SetReadDeadline(time.Now().Add(time.Second))
	for {
		var header [5]byte
		if _, err := io.ReadFull(remote, header[:]); err != nil {
			return err
		}
		flag := header[0]
		id := int32(binary.LittleEndian.Uint32(header[1:]))
		switch flag {
		case muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn:
			var l [2]byte
			if _, err := io.ReadFull(remote, l[:]); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, remote, int64(binary.LittleEndian.Uint16(l[:]))); err != nil {
				return err
			}
		case muxMsgSendOk:
			var window [8]byte
			if _, err := io.ReadFull(remote, window[:]); err != nil {
				return err
			}
		case muxNewConn:
			var frame [5]byte
			frame[0] = muxConnClose
			binary.LittleEndian.PutUint32(frame[1:], uint32(id))
			_, err := remote.Write(frame[:])
			return err
		}
	}
}
