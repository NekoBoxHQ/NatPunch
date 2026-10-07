package goroutine

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/astaxie/beego/logs"
	"github.com/panjf2000/ants/v2"
)

// AuthPageHTML 返回「IP 白名单授权页」的 HTML，由**服务端**在启动时注入
// （见 server/proxy/authpage.go）。
//
// 为什么不直接 import web：这段逻辑只会在服务端的转发路径上走到 ——
// 调用方 lib/conn.CopyWaitGroup 由 server/proxy/base.go 传入 task，
// 而客户端 client/client.go 恒传 nil（下面的 task != nil 判断就把它挡掉了）。
// 直接 import 会让**客户端二进制**把整个面板的 web/static + web/views（约 3.6MB
// 的 jQuery / bootstrap / echarts / 字体）一起编进去，客户端一个字节都用不到。
// 注入之后客户端不再依赖 web 包，二进制小一圈，升级传输也快。
var AuthPageHTML func() string

type connGroup struct {
	src    io.ReadWriteCloser
	dst    io.ReadWriteCloser
	wg     *sync.WaitGroup
	n      *int64
	flow   *file.Flow
	task   *file.Tunnel
	host   *file.Host
	remote string
	dir    int
}

// 拷贝方向常量（流量记账方向语义）：
//
//	DirMuxToOutside：隧道(mux) -> 公网侧 —— 内网数据返回公网 = 出口流量(ExportFlow)
//	DirOutsideToMux：公网侧 -> 隧道(mux) —— 公网请求进入内网 = 入口流量(InletFlow)
const (
	DirMuxToOutside = 1
	DirOutsideToMux = 2
)

//func newConnGroup(dst, src io.ReadWriteCloser, wg *sync.WaitGroup, n *int64) connGroup {
//	return connGroup{
//		src: src,
//		dst: dst,
//		wg:  wg,
//		n:   n,
//	}
//}

func newConnGroup(dst, src io.ReadWriteCloser, wg *sync.WaitGroup, n *int64, flow *file.Flow, task *file.Tunnel, host *file.Host, remote string, dir int) connGroup {
	return connGroup{
		src:    src,
		dst:    dst,
		wg:     wg,
		n:      n,
		flow:   flow,
		task:   task,
		host:   host,
		remote: remote,
		dir:    dir,
	}
}

func CopyBuffer(dst io.Writer, src io.Reader, flow *file.Flow, task *file.Tunnel, host *file.Host, remote string, dir int) (err error) {
	buf := common.CopyBuff.Get()
	defer common.CopyBuff.Put(buf)
	for {
		if len(buf) <= 0 {
			break
		}
		nr, er := src.Read(buf)

		if task != nil {
			if task.Client.IpWhite && task.Client.IpWhitePass != "" {

				// IpWhiteList 会被授权成功路径并发 append，这里在锁下取快照再用
				task.Client.RLock()
				whiteList := task.Client.IpWhiteList
				task.Client.RUnlock()
				if common.IsAuthIp(remote, task.Client.VerifyKey, whiteList) {
					ip := common.GetIpByAddr(remote)
					var jsonBytes []byte

					authHtml := ""
					if AuthPageHTML != nil {
						authHtml = AuthPageHTML()
					}
					authHtml = strings.ReplaceAll(authHtml, "${ip}", ip)

					fullRequest := string(buf[0:nr])
					// 获取HTTP请求的第一行
					lines := strings.Split(fullRequest, "\r\n")
					if len(lines) == 0 {
						lines = strings.Split(fullRequest, "\n")
					}
					firstLine := lines[0]

					// 优先处理客户端直接访问的 POST /authIp 请求，直接响应给客户端，不经隧道转发
					if strings.HasPrefix(firstLine, "POST /authIp") {
						pass := ""
						parts := strings.Split(firstLine, " ")
						if len(parts) > 1 {
							path := parts[1]
							if strings.Contains(path, "/authIp?pass=") {
								pass = strings.ReplaceAll(path, "/authIp?pass=", "")
							}
						}
						if pass == task.Client.IpWhitePass {
							// copy-on-write：不要原地改已经发布出去的切片（读者正持有它）
							task.Client.Lock()
							task.Client.IpWhiteList = append(append([]string(nil), task.Client.IpWhiteList...), ip)
							task.Client.Unlock()
							file.GetDb().UpdateClient(task.Client)
							logs.Info("客户端IP白名单认证授权成功:vkey [%s] ip [%s] password [%s]", task.Client.VerifyKey, ip, pass)
							jsonBytes, err = json.Marshal(map[string]interface{}{"success": true, "message": "授权成功"})
						} else {
							logs.Error("客户端IP白名单认证授权密码错误:vkey [%s] ip [%s] password [%s]", task.Client.VerifyKey, ip, pass)
							jsonBytes, err = json.Marshal(map[string]interface{}{"success": false, "message": "密码错误"})
						}
						response := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(jsonBytes), jsonBytes)
						// 如果 src 是真实的客户端连接（net.Conn），直接写回客户端并关闭连接，避免走隧道转发
						if connSrc, ok := src.(net.Conn); ok {
							connSrc.Write([]byte(response))
							connSrc.Close()
						} else {
							dst.Write([]byte(response))
						}
						return
					}

					// 非授权IP，返回授权页面（同样优先返回给客户端）
					response := fmt.Sprintf("HTTP/1.1 401 Unauthorized\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(authHtml), authHtml)
					if connSrc, ok := src.(net.Conn); ok {
						connSrc.Write([]byte(response))
						connSrc.Close()
					} else {
						dst.Write([]byte(response))
					}
					return
				}
			}
		}

		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				//written += int64(nw)
				if flow != nil {
					// 按方向记账：隧道->公网=出口，公网->隧道=入口
					if dir == DirMuxToOutside {
						flow.Add(0, int64(nw))
					} else {
						flow.Add(int64(nw), 0)
					}
					// <<20 = 1024 * 1024
					if flow.FlowLimit > 0 && (flow.FlowLimit<<20) < (flow.ExportFlow+flow.InletFlow) {
						// task 可能为 nil（httpProxy/socks5/transport 路径），不可直接解引用（F1-6）
						if task != nil {
							logs.Error("隧道[%s]流量已经超出", task.Client.VerifyKey)
						} else {
							logs.Error("流量已经超出")
						}
						break
					}
				}
				if task != nil && task.Flow != nil && task.Flow != flow {
					if dir == DirMuxToOutside {
						task.Flow.Add(0, int64(nw))
					} else {
						task.Flow.Add(int64(nw), 0)
					}
				}
				if host != nil && host.Flow != nil && host.Flow != flow {
					if dir == DirMuxToOutside {
						host.Flow.Add(0, int64(nw))
					} else {
						host.Flow.Add(int64(nw), 0)
					}
				}
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			err = er
			break
		}
	}
	return err
}

