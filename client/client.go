package client

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
	"github.com/pires/go-proxyproto"

	"github.com/astaxie/beego/logs"
	"github.com/creack/pty"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/config"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
)

// shellPtyMap：ShellID -> *os.File(pty)，供 shellresize 控制消息定位对应终端（带外控制，与数据零混流）
var shellPtyMap sync.Map

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	proxyUrl       string
	vKey           string
	tunnel         atomic.Pointer[natpunch_mux.Mux]
	signal         atomic.Pointer[conn.Conn]
	ticker         *time.Ticker
	cnf            *config.Config
	disconnectTime int
	once           sync.Once
	closeCh        chan struct{} // closed when client is shutting down; stops ping
	logger         Logger        // 每客户端独立的 logger，未设置时使用全局 logger
	nowStatus      atomic.Int32  // 连接状态（原包级 NowStatus，收敛为实例字段，阶段三 #6）
	closeClient    atomic.Bool   // 关闭标记（原包级 CloseClient，阶段三 #6）
}

// Logger 客户端日志接口，GUI / CLI 均可注入实现
type Logger interface {
	Info(format string, v ...interface{})
	Error(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Trace(format string, v ...interface{})
}

// new client
func NewRPClient(svraddr string, vKey string, bridgeConnType string, proxyUrl string, cnf *config.Config, disconnectTime int) *TRPClient {
	return &TRPClient{
		svrAddr:        svraddr,
		vKey:           vKey,
		bridgeConnType: bridgeConnType,
		proxyUrl:       proxyUrl,
		cnf:            cnf,
		disconnectTime: disconnectTime,
		once:           sync.Once{},
		closeCh:        make(chan struct{}),
		logger:         nil, // 默认使用全局 logger，可通过 SetLogger 设置
	}
}

// SetLogger 设置客户端的独立 logger
func (s *TRPClient) SetLogger(logger Logger) {
	s.logger = logger
}

// log 辅助方法：如果设置了独立 logger 就使用，否则使用全局 logger
func (s *TRPClient) logInfo(format string, v ...interface{}) {
	if s.logger != nil {
		s.logger.Info(format, v...)
	} else {
		logs.Info(format, v...)
	}
}

func (s *TRPClient) logError(format string, v ...interface{}) {
	if s.logger != nil {
		s.logger.Error(format, v...)
	} else {
		logs.Error(format, v...)
	}
}

func (s *TRPClient) logWarn(format string, v ...interface{}) {
	if s.logger != nil {
		s.logger.Warn(format, v...)
	} else {
		logs.Warn(format, v...)
	}
}

func (s *TRPClient) logTrace(format string, v ...interface{}) {
	if s.logger != nil {
		s.logger.Trace(format, v...)
	} else {
		logs.Trace(format, v...)
	}
}

// IsConnected 返回客户端是否已成功连接到服务器
func (s *TRPClient) IsConnected() bool {
	return s.signal.Load() != nil
}

// Status 返回当前连接状态（0=未连接，1=已连接；原包级 NowStatus，阶段三 #6）
func (s *TRPClient) Status() int {
	return int(s.nowStatus.Load())
}

// start
func (s *TRPClient) Start() {
	s.closeClient.Store(false)
retry:
	if s.closeClient.Load() {
		return
	}
	s.nowStatus.Store(0)
	c, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, common.WORK_MAIN, s.proxyUrl)
	if err != nil {
		s.logError("The connection server failed and will be reconnected in five seconds, error", err.Error())
		time.Sleep(time.Second * 5)
		goto retry
	}
	if c == nil {
		s.logError("Error data from server, and will be reconnected in five seconds")
		time.Sleep(time.Second * 5)
		goto retry
	}
	s.logInfo("Successful connection with server %s", s.svrAddr)
	//monitor the connection
	go s.ping()
	s.signal.Store(c)
	//start a channel connection
	go s.newChan()
	//start health check if the it's open
	if s.cnf != nil && len(s.cnf.Healths) > 0 {
		go heathCheck(s.cnf.Healths, c)
	}
	s.nowStatus.Store(1)
	//msg connection, eg udp
	s.handleMain()
}

