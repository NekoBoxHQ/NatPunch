package pmux

import (
	"net"
	"testing"
	"time"

	"github.com/astaxie/beego/logs"
)

func TestPortMux_Close(t *testing.T) {
	logs.Reset()
	logs.EnableFuncCallDepth(true)
	logs.SetLogFuncCallDepth(3)

	// 动态取空闲端口，避免固定端口被环境占用导致 Start 失败（阶段三 #15 改 Start 返回 error 后暴露）
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	pMux := NewPortMux(port, "Ds")
	time.Sleep(time.Second * 3)
	go func() {
		l := pMux.GetHttpListener()
		conn, err := l.Accept()
		logs.Warn(conn, err)
	}()
	go func() {
		l := pMux.GetHttpListener()
		conn, err := l.Accept()
		logs.Warn(conn, err)
	}()
	go func() {
		l := pMux.GetHttpListener()
		conn, err := l.Accept()
		logs.Warn(conn, err)
	}()
	// 无连接到来时 Accept 会永久阻塞：延迟 Close 触发所有 Accept 返回（原测试依赖 os.Exit 逃逸，阶段三 #15 后必须显式关闭）
	time.AfterFunc(2*time.Second, func() {
		_ = pMux.Close()
	})
	l2 := pMux.GetHttpListener()
	conn, err := l2.Accept()
	logs.Warn(conn, err)
}
