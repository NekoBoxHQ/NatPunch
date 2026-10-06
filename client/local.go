package client

import (
	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
	"errors"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/config"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/server/proxy"
	"github.com/astaxie/beego/logs"
	"github.com/xtaci/kcp-go"
)

var (
	LocalServer   []*net.TCPListener
	udpConn       net.Conn
	muxSession    *natpunch_mux.Mux
	fileServer    []*http.Server
	p2pNetBridge  *p2pBridge
	lock          sync.RWMutex
	udpConnStatus bool
	// 本轮连接启动的 UDP 监控协程的停止通道（每次重连重建一套，由 CloseLocalServer 统一收掉）
	udpMonitorMu    sync.Mutex
	udpMonitorStops []chan struct{}
)

// startUdpMonitor 启动绑定到本轮连接的 UDP 监控协程，并登记停止通道。
//
// handleUdpMonitor 原本是永不退出的 for-select（只等 ticker），而客户端每次重连
// 都会重新执行 StartLocalServer —— 于是每重连一次就多一个常驻 goroutine 和一个 ticker，
// 新旧实例还会一起抢 udpConn / udpConnStatus 这些全局变量。现在由一个停止通道收口，
// CloseLocalServer（每次重连前都会调用）负责把它们全部结束。
func startUdpMonitor(config *config.CommonConfig, l *config.LocalServer) {
	stop := make(chan struct{})
	udpMonitorMu.Lock()
	udpMonitorStops = append(udpMonitorStops, stop)
	udpMonitorMu.Unlock()
	go handleUdpMonitor(config, l, stop)
}

// stopUdpMonitors 结束当前登记的全部 UDP 监控协程
func stopUdpMonitors() {
	udpMonitorMu.Lock()
	stops := udpMonitorStops
	udpMonitorStops = nil
	udpMonitorMu.Unlock()
	for _, ch := range stops {
		close(ch)
	}
}

type p2pBridge struct {
}

func (p2pBridge *p2pBridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error) {
	for i := 0; muxSession == nil; i++ {
		if i >= 20 {
			err = errors.New("p2pBridge:too many times to get muxSession")
			logs.Error(err)
			return
		}
		runtime.Gosched() // waiting for another goroutine establish the mux connection
	}
	nowConn, err := muxSession.NewConn()
	if err != nil {
		udpConn = nil
		return nil, err
	}
	if _, err := conn.NewConn(nowConn).SendInfo(link, ""); err != nil {
		udpConnStatus = false
		return nil, err
	}
	return nowConn, nil
}

func CloseLocalServer() {
	stopUdpMonitors()
	for _, v := range LocalServer {
		v.Close()
	}
	for _, v := range fileServer {
		v.Close()
	}
}

func startLocalFileServer(config *config.CommonConfig, t *file.Tunnel, vkey string) {
	remoteConn, err := NewConn(config.Tp, vkey, config.Server, common.WORK_FILE, config.ProxyUrl)
	if err != nil {
		logs.Error("Local connection server failed ", err.Error())
		return
	}
	srv := &http.Server{
		Handler: http.StripPrefix(t.StripPre, http.FileServer(http.Dir(t.LocalPath))),
	}
	logs.Info("start local file system, local path %s, strip prefix %s ,remote port %s ", t.LocalPath, t.StripPre, t.Ports)
	fileServer = append(fileServer, srv)
	listener := natpunch_mux.NewMux(remoteConn.Conn, common.CONN_TCP, config.DisconnectTime)
	logs.Error(srv.Serve(listener))
}

func StartLocalServer(l *config.LocalServer, config *config.CommonConfig) error {
	if l.Type != "secret" {
		startUdpMonitor(config, l)
	}
	task := &file.Tunnel{
		Port:     l.Port,
		ServerIp: "0.0.0.0",
		Status:   true,
		Client: &file.Client{
			Cnf: &file.Config{
				U:        "",
				P:        "",
				Compress: config.Client.Cnf.Compress,
			},
			Status:    true,
			RateLimit: 0,
			Flow:      &file.Flow{},
		},
		Flow:   &file.Flow{},
		Target: &file.Target{},
	}
	switch l.Type {
	case "p2ps":
		logs.Info("successful start-up of local socks5 monitoring, port", l.Port)
		return proxy.NewSock5ModeServer(p2pNetBridge, task).Start()
	case "p2pt":
		logs.Info("successful start-up of local tcp trans monitoring, port", l.Port)
		return proxy.NewTunnelModeServer(proxy.HandleTrans, p2pNetBridge, task).Start()
	case "p2p", "secret":
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: l.Port})
		if err != nil {
			logs.Error("local listener startup failed port %d, error %s", l.Port, err.Error())
			return err
		}
		LocalServer = append(LocalServer, listener)
		logs.Info("successful start-up of local tcp monitoring, port", l.Port)
		conn.Accept(listener, func(c net.Conn) {
			logs.Trace("new %s connection", l.Type)
			if l.Type == "secret" {
				handleSecret(c, config, l)
			} else if l.Type == "p2p" {
				handleP2PVisitor(c, config, l)
			}
		})
	}
	return nil
}

