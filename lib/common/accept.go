package common

import (
	"errors"
	"net"
	"strings"
	"time"
)

// AcceptBackoff 返回第 n 次连续 accept 失败后的退避时长：
// 100ms 起、每次翻倍、封顶 1s。
//
// 目的：把「临时错误 → 立即重试」变成有节制的重试。EMFILE/ENFILE 这类 fd 耗尽
// 错误在旧实现里会触发零延迟死循环 —— CPU 打满、日志刷屏，期间新连接一律进不来。
//
// 放在 common 是因为 lib/conn 与 lib/pmux 都需要它，而两者互相引用会形成循环。
func AcceptBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := 100 * time.Millisecond
	for i := 1; i < failures && d < time.Second; i++ {
		d *= 2
	}
	if d > time.Second {
		d = time.Second
	}
	return d
}

// IsPermanentAcceptErr 判断 accept 错误是否意味着 listener 已关闭、循环应当退出。
//
// 除标准库的 net.ErrClosed 外，还兼容两条历史错误串：mux 关闭时的
// "the mux has closed"、pmux 端口监听关闭时的 "the listener has closed"。
// 它们都不是 net.ErrClosed，早期实现靠字符串匹配识别；漏掉任意一条，
// 关闭后的 accept 循环都会变成永久自旋。
func IsPermanentAcceptErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "the mux has closed") ||
		strings.Contains(msg, "the listener has closed")
}
