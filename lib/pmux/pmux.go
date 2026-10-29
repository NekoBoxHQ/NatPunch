// This module is used for port reuse
// Distinguish client, web manager , HTTP and HTTPS according to the difference of protocol
package pmux

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/astaxie/beego/logs"
	"github.com/pkg/errors"
)

const (
	// maxHTTPHeaderBytes：复用端口上 HTTP 头的总长上限。
	// 这里是公网可达的，ReadLine 本身没有上限 —— 不回换行的长行、或者几万行
	// 小 header，都能在 10 秒窗口里堆出数百 MB 内存。
	maxHTTPHeaderBytes = 8 << 10

	HTTP_GET        = 716984
	HTTP_POST       = 807983
	HTTP_HEAD       = 726965
	HTTP_PUT        = 808585
	HTTP_DELETE     = 686976
	HTTP_CONNECT    = 677978
	HTTP_OPTIONS    = 798084
	HTTP_TRACE      = 848265
	CLIENT          = 848384
	ACCEPT_TIME_OUT = 10
)

type PortMux struct {
	net.Listener
	port        int
	started     bool
	isClose     bool
	done        chan struct{}  // 关闭时触发 process() 退出，避免 send on closed channel
	wg          sync.WaitGroup // 跟踪 in-flight process()，保证 Close() 关闭 conn channel 前全部退出
	managerHost string
	clientConn  chan *PortConn
	httpConn    chan *PortConn
	httpsConn   chan *PortConn
	managerConn chan *PortConn
}

func NewPortMux(port int, managerHost string) *PortMux {
	pMux := &PortMux{
		managerHost: managerHost,
		port:        port,
		done:        make(chan struct{}),
		clientConn:  make(chan *PortConn),
		httpConn:    make(chan *PortConn),
		httpsConn:   make(chan *PortConn),
		managerConn: make(chan *PortConn),
	}
	pMux.Start()
	return pMux
}

func (pMux *PortMux) Start() error {
	if pMux.started {
		return errors.New("port mux already started")
	}
	pMux.started = true
	// Port multiplexing is based on TCP only
	tcpAddr, err := net.ResolveTCPAddr("tcp", "0.0.0.0:"+strconv.Itoa(pMux.port))
	if err != nil {
		return err
	}
	pMux.Listener, err = net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		// 失败路径显式复位为 nil：net.ListenTCP 返回的 nil 指针装入 net.Listener 接口后
		// `== nil` 为 false（经典 Go 陷阱），不清零会导致 Getter 的 nil 守卫失效、Addr() panic（阶段三 #15）
		pMux.Listener = nil
		return err
	}
	ln := pMux.Listener // 局部捕获：accept 循环引用局部值，防止 Listener 字段被外部清零导致 nil 解引用
	pMux.wg.Add(1)
	go func() {
		defer pMux.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				logs.Warn(err)
				return
			}
			pMux.wg.Add(1)
			go func(c net.Conn) {
				defer pMux.wg.Done()
				pMux.process(c)
			}(conn)
		}
	}()
	return nil
}

