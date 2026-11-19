package conn

import (
	"net"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/astaxie/beego/logs"
	"github.com/xtaci/kcp-go"
)

// acceptLoop 接受连接并交给 handle 处理，直到 listener 被关闭。
//
// 只有「listener 已关闭」才返回；其余错误（EMFILE/ENFILE、临时网络错误）按
// common.AcceptBackoff 退避后继续。sleep 作为参数注入，便于测试断言退避而不真的等待。
func acceptLoop(l net.Listener, handle func(net.Conn), sleep func(time.Duration)) {
	failures := 0
	for {
		c, err := l.Accept()
		if err != nil {
			if common.IsPermanentAcceptErr(err) {
				return
			}
			failures++
			logs.Warn("accept error (连续第 %d 次): %v", failures, err)
			sleep(common.AcceptBackoff(failures))
			continue
		}
		failures = 0
		if c == nil {
			logs.Warn("nil connection")
			return
		}
		go handle(c)
	}
}

func NewTcpListenerAndProcess(addr string, f func(c net.Conn), listener *net.Listener) error {
	var err error
	*listener, err = net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	Accept(*listener, f)
	return nil
}

func NewKcpListenerAndProcess(addr string, f func(c net.Conn)) error {
	kcpListener, err := kcp.ListenWithOptions(addr, nil, 150, 3)
	if err != nil {
		logs.Error(err)
		return err
	}
	failures := 0
	for {
		c, err := kcpListener.AcceptKCP()
		if err != nil {
			if common.IsPermanentAcceptErr(err) {
				return nil
			}
			// KCP 的 accept 错误同样是「临时错误不能退出、也不能空转」，
			// 与 TCP 路径保持一致的退避策略。
			failures++
			logs.Warn("kcp accept error (连续第 %d 次): %v", failures, err)
			time.Sleep(common.AcceptBackoff(failures))
			continue
		}
		failures = 0
		SetUdpSession(c)
		go f(c)
	}
}

func Accept(l net.Listener, f func(c net.Conn)) {
	acceptLoop(l, f, time.Sleep)
}