// handle main connection
func (s *TRPClient) handleMain() {
	// 循环外取一次本地引用：closing() 会把 s.signal 置空（关闭后 IsConnected 返回 false），
	// 如果每轮都从字段读，关闭恰好卡在读取之前就会 nil 解引用 panic。
	// 持有本地引用不受影响 —— 连接被 Close 后 ReadFlag 会返回错误，循环自然退出。
	sig := s.signal.Load()
	if sig == nil {
		return
	}
	for {
		flags, err := sig.ReadFlag()
		if err != nil {
			s.logError("Accept server data error %s, end this service", err.Error())
			break
		}
		switch flags {
		case common.REPORT_LOCAL_IP:
			// server requests private/LAN IPs (new natpunch); ignore failures so main loop continues
			localIPs := common.GetLocalIPs(sig.Conn)
			if err := sig.WriteLenContent([]byte(localIPs)); err != nil {
				s.logWarn("report local ip failed: %s", err.Error())
			} else {
				s.logInfo("reported local addr: %s", localIPs)
			}
		}
	}
	s.Close()
}

func (s *TRPClient) newChan() {
	tunnel, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, common.WORK_CHAN, s.proxyUrl)
	if err != nil {
		// WORK_MAIN may already be up; without Close the client stays half-connected
		// (handleMain blocks, outer reconnect loop never runs). See #115.
		s.logError("connect to %s error: %v, client will reconnect", s.svrAddr, err)
		s.Close()
		return
	}
	mux := natpunch_mux.NewMux(tunnel.Conn, s.bridgeConnType, s.disconnectTime)
	s.tunnel.Store(mux)
	for {
		src, err := mux.Accept()
		if err != nil {
			s.logWarn(err.Error())
			s.Close()
			break
		}
		go s.handleChan(src)
	}
}

