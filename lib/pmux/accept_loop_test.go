package pmux

import (
	"errors"
	"net"
	"testing"
	"time"
)

// fakeListener 按脚本返回 Accept 结果，脚本耗尽后一律 net.ErrClosed。
type fakeListener struct {
	script []step
	calls  int
}

type step struct {
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

var errTemp = errors.New("accept tcp: too many open files")

// 临时错误不能让端口复用的 accept 循环退出（旧实现直接 return，等于唯一入口永久消失），
// 且必须退避，不能零延迟空转。
func TestPmuxAcceptLoopBacksOffAndSurvivesTempErrors(t *testing.T) {
	l := &fakeListener{script: []step{
		{err: errTemp},
		{err: errTemp},
		{conn: &fakeConn{}},
		{err: errTemp},
	}}
	var slept []time.Duration
	handled := 0
	done := make(chan struct{})
	go func() {
		acceptLoop(l, func(net.Conn) { handled++ }, func(d time.Duration) { slept = append(slept, d) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acceptLoop 没有在 listener 关闭后退出")
	}
	if handled != 1 {
		t.Fatalf("成功 accept 的连接应被交给 handle 一次，实际 %d", handled)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond}
	if len(slept) != len(want) {
		t.Fatalf("期望退避 %d 次，实际 %v", len(want), slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("退避序列应为 %v（成功后退避重置），实际 %v", want, slept)
		}
	}
}

func TestPmuxAcceptLoopExitsWhenListenerClosed(t *testing.T) {
	l := &fakeListener{script: []step{{err: net.ErrClosed}}}
	slept := 0
	done := make(chan struct{})
	go func() {
		acceptLoop(l, func(net.Conn) {}, func(time.Duration) { slept++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("listener 关闭后 acceptLoop 没有退出（会永久自旋）")
	}
	if slept != 0 {
		t.Errorf("listener 关闭属于终止条件，不应退避，实际退避 %d 次", slept)
	}
}
