package proxy

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/NekoBoxHQ/NatPunch/lib/cache"
	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/astaxie/beego/logs"
	"github.com/pkg/errors"
)

type HttpsServer struct {
	httpServer
	listener         net.Listener
	httpsListenerMap sync.Map
	hostIdCertMap    sync.Map
}

func NewHttpsServer(l net.Listener, bridge NetBridge, useCache bool, cacheLen int) *HttpsServer {
	https := &HttpsServer{listener: l}
	https.bridge = bridge
	https.useCache = useCache
	if useCache {
		https.cache = cache.New(cacheLen)
	}
	return https
}

// start https server
func (https *HttpsServer) Start() error {

	conn.Accept(https.listener, func(c net.Conn) {
		serverName, rb := GetServerNameFromClientHello(c)
		if serverName == "" {
			serverName = getFallbackServerName()
			logs.Debug("https fallback server name result, remote addr %s, server name %q", c.RemoteAddr().String(), serverName)
		}
		r := buildHttpsRequest(serverName)
		if host, err := file.GetDb().GetInfoByHost(serverName, r); err != nil {
			c.Close()
			logs.Debug("https host lookup failed, server name %q, remote addr %s, error %v", serverName, c.RemoteAddr().String(), err)
			return
		} else {
			if host.CertFilePath == "" || host.KeyFilePath == "" {
				logs.Debug("加载客户端本地证书")
				https.handleHttps2(c, serverName, rb, r)
			} else {
				logs.Debug("使用上传证书")

				// 判断是路径还是证书，-----BEGIN 开头的为证书
				if strings.Contains(host.CertFilePath, "-----BEGIN") || strings.Contains(host.KeyFilePath, "-----BEGIN") {
					logs.Debug("通过上传文件加载证书")
					https.cert(host, c, rb, host.CertFilePath, host.KeyFilePath)
				} else {
					logs.Debug("通过路径加载证书")
					if !common.FileExists(host.CertFilePath) || !common.FileExists(host.KeyFilePath) {
						c.Close()
						logs.Error("证书或秘钥文件不存在", host.KeyFilePath, host.CertFilePath)
						return
					}

					cert, err := common.ReadCertFile(host.CertFilePath)
					if err != nil {
						c.Close()
						logs.Error("加载证书失败", err)
						return
					}
					key, err := common.ReadCertFile(host.KeyFilePath)
					if err != nil {
						c.Close()
						logs.Error("加载证书秘钥失败", err)
						return
					}

					https.cert(host, c, rb, string(cert), string(key))

				}

			}
		}
	})
	return nil
}

func getFallbackServerName() string {
	global := file.GetDb().GetGlobal()
	if global == nil {
		return ""
	}
	serverName := strings.TrimSpace(global.ServerUrl)
	if serverName == "" {
		return ""
	}
	if strings.Contains(serverName, "://") {
		if u, err := url.Parse(serverName); err == nil && u.Host != "" {
			serverName = u.Host
		}
	}
	if i := strings.Index(serverName, "/"); i >= 0 {
		serverName = serverName[:i]
	}
	serverName = common.GetIpByAddr(serverName)
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return ""
	}
	ip := net.ParseIP(serverName)
	if ip != nil && ip.IsUnspecified() {
		return ""
	}
	return serverName
}

