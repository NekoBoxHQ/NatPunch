package proxy

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/NekoBoxHQ/NatPunch/bridge"
	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/server/connection"
	"github.com/NekoBoxHQ/NatPunch/web"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"time"
)

type TunnelModeServer struct {
	BaseServer
	process  process
	listener net.Listener
}

// tcp|http|host
func NewTunnelModeServer(process process, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	s := new(TunnelModeServer)
	s.bridge = bridge
	s.process = process
	s.task = task
	return s
}

// 开始
func (s *TunnelModeServer) Start() error {
	addr := s.task.ServerIp + ":" + strconv.Itoa(s.task.Port)
	var l net.Listener
	var err error
	if s.task.Https {
		// 隧道端口启用 TLS：借用面板证书，服务端终止 TLS 后转发内网（仅 TCP，UDP 不受影响）
		certFile := beego.AppConfig.String("web_cert_file")
		keyFile := beego.AppConfig.String("web_key_file")
		if certFile == "" || keyFile == "" {
			return errors.New("https tunnel: web_cert_file/web_key_file not configured")
		}
		cert, e := tls.LoadX509KeyPair(certFile, keyFile)
		if e != nil {
			return e
		}
		l, err = tls.Listen("tcp", addr, &tls.Config{Certificates: []tls.Certificate{cert}})
	} else {
		l, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return err
	}
	s.listener = l
	conn.Accept(l, func(c net.Conn) {
		if err := s.CheckFlowAndConnNum(s.task.Client); err != nil {
			logs.Warn("client id %d, task id %d,error %s, when tcp connection", s.task.Client.Id, s.task.Id, err.Error())
			c.Close()
			return
		}
		logs.Trace("new tcp connection,local port %d,client %d,remote address %s", s.task.Port, s.task.Client.Id, c.RemoteAddr())
		s.process(conn.NewConn(c), s)
		s.task.Client.AddConn()
	})
	return nil
}

// close
func (s *TunnelModeServer) Close() error {
	// 建隧道后立即删除时 Start 尚未执行、listener 为 nil（阶段三 G4）
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

// web管理方式
type WebServer struct {
	BaseServer
}

// 开始
func (s *WebServer) Start() error {
	p, _ := beego.AppConfig.Int("web_port")
	if p == 0 {
		stop := make(chan struct{})
		<-stop
	}
	beego.BConfig.WebConfig.Session.SessionOn = true
	// Serve management UI from embedded assets only (no disk web/ directory).
	web.InitBeegoAssets()
	var (
		l   net.Listener
		err error
	)
	if l, err = connection.GetWebManagerListener(); err == nil {
		beego.InitBeforeHTTPRun()
		secure := beego.AppConfig.String("web_open_ssl") == "true"
		if secure {
			keyPath := beego.AppConfig.String("web_key_file")
			certPath := beego.AppConfig.String("web_cert_file")
			err = http.ServeTLS(l, newSameSiteHandler(beego.BeeApp.Handlers, true), certPath, keyPath)
		} else {
			err = http.Serve(l, newSameSiteHandler(beego.BeeApp.Handlers, false))
		}
	} else {
		logs.Error(err)
	}
	return err
}

func (s *WebServer) Close() error {
	return nil
}

// new
func NewWebServer(bridge *bridge.Bridge) *WebServer {
	s := new(WebServer)
	s.bridge = bridge
	return s
}

type process func(c *conn.Conn, s *TunnelModeServer) error

// tcp proxy
func ProcessTunnel(c *conn.Conn, s *TunnelModeServer) error {
	targetAddr, err := s.task.Target.GetRandomTarget()
	if err != nil {
		c.Close()
		logs.Warn("tcp port %d ,client id %d,task id %d connect error %s", s.task.Port, s.task.Client.Id, s.task.Id, err.Error())
		return err
	}
	return s.DealClient(c, s.task.Client, targetAddr, nil, common.CONN_TCP, nil, s.task.Target.LocalProxy, s.task, nil)
}

// http proxy
func ProcessHttp(c *conn.Conn, s *TunnelModeServer) error {
	// 握手阶段（读 HTTP 请求头）必须限时：公网侧一个字节都不发的连接，
	// 原来会一直占着 goroutine + 32KB 缓冲，还白占一个 MaxConn 名额。
	// 放在调用点而不是 GetHost 里 —— GetHost 的调用方（含测试）可能自己设了
	// 更短的 deadline，不能被覆盖；读完这里立刻清掉，不影响后续长连接/大上传。
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, addr, rb, err, r := c.GetHost()
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		logs.Info(err)
		return err
	}
	// Authenticate before establishing CONNECT tunnel (fixes auth-after-200 bug).
	// 优先使用隧道级账号（MultiAccount），未配置则回退客户端级账号
	u, p := s.task.Client.Cnf.U, s.task.Client.Cnf.P
	if s.task.MultiAccount != nil && len(s.task.MultiAccount.AccountMap) > 0 {
		for k, v := range s.task.MultiAccount.AccountMap {
			u, p = k, v
			break
		}
	}
	if err := s.proxyAuth(r, c, u, p); err != nil {
		logs.Warn("http proxy auth failed, client %d, remote %s: %v", s.task.Client.Id, c.Conn.RemoteAddr(), err)
		return err
	}
	if r.Method == "CONNECT" {
		if _, err := c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
			c.Close()
			return err
		}
		rb = nil
	}
	logs.Info("http proxy request, method %s, host %s, client %d, remote %s", r.Method, addr, s.task.Client.Id, c.Conn.RemoteAddr())
	return s.DealClient(c, s.task.Client, addr, rb, common.CONN_TCP, nil, s.task.Target.LocalProxy, s.task, nil)
}
