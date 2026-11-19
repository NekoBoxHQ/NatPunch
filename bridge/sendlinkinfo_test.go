package bridge

import (
	"errors"
	"net"
	"testing"
)

// failingConn 让 Write 失败并记录是否被关闭，用于验证“发送失败必须关闭 mux 流”。
type failingConn struct {
	net.Conn
	closed bool
}

func (f *failingConn) Write([]byte) (int, error) { return 0, errors.New("peer closed") }
func (f *failingConn) Close() error              { f.closed = true; return nil }

// sendLinkInfo 失败时不关闭目标流，客户端会一直阻塞在读 link 上，
// 直到整个 mux 关闭才释放（每失败一次漏一条流 + 一个协程）。
func TestSendLinkInfoClosesTargetOnFailure(t *testing.T) {
	target := &failingConn{}
	if err := sendLinkInfo(target, nil); err == nil {
		t.Fatal("Write 失败时 sendLinkInfo 应返回错误")
	}
	if !target.closed {
		t.Fatal("发送失败后必须 Close 这条 mux 流，否则泄漏")
	}
}
