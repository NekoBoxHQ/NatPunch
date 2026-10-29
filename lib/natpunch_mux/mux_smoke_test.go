package natpunch_mux

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// 最小端到端冒烟：不依赖 docker/tc，用 net.Pipe 拉两条 Mux，
// 验证 建流(NewConn/Accept) → 双向写入 → 读回 → 关闭 这条主链路。
//
// integration 目录里那套要 docker + tc(NET_ADMIN)，CI 之外基本跑不起来；
// 这个用例把核心握手/数据路径放进普通 `go test -race`，改 mux 时能立刻发现
// “建流超时没人回收 / 关流后写侧不醒 / 同流并发写串包”这类回归。
func TestMuxSmokeEndToEnd(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	muxA := NewMux(c1, "tcp", 60)
	defer muxA.Close()
	muxB := NewMux(c2, "tcp", 60)
	defer muxB.Close()

	// B 侧接受一条流
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := muxB.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	// A 侧发起一条流
	client, err := muxA.NewConn()
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	defer client.Close()

	var server net.Conn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		t.Fatalf("Accept: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Accept 超时：建流握手没走通")
	}
	defer server.Close()

	// A → B
	payload := bytes.Repeat([]byte("natpunch-mux-smoke-"), 128)
	writeDone := make(chan error, 1)
	go func() {
		_, werr := client.Write(payload)
		writeDone <- werr
	}()

	buf := make([]byte, len(payload))
	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, rerr := io.ReadFull(server, buf)
	if rerr != nil {
		t.Fatalf("读回数据失败（读到 %d 字节）: %v", n, rerr)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("数据不一致：收到 %d 字节，期望 %d", n, len(payload))
	}
	if werr := <-writeDone; werr != nil {
		t.Fatalf("Write: %v", werr)
	}

	// 反向：B → A
	reply := []byte("pong")
	if _, err := server.Write(reply); err != nil {
		t.Fatalf("反向 Write: %v", err)
	}
	rbuf := make([]byte, len(reply))
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(client, rbuf); err != nil {
		t.Fatalf("反向读失败: %v", err)
	}
	if !bytes.Equal(rbuf, reply) {
		t.Fatalf("反向数据不一致: %q", rbuf)
	}
}

// 关流不能截断尾巴：对端"写完立刻关"是隧道里最常见的模式（HTTP 响应发完就关），
// 如果 close 到达时把接收队列里剩下的数据直接丢掉，浏览器就会看到半截响应
// （ERR_RESPONSE_HEADERS_TRUNCATED：连响应头的结束空行都被吃掉）。
func TestMuxCloseDoesNotTruncateTail(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	muxA := NewMux(c1, "tcp", 60)
	defer muxA.Close()
	muxB := NewMux(c2, "tcp", 60)
	defer muxB.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := muxB.Accept(); err == nil {
			accepted <- conn
		}
	}()

	client, err := muxA.NewConn()
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}

	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("Accept 超时")
	}

	// 一个"没有 body、立刻关连接"的 403：结尾的 \r\n0\r\n\r\n 必须原样到达
	payload := []byte("HTTP/1.1 403 Forbidden\r\nConnection: Keep-Alive\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = client.Close() // 关键：写完立刻关

	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, rerr := io.ReadAll(server)
	if rerr != nil {
		t.Fatalf("读回失败（已读 %d 字节）: %v", len(got), rerr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("关流截断了尾巴：收到 %d 字节 %q，期望 %d 字节 %q", len(got), got, len(payload), payload)
	}
}
