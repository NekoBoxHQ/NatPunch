package conn

import (
	"errors"
	"net"
	"testing"
	"time"
)

// fakeListener 按脚本返回 accept 结果：脚本耗尽后一律返回 net.ErrClosed。
// 这样测试既不占用真实 fd，也不会因为阻塞而挂住。
type fakeListener struct {
	script []acceptStep
	calls  int
}

type acceptStep struct {
	conn net.Conn
	err  error
}

func (f *fakeListener) Accept() (net.Conn, error) {
	i := f.calls
	f.calls++
	if i < len(f.script) {
		return f.script[i].conn, f.script[i].err
	}
	return nil, net.ErrClosed
}

func (f *fakeListener) Close() error   { return nil }
func (f *fakeListener) Addr() net.Addr { return &net.TCPAddr{} }

type fakeConn struct{ net.Conn }

var errTooManyFiles = errors.New("accept tcp: too many open files")

func TestAcceptLoopBacksOffOnTemporaryError(t *testing.T) {
	l := &fakeListener{script: []acceptStep{
		{err: errTooManyFiles},
		{err: errTooManyFiles},
		{err: errTooManyFiles},
	}}
	var slept []time.Duration
	done := make(chan struct{})
	go func() {
		acceptLoop(l, func(net.Conn) {}, func(d time.Duration) { slept = append(slept, d) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acceptLoop 没有在 listener 关闭后退出（可能仍在自旋）")
	}

	if len(slept) != 3 {
		t.Fatalf("期望退避 3 次，实际 %d 次", len(slept))
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("退避序列不符合 100ms→200ms→400ms，实际 %v", slept)
		}
	}
}

func TestAcceptLoopExitsOnClosedErrors(t *testing.T) {
	cases := []error{
		net.ErrClosed,
		errors.New("accept tcp: use of closed network connection"),
		errors.New("the mux has closed"),
		errors.New("the listener has closed"),
	}
	for _, cerr := range cases {
		l := &fakeListener{script: []acceptStep{{err: cerr}}}
		slept := 0
		done := make(chan struct{})
		go func() {
			acceptLoop(l, func(net.Conn) {}, func(time.Duration) { slept++ })
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("错误 %v 未被识别为终止错误，acceptLoop 没有退出", cerr)
		}
		if slept != 0 {
			t.Errorf("错误 %v 是终止错误，不应退避，实际退避 %d 次", cerr, slept)
		}
	}
}

func TestAcceptLoopResetsBackoffAfterSuccess(t *testing.T) {
	// 两次失败 → 成功一次 → 再失败：成功应把连续失败计数清零
	l := &fakeListener{script: []acceptStep{
		{err: errTooManyFiles},
		{err: errTooManyFiles},
		{conn: &fakeConn{}},
		{err: errTooManyFiles},
	}}
	var slept []time.Duration
	done := make(chan struct{})
	go func() {
		acceptLoop(l, func(net.Conn) {}, func(d time.Duration) { slept = append(slept, d) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acceptLoop 没有退出")
	}
	if len(slept) != 3 {
		t.Fatalf("期望 3 次退避，实际 %v", slept)
	}
	if slept[2] != 100*time.Millisecond {
		t.Fatalf("成功一次后退避未重置：%v", slept)
	}
}
