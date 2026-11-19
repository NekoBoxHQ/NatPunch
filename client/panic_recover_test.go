package client

import (
	"net"
	"sync"
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

// onceClose：多路径并发调用只真正执行一次（面板终端的主流程与 io.Copy goroutine）。
func TestOnceCloseRunsOnceUnderConcurrency(t *testing.T) {
	var n int32
	f := onceClose(func() { atomic.AddInt32(&n, 1) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("onceClose 应只执行 1 次，实际 %d 次", got)
	}
}