func (https *HttpsServer) cert(host *file.Host, c net.Conn, rb []byte, certFileUrl string, keyFileUrl string) {
	var l *HttpsListener
	i := 0
	https.hostIdCertMap.Range(func(key, value interface{}) bool {
		i++
		// 如果host Id 不存在，则删除map
		if id, ok := key.(int); ok {
			var err error
			_, err = file.GetDb().GetHostById(id)
			if err != nil {
				// 说明这个host已经不存了，需要释放Listener
				logs.Error(err)
				if oldL, ok := https.httpsListenerMap.Load(value); ok {
					err := oldL.(*HttpsListener).Close()
					if err != nil {
						logs.Error(err)
					}
					https.httpsListenerMap.Delete(value)
					https.hostIdCertMap.Delete(key)
					logs.Debug("Listener 已释放")
				}
			}
		}
		return true
	})

	logs.Debug("当前 Listener 连接数量", i)

	if cert, ok := https.hostIdCertMap.Load(host.Id); ok {
		if cert == certFileUrl {
			// 证书已经存在，直接加载
			if v, ok := https.httpsListenerMap.Load(certFileUrl); ok {
				l = v.(*HttpsListener)
			} else {
				// map miss（防御）：重建 listener，避免 nil 解引用（阶段三 #3）
				l = NewHttpsListener(https.listener)
				https.NewHttps(l, certFileUrl, keyFileUrl)
				https.httpsListenerMap.Store(certFileUrl, l)
			}
		} else {
			// 证书修改过，重新加载证书
			l = NewHttpsListener(https.listener)
			https.NewHttps(l, certFileUrl, keyFileUrl)
			if oldL, ok := https.httpsListenerMap.Load(cert); ok {
				err := oldL.(*HttpsListener).Close()
				if err != nil {
					logs.Error(err)
				}
				https.httpsListenerMap.Delete(cert)
			}
			https.httpsListenerMap.Store(certFileUrl, l)
			https.hostIdCertMap.Store(host.Id, certFileUrl)
		}
	} else {
		// 第一次加载证书
		l = NewHttpsListener(https.listener)
		https.NewHttps(l, certFileUrl, keyFileUrl)
		https.httpsListenerMap.Store(certFileUrl, l)
		https.hostIdCertMap.Store(host.Id, certFileUrl)
	}

	acceptConn := conn.NewConn(c)
	acceptConn.Rb = rb
	select {
	case l.acceptConn <- acceptConn:
	case <-l.closeCh:
		// listener 已关闭：丢弃连接，避免向已关闭的 channel 发送 panic（阶段三 #3）
		c.Close()
	}
}

// handle the https which is just proxy to other client
func (https *HttpsServer) handleHttps2(c net.Conn, hostName string, rb []byte, r *http.Request) {
	var targetAddr string
	var host *file.Host
	var err error
	if host, err = file.GetDb().GetInfoByHost(hostName, r); err != nil {
		c.Close()
		logs.Debug("the url %s can't be parsed!", hostName)
		return
	}
	if err := https.CheckFlowAndConnNum(host.Client); err != nil {
		logs.Debug("client id %d, host id %d, error %s, when https connection", host.Client.Id, host.Id, err.Error())
		c.Close()
		return
	}
	defer host.Client.AddConn()
	if err = https.auth(r, conn.NewConn(c), host.Client.Cnf.U, host.Client.Cnf.P); err != nil {
		logs.Warn("auth error", err, r.RemoteAddr)
		return
	}
	if targetAddr, err = host.Target.GetRandomTarget(); err != nil {
		logs.Warn(err.Error())
	}
	logs.Info("new https connection,clientId %d,host %s,remote address %s", host.Client.Id, r.Host, c.RemoteAddr().String())
	https.DealClient(conn.NewConn(c), host.Client, targetAddr, rb, common.CONN_TCP, nil, host.Client.Flow, host.Target.LocalProxy, nil, host)
}

// close
func (https *HttpsServer) Close() error {
	return https.listener.Close()
}

// new https server by cert and key file
func (https *HttpsServer) NewHttps(l net.Listener, certFile string, keyFile string) {
	go func() {
		//logs.Error(https.NewServer(0, "https").ServeTLS(l, certFile, keyFile))
		logs.Error(https.NewServerWithTls(0, "https", l, certFile, keyFile))

	}()
}