func (s *TRPClient) handleChan(src net.Conn) {
	lk, err := conn.NewConn(src).GetLinkInfo()
	if err != nil || lk == nil {
		src.Close()
		s.logError("get connection info from server error %v", err)
		return
	}
	//host for target processing
	lk.Host = common.FormatAddress(lk.Host)
	//if Conn type is http, read the request and log
	if lk.ConnType == "http" {
		if targetConn, err := net.DialTimeout(common.CONN_TCP, lk.Host, lk.Option.Timeout); err != nil {
			s.logWarn("connect to %s error %s", lk.Host, err.Error())
			src.Close()
		} else {
			srcConn := conn.GetConn(src, lk.Crypt, lk.Compress, nil, false)
			go func() {
				common.CopyBuffer(srcConn, targetConn)
				srcConn.Close()
				targetConn.Close()
			}()
			br := bufio.NewReader(srcConn)
			for {
				if r, err := http.ReadRequest(br); err != nil {
					srcConn.Close()
					targetConn.Close()
					break
				} else {
					remoteAddr := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
					if len(remoteAddr) == 0 {
						remoteAddr = r.RemoteAddr
					}
					s.logTrace("http request, method %s, host %s, url %s, remote address %s", r.Method, r.Host, r.URL.Path, remoteAddr)
					r.Write(targetConn)
				}
			}
		}
		return
	}
	if lk.ConnType == "udp5" {
		s.logTrace("new %s connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)
		s.handleUdp(src)
	}
	// shellresize: 服务端下发的终端尺寸控制消息（带外通道，不经过数据流，零混流风险）
	// 定位对应 pty 应用新尺寸；旧客户端不认识此类型会走默认分支忽略，不影响现有功能
	if lk.ConnType == "shellresize" {
		if pf, ok := shellPtyMap.Load(lk.ShellID); ok {
			if f, ok2 := pf.(*os.File); ok2 {
				if err := pty.Setsize(f, &pty.Winsize{Cols: uint16(lk.Cols), Rows: uint16(lk.Rows)}); err != nil {
					s.logWarn("shell resize error %s", err.Error())
				}
			}
		}
		src.Close()
		return
	}
	// shell: 本地启动 shell（面板终端，无需 SSH 凭据）
	if lk.ConnType == "shell" {
		s.logTrace("new shell connection, remote address:%s", lk.RemoteAddr)
		cols, rows := lk.Cols, lk.Rows
		if cols <= 0 {
			cols = 80
		}
		if rows <= 0 {
			rows = 24
		}
		// 登录式 shell（与电脑 SSH 登录同一机制）。Linux 上优先 bash：
		// Debian/Ubuntu 的 /bin/sh=dash，其 /etc/profile 对 dash 固定把 PS1 设为 "# "（系统设计），
		// 只有 bash 分支才给出 root@host:~# 完整提示符；OpenWrt 无 bash，回退 /bin/sh(busybox ash)。
		shellPath := "/bin/sh"
		if _, serr := os.Stat("/bin/bash"); serr == nil {
			shellPath = "/bin/bash"
		}
		cmd := exec.Command(shellPath, "-l")
		// 家目录与环境变量的构造见 shellenv.go —— 那里解释了为什么要拒绝 HOME="/"、
		// 为什么 HOME/PS1/TERM/HOSTNAME 必须"先摘后加"。这块逻辑有单测（shellenv_test.go）。
		home := shellHome()
		cmd.Dir = home
		hostname, _ := os.Hostname()
		cmd.Env = buildShellEnv(os.Environ(), home, hostname)
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		if err != nil {
			s.logWarn("start shell error %s", err.Error())
			src.Close()
			return
		}
		// 记录 ShellID->pty，供 shellresize 控制消息定位；旧服务端无 ShellID 时不记录（resize 降级为空操作）
		if lk.ShellID != "" {
			shellPtyMap.Store(lk.ShellID, f)
			defer shellPtyMap.Delete(lk.ShellID)
		}
		srcConn := conn.GetConn(src, lk.Crypt, lk.Compress, nil, false)
		// 完整双向桥接：系统自带 banner/motd（ASCII logo、系统版本、profile 输出）正常显示，
		// 与电脑 SSH 登录一致。TERM=xterm-256color 已注入，banner 的 ANSI 颜色/控制序列正常渲染。
		go func() {
			io.Copy(srcConn, f)
			srcConn.Close()
			f.Close()
			cmd.Process.Kill()
		}()
		io.Copy(f, srcConn)
		f.Close()
		srcConn.Close()
		cmd.Process.Kill()
		return
	}
	//connect to target if conn type is tcp or udp
	if targetConn, err := net.DialTimeout(lk.ConnType, lk.Host, lk.Option.Timeout); err != nil {
		s.logWarn("connect to %s error %s", lk.Host, err.Error())
		src.Close()
	} else {
		s.logTrace("new %s connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)

		if lk.ProtoVersion == "V1" || lk.ProtoVersion == "V2" {
			var addr = targetConn.RemoteAddr()
			if lk.RemoteAddr != "" {
				// RemoteAddr 完全由服务端下发的 link 决定。原实现盲切
				// strings.Split(..., ":")[1]，不含冒号时直接越界 panic；
				// 这里跑在独立 goroutine 里，外层 recover 覆盖不到，
				// 结果是 root 守护进程整个退出（远程可触发）。
				if host, portStr, err := net.SplitHostPort(lk.RemoteAddr); err == nil {
					if port, perr := strconv.Atoi(portStr); perr == nil {
						addr = &net.TCPAddr{IP: net.ParseIP(host), Port: port}
					}
				} else {
					s.logWarn("服务端下发的 RemoteAddr 非法（%q），回退到真实远端地址", lk.RemoteAddr)
				}
			}

			var version byte

			if lk.ProtoVersion == "V1" {
				version = 1
			} else if lk.ProtoVersion == "V2" {
				version = 2
			}

			transportProtocol := proxyproto.TCPv4
			if strings.Contains(addr.String(), ".") {
				transportProtocol = proxyproto.TCPv4
			} else {
				transportProtocol = proxyproto.TCPv6
			}

			header := &proxyproto.Header{
				Command:           proxyproto.PROXY,
				SourceAddr:        addr,
				DestinationAddr:   targetConn.RemoteAddr(),
				Version:           version,
				TransportProtocol: transportProtocol,
			}

			_, err2 := header.WriteTo(targetConn)
			if err2 != nil {
				s.logError(err2.Error())
			}
		}

		conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, false, nil, nil, nil)
	}
}

func (s *TRPClient) handleUdp(serverConn net.Conn) {
	// bind a local udp port
	local, err := net.ListenUDP("udp", nil)
	defer serverConn.Close()
	if err != nil {
		s.logError("bind local udp port error %s", err.Error())
		return
	}
	defer local.Close()
	go func() {
		defer serverConn.Close()
		b := common.BufPoolUdp.Get().([]byte)
		defer common.BufPoolUdp.Put(b)
		var buf bytes.Buffer
		for {
			n, raddr, err := local.ReadFrom(b)
			if err != nil {
				s.logError("read data from remote server error %s", err.Error())
				return
			}
			buf.Reset()
			dgram := common.NewUDPDatagram(common.NewUDPHeader(0, 0, common.ToSocksAddr(raddr)), b[:n])
			dgram.Write(&buf)
			data, err := conn.GetLenBytes(buf.Bytes())
			if err != nil {
				s.logWarn("get len bytes error %s", err.Error())
				continue
			}
			if _, err := serverConn.Write(data); err != nil {
				s.logError("write data to remote  error %s", err.Error())
				return
			}
		}
	}()
	b := common.BufPoolUdp.Get().([]byte)
	defer common.BufPoolUdp.Put(b)
	for {
		n, err := serverConn.Read(b)
		if err != nil {
			s.logError("read udp data from server error %s", err.Error())
			return
		}

		udpData, err := common.ReadUDPDatagram(bytes.NewReader(b[:n]))
		if err != nil {
			s.logError("unpack data error %s", err.Error())
			return
		}
		raddr, err := net.ResolveUDPAddr("udp", udpData.Header.Addr.String())
		if err != nil {
			s.logError("build remote addr err %s", err.Error())
			continue // drop silently
		}
		_, err = local.WriteTo(udpData.Data, raddr)
		if err != nil {
			s.logError("write data to remote %s error %s", raddr.String(), err.Error())
			return
		}
	}
}

// Whether the monitor channel is closed
func (s *TRPClient) ping() {
	s.ticker = time.NewTicker(time.Second * 5)
	defer s.ticker.Stop()
	for {
		select {
		case <-s.ticker.C:
			// tunnel still nil: newChan failed or still connecting; if Close already ran, closeCh fires.
			// tunnel established then closed: tear down so outer loop can reconnect.
			if mux := s.tunnel.Load(); mux != nil && mux.IsClose() {
				s.Close()
				return
			}
		case <-s.closeCh:
			return
		}
	}
}

func (s *TRPClient) Close() {
	s.once.Do(s.closing)
}

func (s *TRPClient) closing() {
	s.closeClient.Store(true)
	s.nowStatus.Store(0)
	// unblock ping; safe: closing runs only once via once.Do
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	if mux := s.tunnel.Load(); mux != nil {
		_ = mux.Close()
	}
	if sig := s.signal.Load(); sig != nil {
		_ = sig.Close()
	}
	s.signal.Store(nil) // 复位：IsConnected 在关闭后返回 false（阶段三 #6）
}
