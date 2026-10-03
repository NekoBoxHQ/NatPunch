package client

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"ehang.io/nps/lib/nps_mux"
	"github.com/pires/go-proxyproto"

	"github.com/astaxie/beego/logs"
	"github.com/creack/pty"
	"github.com/xtaci/kcp-go"

	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/config"
	"ehang.io/nps/lib/conn"
	"ehang.io/nps/lib/crypt"
)

// shellPtyMap：ShellID -> *os.File(pty)，供 shellresize 控制消息定位对应终端（带外控制，与数据零混流）
var shellPtyMap sync.Map

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	proxyUrl       string
	vKey           string
	p2pAddr        map[string]string
	tunnel         *nps_mux.Mux
	signal         *conn.Conn
	ticker         *time.Ticker
	cnf            *config.Config
	disconnectTime int
	once           sync.Once
	closeCh        chan struct{} // closed when client is shutting down; stops ping
	logger         Logger        // 每客户端独立的 logger，未设置时使用全局 logger
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
		p2pAddr:        make(map[string]string, 0),
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
	return s.signal != nil
}

var NowStatus int
var CloseClient bool

// start
func (s *TRPClient) Start() {
	CloseClient = false
retry:
	if CloseClient {
		return
	}
	NowStatus = 0
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
	s.signal = c
	//start a channel connection
	go s.newChan()
	//start health check if the it's open
	if s.cnf != nil && len(s.cnf.Healths) > 0 {
		go heathCheck(s.cnf.Healths, s.signal)
	}
	NowStatus = 1
	//msg connection, eg udp
	s.handleMain()
}

// handle main connection
func (s *TRPClient) handleMain() {
mainLoop:
	for {
		flags, err := s.signal.ReadFlag()
		if err != nil {
			s.logError("Accept server data error %s, end this service", err.Error())
			break
		}
		switch flags {
		case common.REPORT_LOCAL_IP:
			// server requests private/LAN IPs (new nps); ignore failures so main loop continues
			localIPs := common.GetLocalIPs(s.signal.Conn)
			if err := s.signal.WriteLenContent([]byte(localIPs)); err != nil {
				s.logWarn("report local ip failed: %s", err.Error())
			} else {
				s.logInfo("reported local addr: %s", localIPs)
			}
		case common.NEW_UDP_CONN:
			//read server udp addr and password
			if lAddr, err := s.signal.GetShortLenContent(); err != nil {
				s.logWarn(err.Error())
				break mainLoop
			} else if pwd, err := s.signal.GetShortLenContent(); err == nil {
				var localAddr string
				//The local port remains unchanged for a certain period of time
				if v, ok := s.p2pAddr[crypt.Md5(string(pwd)+strconv.Itoa(int(time.Now().Unix()/100)))]; !ok {
					tmpConn, err := common.GetLocalUdpAddr()
					if err != nil {
						s.logError(err.Error())
						break mainLoop
					}
					localAddr = tmpConn.LocalAddr().String()
				} else {
					localAddr = v
				}
				go s.newUdpConn(localAddr, string(lAddr), string(pwd))
			}
		}
	}
	s.Close()
}

func (s *TRPClient) newUdpConn(localAddr, rAddr string, md5Password string) {
	var localConn net.PacketConn
	var err error
	var remoteAddress string
	if remoteAddress, localConn, err = handleP2PUdp(localAddr, rAddr, md5Password, common.WORK_P2P_PROVIDER); err != nil {
		s.logError(err.Error())
		return
	}
	l, err := kcp.ServeConn(nil, 150, 3, localConn)
	if err != nil {
		s.logError(err.Error())
		return
	}
	s.logTrace("start local p2p udp listen, local address %s", localConn.LocalAddr().String())
	for {
		udpTunnel, err := l.AcceptKCP()
		if err != nil {
			s.logError(err.Error())
			l.Close()
			return
		}
		if udpTunnel.RemoteAddr().String() == string(remoteAddress) {
			conn.SetUdpSession(udpTunnel)
			s.logTrace("successful connection with client ,address %s", udpTunnel.RemoteAddr().String())
			//read link info from remote
			conn.Accept(nps_mux.NewMux(udpTunnel, s.bridgeConnType, s.disconnectTime), func(c net.Conn) {
				go s.handleChan(c)
			})
			break
		}
	}
}