func copyConnGroup(group interface{}) {
	//logs.Info("copyConnGroup.........")
	cg, ok := group.(connGroup)
	if !ok {
		return
	}

	var err error
	err = CopyBuffer(cg.dst, cg.src, cg.flow, cg.task, cg.host, cg.remote, cg.dir)
	if err != nil {
		cg.src.Close()
		cg.dst.Close()
		//logs.Warn("close natpunch-client by copy from natpunch", err, c.connId)
	}

	//if conns.flow != nil {
	//	conns.flow.Add(in, out)
	//}
	cg.wg.Done()
}

type Conns struct {
	conn1 io.ReadWriteCloser // mux connection
	conn2 net.Conn           // outside connection
	flow  *file.Flow
	wg    *sync.WaitGroup
	task  *file.Tunnel
	host  *file.Host
}

func NewConns(c1 io.ReadWriteCloser, c2 net.Conn, flow *file.Flow, wg *sync.WaitGroup, task *file.Tunnel, host *file.Host) Conns {
	return Conns{
		conn1: c1,
		conn2: c2,
		flow:  flow,
		wg:    wg,
		task:  task,
		host:  host,
	}
}

func copyConns(group interface{}) {
	//logs.Info("copyConns.........")
	conns := group.(Conns)
	wg := new(sync.WaitGroup)
	wg.Add(2)
	var in, out int64
	remoteAddr := conns.conn2.RemoteAddr().String()
	// mux to outside : outgoing —— 隧道->公网 = 出口流量
	_ = connCopyPool.Invoke(newConnGroup(conns.conn1, conns.conn2, wg, &in, conns.flow, conns.task, conns.host, remoteAddr, DirMuxToOutside))
	// outside to mux : incoming —— 公网->隧道 = 入口流量
	_ = connCopyPool.Invoke(newConnGroup(conns.conn2, conns.conn1, wg, &out, conns.flow, conns.task, conns.host, remoteAddr, DirOutsideToMux))
	wg.Wait()
	//if conns.flow != nil {
	//	conns.flow.Add(in, out)
	//}
	conns.wg.Done()
}

var connCopyPool, _ = ants.NewPoolWithFunc(200000, copyConnGroup, ants.WithNonblocking(false))
var CopyConnsPool, _ = ants.NewPoolWithFunc(100000, copyConns, ants.WithNonblocking(false))