func (pMux *PortMux) process(conn net.Conn) {
	// 设置读超时，防止客户端不发数据导致 goroutine 永久阻塞，造成 wg.Wait() 死锁
	readTimeout := ACCEPT_TIME_OUT * time.Second
	conn.SetReadDeadline(time.Now().Add(readTimeout))

	// Recognition according to different signs
	// read 3 byte
	buf := make([]byte, 3)
	if n, err := io.ReadFull(conn, buf); err != nil || n != 3 {
		conn.Close()
		return
	}
	var ch chan *PortConn
	var rs []byte
	var buffer bytes.Buffer
	var readMore = false
	switch common.BytesToNum(buf) {
	case HTTP_CONNECT, HTTP_DELETE, HTTP_GET, HTTP_HEAD, HTTP_OPTIONS, HTTP_POST, HTTP_PUT, HTTP_TRACE: //http and manager
		conn.SetReadDeadline(time.Now().Add(readTimeout)) // 刷新超时，给 HTTP 头读取完整时间窗口
		buffer.Reset()
		r := bufio.NewReader(conn)
		buffer.Write(buf)
		for {
			b, _, err := r.ReadLine()
			if err != nil {
				logs.Warn("read line error", err.Error())
				conn.Close()
				break
			}
			buffer.Write(b)
			buffer.Write([]byte("\r\n"))
			// 头部总长上限：公网口，ReadLine 无上限，一条长行（或几万行小头）
			// 在 10 秒窗口里能堆出几百 MB → OOM。正常 HTTP 头远小于 8KB。
			if buffer.Len() > maxHTTPHeaderBytes {
				logs.Warn("pmux: http header too large from %s, drop", conn.RemoteAddr().String())
				conn.Close()
				break
			}
			// Host 头按 RFC 大小写不敏感、冒号前后可带空白。
			// 原来只认 "Host:" / "host:" 两种拼写：写成 "HOST:" 或 "Host :" 时
			// 这一行永远匹配不上 → 循环一路读到 8KB 上限才丢连接。
			key, val, isHeader := cutHeaderLine(string(b))
			if isHeader && strings.EqualFold(key, "host") {
				str := strings.TrimSpace(val)
				// Determine whether it is the same as the manager domain name
				if common.GetIpByAddr(str) == pMux.managerHost {
					ch = pMux.managerConn
				} else {
					ch = pMux.httpConn
				}
				b, _ := r.Peek(r.Buffered())
				buffer.Write(b)
				rs = buffer.Bytes()
				break
			}
		}
	case CLIENT: // client connection
		ch = pMux.clientConn
	default: // https
		readMore = true
		ch = pMux.httpsConn
	}
	// 清除读超时，后续由 PortConn 的消费者自行管理超时
	conn.SetReadDeadline(time.Time{})
	if len(rs) == 0 {
		rs = buf
	}
	timer := time.NewTimer(readTimeout)
	defer timer.Stop()
	select {
	case <-pMux.done:
		conn.Close()
	case <-timer.C:
		conn.Close()
	case ch <- newPortConn(conn, rs, readMore):
	}
}

func (pMux *PortMux) Close() error {
	if pMux.isClose {
		return errors.New("the port pmux has closed")
	}
	pMux.isClose = true
	// Listener 可能为 (*TCPListener)(nil) 接口（Start 失败路径），直接 Close() 会 nil 解引用（阶段三 #15）
	if pMux.Listener != nil {
		_ = pMux.Listener.Close() // 停止接收新连接，触发 accept 协程退出
	}
	close(pMux.done) // 唤醒 in-flight process()
	pMux.wg.Wait()   // 等待所有 process() 退出后再 close conn channel
	close(pMux.clientConn)
	close(pMux.httpsConn)
	close(pMux.httpConn)
	close(pMux.managerConn)
	return nil
}

// GetXxxListener：Listener 未就绪（Start 失败/未调用）时返回 nil，
// 由调用方检查并报错，避免 nil 解引用（阶段三 #15 配套）
func (pMux *PortMux) GetClientListener() net.Listener {
	if pMux.Listener == nil {
		return nil
	}
	return NewPortListener(pMux.clientConn, pMux.Listener.Addr())
}

func (pMux *PortMux) GetHttpListener() net.Listener {
	if pMux.Listener == nil {
		return nil
	}
	return NewPortListener(pMux.httpConn, pMux.Listener.Addr())
}

func (pMux *PortMux) GetHttpsListener() net.Listener {
	if pMux.Listener == nil {
		return nil
	}
	return NewPortListener(pMux.httpsConn, pMux.Listener.Addr())
}

func (pMux *PortMux) GetManagerListener() net.Listener {
	if pMux.Listener == nil {
		return nil
	}
	return NewPortListener(pMux.managerConn, pMux.Listener.Addr())
}

// cutHeaderLine 把一行 HTTP 头拆成 key/value（按第一个冒号）。
// 返回 isHeader=false 表示这一行不是头（请求行、空行等）。
func cutHeaderLine(line string) (key, val string, isHeader bool) {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), line[i+1:], true
}
