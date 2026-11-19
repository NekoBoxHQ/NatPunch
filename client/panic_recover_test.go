package client

import (
	"net"
	"sync/atomic"
	"testing"
)

type probeConn struct {
	net.Conn
	closed atomic.Bool
}

func (p *probeConn) Close() error {
	p.closed.Store(true)
	return nil
}

// 单条流的 panic 必须被兜住：只关这条流，不能让进程退出。
func TestRecoverChanPanicClosesStreamAndSwallows(t *testing.T) {
	c := &probeConn{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic 未被兜住，进程会退出: %v", r)
			}
		}()
		func() {
			defer recoverChanPanic(c, "test")
			panic("boom")
		}()
	}()
	if !c.closed.Load() {
		t.Fatal("panic 后必须关闭这条流")
	}
}

// 没有 panic 时不能误关流。
func TestRecoverChanPanicNoopWithoutPanic(t *testing.T) {
	c := &probeConn{}
	func() {
		defer recoverChanPanic(c, "test")
	}()
	if c.closed.Load() {
		t.Fatal("没有 panic 不该关闭流")
	}
}