// pmux tunnel
func (s *TRPClient) newChan() {
	tunnel, err := NewConn(s.bridgeConnType, s.vKey, s.svrAddr, common.WORK_CHAN, s.proxyUrl)
	if err != nil {
		// WORK_MAIN may already be up; without Close the client stays half-connected
		// (handleMain blocks, outer reconnect loop never runs). See #115.
		s.logError("connect to %s error: %v, client will reconnect", s.svrAddr, err)
		s.Close()
		return
	}
	s.tunnel = nps_mux.NewMux(tunnel.Conn, s.bridgeConnType, s.disconnectTime)
	for {
		src, err := s.tunnel.Accept()
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
		// 起始目录与电脑 SSH 登录一致：家目录（root 的 /root），避免面板终端起始显示 /
		home := "/root"
		if h, herr := os.UserHomeDir(); herr == nil && h != "" {
			home = h
		}
		cmd.Dir = home
		// 注入 PS1 兜底：个别系统 profile/bash.bashrc 未设置 PS1 时，保证仍显示 root@host:~# 完整提示符
		// 同时注入 HOME：bash 的 \w 需要 $HOME 判断家目录，否则显示 /root 而非 ~
		// TERM=xterm-256color：与电脑 SSH 一致的标准终端类型，vim/top/htop 颜色与全屏布局正常
		cmd.Env = append(os.Environ(), "HOME="+home, "PS1=\\u@\\h:\\w\\$ ", "TERM=xterm-256color")
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
		// 系统信息先写入（模拟 SSH 客户端连接信息面板，客户端→服务端→前端），失败项自动跳过
		if info := collectSysInfo(); info != "" {
			srcConn.Write([]byte("连接主机成功\n" + info))
		}
		// 完整双向桥接：banner/motd（ASCII logo、系统版本、Last login 前的 profile 输出）正常显示，
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
				s := strings.Split(lk.RemoteAddr, ":")[1]
				port, _ := strconv.Atoi(s)
				addr = &net.TCPAddr{
					IP:   net.ParseIP(strings.Split(lk.RemoteAddr, ":")[0]),
					Port: port,
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
			if s.tunnel != nil && s.tunnel.IsClose() {
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
	CloseClient = true
	NowStatus = 0
	// unblock ping; safe: closing runs only once via once.Do
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	if s.tunnel != nil {
		_ = s.tunnel.Close()
	}
	if s.signal != nil {
		_ = s.signal.Close()
	}
}

// runQuick 执行单个本地命令并返回输出（超时 2 秒），失败返回空串。
// 仅使用通用命令，兼容 OpenWrt busybox / Linux（id/ps/df/free/hostname/uptime/uname/grep/cat）。
func runQuick(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// publicIP 查询客户端设备公网 IP（IPv4/IPv6）。仅当设备存在 curl/wget 时执行，超时 3 秒，失败返回空串。
func publicIP(v6 bool) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "https://api.ipify.org"
	if v6 {
		url = "https://ifconfig.co"
	}
	if _, err := exec.LookPath("curl"); err == nil {
		args := []string{"curl", "-s", "--max-time", "2"}
		if v6 {
			args = append(args, "-6")
		}
		args = append(args, url)
		if out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	if _, err := exec.LookPath("wget"); err == nil {
		args := []string{"wget", "-qO-", "-T", "2"}
		if v6 {
			args = append(args, "-6")
		}
		args = append(args, url)
		if out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				// busybox wget 不支持 -6：拿不到真实 v6 时可能返回空或 IPv4 地址，校验过滤
				if v6 && !strings.Contains(s, ":") {
					return ""
				}
				return s
			}
		}
	}
	return ""
}

// collectSysInfo 采集设备系统信息（模拟 SSH 客户端连接信息面板），失败项自动跳过。
// 由 shell 分支在桥接前写入连接流，前端显示在 shell banner 之前。
func collectSysInfo() string {
	var b strings.Builder
	// 登录用户
	if uid := runQuick("id", "-u"); uid != "" {
		fmt.Fprintf(&b, "登录用户 : %s\n", uid)
	}
	// 运行进程（GNU ps 需 -e 显示全部；busybox ps 默认全量且不识别 -e，失败回退裸 ps）
	ps := runQuick("ps", "-e")
	if ps == "" {
		ps = runQuick("ps")
	}
	if ps != "" {
		fmt.Fprintf(&b, "运行进程 : %d\n", strings.Count(ps, "\n"))
	}
	// 磁盘使用（df -h / 第二行：Size Used）
	if df := runQuick("df", "-h", "/"); df != "" {
		lines := strings.Split(df, "\n")
		if len(lines) >= 2 {
			fs := strings.Fields(lines[1])
			if len(fs) >= 3 {
				fmt.Fprintf(&b, "磁盘使用 : %s/%s\n", fs[2], fs[1])
			}
		}
	}
	// 主机名称
	if hn := runQuick("hostname"); hn != "" {
		fmt.Fprintf(&b, "主机名称 : %s\n", hn)
	}
	// 内存使用（free -m 兼容 busybox，busybox 无 -h）
	if free := runQuick("free", "-m"); free != "" {
		for _, l := range strings.Split(free, "\n") {
			if strings.HasPrefix(l, "Mem:") {
				fs := strings.Fields(l)
				if len(fs) >= 3 {
					fmt.Fprintf(&b, "内存使用 : %sMi/%sMi\n", fs[2], fs[1])
				}
				break
			}
		}
	}
	// 公网 IPv4 / IPv6 并行查询（最慢项约 3 秒，串行会翻倍到 6 秒；查询失败自动跳过）
	v4c := make(chan string, 1)
	go func() { v4c <- publicIP(false) }()
	v6 := publicIP(true)
	v4 := <-v4c
	if v4 != "" {
		fmt.Fprintf(&b, "公网IPv4 : %s\n", v4)
	}
	if v6 != "" {
		fmt.Fprintf(&b, "公网IPv6 : %s\n", v6)
	}
	// 运行时间（uptime -p；busybox 不支持 -p 时回退 /proc/uptime 换算）
	if up := runQuick("uptime", "-p"); up != "" {
		up = strings.TrimPrefix(up, "up ")
		fmt.Fprintf(&b, "运行时间 : %s\n", up)
	} else if ut := runQuick("cat", "/proc/uptime"); ut != "" {
		secs := 0.0
		fmt.Sscanf(ut, "%f", &secs)
		if secs > 0 {
			d := int(secs) / 86400
			h := (int(secs) % 86400) / 3600
			m := (int(secs) % 3600) / 60
			fmt.Fprintf(&b, "运行时间 : %d days, %d hours, %d minutes\n", d, h, m)
		}
	}
	// 操作系统（/etc/os-release PRETTY_NAME，失败回退 uname）
	if osr := runQuick("sh", "-c", "grep PRETTY_NAME /etc/os-release 2>/dev/null | cut -d'\"' -f2"); osr != "" {
		fmt.Fprintf(&b, "操作系统 : %s\n", osr)
	} else if un := runQuick("uname", "-sr"); un != "" {
		fmt.Fprintf(&b, "操作系统 : %s\n", un)
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