// handle the https which is just proxy to other client
func (https *HttpsServer) handleHttps(c net.Conn) {
	hostName, rb := GetServerNameFromClientHello(c)
	var targetAddr string
	r := buildHttpsRequest(hostName)
	var host *file.Host
	var err error
	if host, err = file.GetDb().GetInfoByHost(hostName, r); err != nil {
		c.Close()
		logs.Notice("the url %s can't be parsed!", hostName)
		return
	}
	if err := https.CheckFlowAndConnNum(host.Client); err != nil {
		logs.Warn("client id %d, host id %d, error %s, when https connection", host.Client.Id, host.Id, err.Error())
		c.Close()
		return
	}
	defer host.Client.AddConn()
	if err = https.auth(r, conn.NewConn(c), host.Client.Cnf.U, host.Client.Cnf.P); err != nil {
		logs.Warn("auth error", err, r.RemoteAddr)
		return
	}
	if targetAddr, err = host.Target.GetRandomTarget(); err != nil {
		logs.Warn(err.Error())
	}
	logs.Trace("new https connection,clientId %d,host %s,remote address %s", host.Client.Id, r.Host, c.RemoteAddr().String())
	https.DealClient(conn.NewConn(c), host.Client, targetAddr, rb, common.CONN_TCP, nil, host.Client.Flow, host.Target.LocalProxy, nil, host)
}

type HttpsListener struct {
	acceptConn     chan *conn.Conn
	parentListener net.Listener
	closeCh        chan struct{}
	closeOnce      sync.Once
}

// https listener
func NewHttpsListener(l net.Listener) *HttpsListener {
	// 有缓冲：避免 Accept 未就绪时 send 阻塞泄漏 goroutine（阶段三 #3）
	return &HttpsListener{parentListener: l, acceptConn: make(chan *conn.Conn, 8), closeCh: make(chan struct{})}
}

// accept
func (httpsListener *HttpsListener) Accept() (net.Conn, error) {
	select {
	case httpsConn := <-httpsListener.acceptConn:
		if httpsConn == nil {
			return nil, errors.New("get connection error")
		}
		return httpsConn, nil
	case <-httpsListener.closeCh:
		return nil, errors.New("https listener closed")
	}
}

// close 真正关闭：通知阻塞中的 Accept 退出（阶段三 #3）
func (httpsListener *HttpsListener) Close() error {
	httpsListener.closeOnce.Do(func() {
		close(httpsListener.closeCh)
		// 排空缓冲中尚未被 Accept 取走的连接并关闭。
		// 只关 closeCh 时，Accept 返回错误后调用方不再调用 Accept，
		// 缓冲里最多 8 个已建立的连接将永不释放 → socket 泄漏（复评🟡）。
		for {
			select {
			case c := <-httpsListener.acceptConn:
				if c != nil {
					_ = c.Close()
				}
			default:
				return
			}
		}
	})
	return nil
}

// addr
func (httpsListener *HttpsListener) Addr() net.Addr {
	return httpsListener.parentListener.Addr()
}

// get server name from connection by read full client hello bytes
func GetServerNameFromClientHello(c net.Conn) (string, []byte) {
	header := make([]byte, 5)
	headerN, err := io.ReadFull(c, header)
	if err != nil {
		return "", header[:headerN]
	}
	if header[0] != 0x16 {
		return "", header
	}
	recordLen := int(header[3])<<8 | int(header[4])
	if recordLen <= 0 || recordLen > 16384 {
		return "", header
	}

	body := make([]byte, recordLen)
	bodyN, err := io.ReadFull(c, body)
	if err != nil {
		return "", append(header, body[:bodyN]...)
	}

	rawBytes := append(header, body...)
	clientHello := new(crypt.ClientHelloMsg)
	if !clientHello.Unmarshal(body) {
		return "", rawBytes
	}
	return clientHello.GetServerName(), rawBytes
}

// build https request for SNI-based host lookup.
// RequestURI is set to "*" to skip Location filtering in GetInfoByHost,
// because at TLS handshake stage the actual HTTP request path is unknown.
func buildHttpsRequest(hostName string) *http.Request {
	r := new(http.Request)
	r.RequestURI = "*"
	r.URL = new(url.URL)
	r.URL.Scheme = "https"
	r.Host = hostName
	return r
}
