package bridge

import (
	"net"
	"testing"
)

// netConnWrapper 模拟 *tls.Conn：对调用方只暴露 NetConn()，不再能直接断言成 *net.TCPConn。
type netConnWrapper struct {
	net.Conn
}

func (w *netConnWrapper) NetConn() net.Conn { return w.Conn }

func TestTcpConnOfUnwrapsTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer raw.Close()

	tcp, ok := raw.(*net.TCPConn)
	if !ok {
		t.Fatalf("期望 *net.TCPConn，实际 %T", raw)
	}

	// 明文桥接：直接就是 *net.TCPConn
	if got, ok := tcpConnOf(raw); !ok || got != tcp {
		t.Fatalf("明文连接应能直接取出 *net.TCPConn，得到 %v ok=%v", got, ok)
	}

	// TLS 桥接：c.Conn 是 *tls.Conn，必须经 NetConn() 解包，否则 keepalive 设不上
	if got, ok := tcpConnOf(&netConnWrapper{Conn: raw}); !ok || got != tcp {
		t.Fatalf("TLS 连接应通过 NetConn() 解包出 *net.TCPConn，得到 %v ok=%v", got, ok)
	}

	// 非 TCP 连接（内核 pipe）没有 keepalive 可设，应如实返回 false
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, ok := tcpConnOf(a); ok {
		t.Fatal("非 TCP 连接不应被判定为可设 keepalive")
	}
}