func handleUdpMonitor(config *config.CommonConfig, l *config.LocalServer, stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second * 1)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !udpConnStatus {
				udpConn = nil
				tmpConn, err := common.GetLocalUdpAddr()
				if err != nil {
					logs.Error(err)
					return
				}
				for i := 0; i < 10; i++ {
					logs.Notice("try to connect to the server", i+1)
					newUdpConn(tmpConn.LocalAddr().String(), config, l)
					if udpConn != nil {
						udpConnStatus = true
						break
					}
				}
			}
		}
	}
}

func handleSecret(localTcpConn net.Conn, config *config.CommonConfig, l *config.LocalServer) {
	remoteConn, err := NewConn(config.Tp, config.VKey, config.Server, common.WORK_SECRET, config.ProxyUrl)
	if err != nil {
		logs.Error("Local connection server failed ", err.Error())
		return
	}
	if _, err := remoteConn.Write([]byte(crypt.Md5(l.Password))); err != nil {
		logs.Error("Local connection server failed ", err.Error())
		return
	}
	conn.CopyWaitGroup(remoteConn.Conn, localTcpConn, false, false, nil, nil, false, nil, nil, nil)
}

func handleP2PVisitor(localTcpConn net.Conn, config *config.CommonConfig, l *config.LocalServer) {
	if udpConn == nil {
		logs.Notice("new conn, P2P can not penetrate successfully, traffic will be transferred through the server")
		handleSecret(localTcpConn, config, l)
		return
	}
	logs.Trace("start trying to connect with the server")
	//TODO just support compress now because there is not tls file in client packages
	link := conn.NewLink(common.CONN_TCP, l.Target, false, config.Client.Cnf.Compress, localTcpConn.LocalAddr().String(), false, "")
	if target, err := p2pNetBridge.SendLinkInfo(0, link, nil); err != nil {
		logs.Error(err)
		udpConnStatus = false
		return
	} else {
		conn.CopyWaitGroup(target, localTcpConn, false, config.Client.Cnf.Compress, nil, nil, false, nil, nil, nil)
	}
}

func newUdpConn(localAddr string, config *config.CommonConfig, l *config.LocalServer) {
	lock.Lock()
	defer lock.Unlock()
	remoteConn, err := NewConn(config.Tp, config.VKey, config.Server, common.WORK_P2P, config.ProxyUrl)
	if err != nil {
		logs.Error("Local connection server failed ", err.Error())
		return
	}
	if _, err := remoteConn.Write([]byte(crypt.Md5(l.Password))); err != nil {
		logs.Error("Local connection server failed ", err.Error())
		return
	}
	var rAddr []byte
	//读取服务端地址、密钥 继续做处理
	if rAddr, err = remoteConn.GetShortLenContent(); err != nil {
		logs.Error(err)
		return
	}
	var localConn net.PacketConn
	var remoteAddress string
	if remoteAddress, localConn, err = handleP2PUdp(localAddr, string(rAddr), crypt.Md5(l.Password), common.WORK_P2P_VISITOR); err != nil {
		logs.Error(err)
		return
	}
	udpTunnel, err := kcp.NewConn(remoteAddress, nil, 150, 3, localConn)
	if err != nil || udpTunnel == nil {
		logs.Warn(err)
		return
	}
	logs.Trace("successful create a connection with server", remoteAddress)
	conn.SetUdpSession(udpTunnel)
	udpConn = udpTunnel
	muxSession = natpunch_mux.NewMux(udpConn, "kcp", config.DisconnectTime)
	p2pNetBridge = &p2pBridge{}
}
