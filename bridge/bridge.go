package bridge

import (
	"crypto/tls"
	"github.com/NekoBoxHQ/NatPunch/lib/nps_mux"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/NekoBoxHQ/NatPunch/server/connection"
	"github.com/NekoBoxHQ/NatPunch/server/tool"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

var ServerTlsEnable bool = false

type Client struct {
	mu        sync.Mutex // 保护 signal/tunnel/Version 等字段的并发读写
	tunnel    *nps_mux.Mux
	signal    *conn.Conn
	file      *nps_mux.Mux
	Version   string
	retryTime atomic.Int32 // it will be add 1 when ping not ok until to 3 will close the client
}

func NewClient(t, f *nps_mux.Mux, s *conn.Conn, vs string) *Client {
	return &Client{
		signal:  s,
		tunnel:  t,
		file:    f,
		Version: vs,
	}
}

type Bridge struct {
	TunnelPort     int //通信隧道端口
	Client         sync.Map
	Register       sync.Map
	tunnelType     string //bridge type kcp or tcp
	OpenTask       chan *file.Tunnel
	CloseTask      chan *file.Tunnel
	CloseClient    chan int
	SecretChan     chan *conn.Secret
	ipVerify       bool
	runList        *sync.Map //map[int]interface{}
	disconnectTime int
}

func NewTunnel(tunnelPort int, tunnelType string, ipVerify bool, runList *sync.Map, disconnectTime int) *Bridge {
	return &Bridge{
		TunnelPort:     tunnelPort,
		tunnelType:     tunnelType,
		// 有缓冲：避免 DealBridgeTask 忙时生产者（client 消息循环）阻塞（阶段三 G6）
		OpenTask:       make(chan *file.Tunnel, 128),
		CloseTask:      make(chan *file.Tunnel, 128),
		CloseClient:    make(chan int, 128),
		SecretChan:     make(chan *conn.Secret, 128),
		ipVerify:       ipVerify,
		runList:        runList,
		disconnectTime: disconnectTime,
	}
}

func (s *Bridge) StartTunnel() error {
	go s.ping()
	if s.tunnelType == "kcp" {
		logs.Info("server start, the bridge type is %s, the bridge port is %d", s.tunnelType, s.TunnelPort)
		return conn.NewKcpListenerAndProcess(beego.AppConfig.String("bridge_ip")+":"+beego.AppConfig.String("bridge_port"), func(c net.Conn) {
			s.cliProcess(conn.NewConn(c))
		})
	} else {

		go func() {
			listener, err := connection.GetBridgeListener(s.tunnelType)
			if err != nil {
				// 库代码禁止 os.Exit：监听失败记录错误并退出本 goroutine（阶段三 #15）
				logs.Error("bridge listener start error: %v", err)
				return
			}
			conn.Accept(listener, func(c net.Conn) {
				s.cliProcess(conn.NewConn(c))
			})
		}()

		// tls
		if ServerTlsEnable {
			go func() {
				// 监听TLS 端口
				tlsBridgePort := beego.AppConfig.DefaultInt("tls_bridge_port", 8025)

				logs.Info("tls server start, the bridge type is %s, the tls bridge port is %d", "tcp", tlsBridgePort)
				tlsListener, tlsErr := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(beego.AppConfig.String("bridge_ip")), Port: tlsBridgePort})
				if tlsErr != nil {
					// 库代码禁止 os.Exit：监听失败记录错误并退出本 goroutine（阶段三 #15）
					logs.Error("tls bridge listener start error: %v", tlsErr)
					return
				}
				conn.Accept(tlsListener, func(c net.Conn) {
					s.cliProcess(conn.NewConn(tls.Server(c, crypt.BuildTlsServerConfig())))
				})
			}()
		}
	}
	return nil
}

// requestClientLocalAddr asks the client for private/LAN IPs on the main signal conn.
// New clients reply with WriteLenContent; old clients ignore the flag and we time out.
func (s *Bridge) requestClientLocalAddr(id int, c *conn.Conn) {
	if c == nil || c.Conn == nil {
		return
	}
	if _, err := c.Write([]byte(common.REPORT_LOCAL_IP)); err != nil {
		return
	}
	_ = c.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer func() {
		// clear deadline so health / later reads are not affected
		_ = c.Conn.SetReadDeadline(time.Time{})
	}()
	b, err := c.GetShortLenContent()
	if err != nil {
		logs.Trace("clientId %d did not report local addr (old client or timeout): %v", id, err)
		return
	}
	localAddr := strings.TrimSpace(string(b))
	if localAddr == "" {
		return
	}
	if client, err := file.GetDb().GetClient(id); err == nil {
		client.Lock()
		client.LocalAddr = localAddr
		client.Unlock()
		logs.Info("clientId %d local addr: %s", id, localAddr)
		file.GetDb().JsonDb.StoreClientsToJsonFile()
	}
}

// get health information form client
func (s *Bridge) GetHealthFromClient(id int, c *conn.Conn) {
	for {
		if info, status, err := c.GetHealthInfo(); err != nil {
			break
		} else if !status { //the status is true , return target to the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(v.Target.TargetStr, "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && strings.Contains(v.Target.TargetStr, info) {
					v.Lock()
					if v.Target.TargetArr == nil || (len(v.Target.TargetArr) == 0 && len(v.HealthRemoveArr) == 0) {
						v.Target.TargetArr = common.TrimArr(strings.Split(v.Target.TargetStr, "\n"))
					}
					v.Target.TargetArr = common.RemoveArrVal(v.Target.TargetArr, info)
					if v.HealthRemoveArr == nil {
						v.HealthRemoveArr = make([]string, 0)
					}
					v.HealthRemoveArr = append(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		} else { //the status is false,remove target from the targetArr
			file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
				v := value.(*file.Tunnel)
				if v.Client.Id == id && v.Mode == "tcp" && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})

			file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
				v := value.(*file.Host)
				if v.Client.Id == id && common.IsArrContains(v.HealthRemoveArr, info) && !common.IsArrContains(v.Target.TargetArr, info) {
					v.Lock()
					v.Target.TargetArr = append(v.Target.TargetArr, info)
					v.HealthRemoveArr = common.RemoveArrVal(v.HealthRemoveArr, info)
					v.Unlock()
				}
				return true
			})
		}
	}
	s.DelClient(id)
}

// 验证失败，返回错误验证flag，并且关闭连接
func (s *Bridge) verifyError(c *conn.Conn) {
	c.Write([]byte(common.VERIFY_EER))
	c.Close()
}

func (s *Bridge) verifySuccess(c *conn.Conn) {
	c.Write([]byte(common.VERIFY_SUCCESS))
}

func (s *Bridge) cliProcess(c *conn.Conn) {
	// 握手 10s 超时：扫描/半开连接不会一直占着 goroutine；成功后清掉
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))

	//read test flag
	if _, err := c.GetShortContent(3); err != nil {
		logs.Info("The client %s connect error", c.Conn.RemoteAddr(), err.Error())
		c.Close()
		return
	}
	//version check（版本不匹配仍兼容放行；读失败则断开）
	if _, err := c.GetShortLenContent(); err != nil {
		c.Close()
		return
	}
	//version get
	var vs []byte
	var err error
	if vs, err = c.GetShortLenContent(); err != nil {
		logs.Info("get client %s version error", err.Error())
		c.Close()
		return
	}
	//write server version to client
	c.Write([]byte(crypt.Md5(version.GetVersion())))
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var buf []byte
	//get vKey from client
	if buf, err = c.GetShortContent(32); err != nil {
		c.Close()
		return
	}
	//verify
	id, err := file.GetDb().GetIdByVerifyKey(string(buf), c.Conn.RemoteAddr().String())
	if err != nil {
		logs.Info("Current client connection validation error, close this client:", c.Conn.RemoteAddr())
		s.verifyError(c)
		return
	} else {
		s.verifySuccess(c)
	}
	if flag, err := c.ReadFlag(); err == nil {
		// 握手完成，交给 typeDeal 做长连接
		_ = c.SetReadDeadline(time.Time{})
		s.typeDeal(flag, c, id, string(vs))
	} else {
		logs.Warn(err, flag)
		c.Close()
	}
	return
}

func (s *Bridge) DelClient(id int) {
	if v, ok := s.Client.Load(id); ok {
		cl := v.(*Client)
		cl.mu.Lock()
		signalToClose := cl.signal
		cl.signal = nil
		cl.mu.Unlock()
		if signalToClose != nil {
			signalToClose.Close()
		}
		s.Client.Delete(id)
		if file.GetDb().IsPubClient(id) {
			return
		}
		if c, err := file.GetDb().GetClient(id); err == nil {
			s.CloseClient <- c.Id
		}
	}
}

// use different
func (s *Bridge) typeDeal(typeVal string, c *conn.Conn, id int, vs string) {
	isPub := file.GetDb().IsPubClient(id)
	switch typeVal {
	case common.WORK_MAIN:
		if isPub {
			c.Close()
			return
		}
		tcpConn, ok := c.Conn.(*net.TCPConn)
		if ok {
			// add tcp keep alive option for signal connection
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(5 * time.Second)
		}
		//the vKey connect by another ,close the client of before
		if v, ok := s.Client.LoadOrStore(id, NewClient(nil, nil, c, vs)); ok {
			cl := v.(*Client)
			cl.mu.Lock()
			oldSignal := cl.signal
			cl.signal = c
			cl.Version = vs
			cl.mu.Unlock()
			cl.retryTime.Store(0)
			if oldSignal != nil {
				oldSignal.WriteClose()
			}
		}
		// Request private/LAN IPs from client. Old clients ignore the flag; short timeout keeps compatibility.
		s.requestClientLocalAddr(id, c)
		go s.GetHealthFromClient(id, c)
		logs.Info("clientId %d connection succeeded, address:%s ", id, c.Conn.RemoteAddr())
	case common.WORK_CHAN:
		muxConn := nps_mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime)
		if v, ok := s.Client.LoadOrStore(id, NewClient(muxConn, nil, nil, vs)); ok {
			cl := v.(*Client)
			cl.mu.Lock()
			oldTunnel := cl.tunnel
			cl.tunnel = muxConn
			cl.mu.Unlock()
			if oldTunnel != nil {
				oldTunnel.Close()
			}
		}
	case common.WORK_CONFIG:
		client, err := file.GetDb().GetClient(id)
		if err != nil || (!isPub && !client.ConfigConnAllow) {
			c.Close()
			return
		}
		if err := binary.Write(c, binary.LittleEndian, isPub); err != nil {
			logs.Warn("write isPub error: %v", err)
			c.Close()
			return
		}
		go s.getConfig(c, isPub, client)
	case common.WORK_REGISTER:
		go s.register(c)
	case common.WORK_SECRET:
		if b, err := c.GetShortContent(32); err == nil {
			s.SecretChan <- conn.NewSecret(string(b), c)
		} else {
			logs.Error("secret error, failed to match the key successfully")
		}
	case common.WORK_FILE:
		muxConn := nps_mux.NewMux(c.Conn, s.tunnelType, s.disconnectTime)
		if v, ok := s.Client.LoadOrStore(id, NewClient(nil, muxConn, nil, vs)); ok {
			cl := v.(*Client)
			cl.mu.Lock()
			oldFile := cl.file
			cl.file = muxConn
			cl.mu.Unlock()
			if oldFile != nil {
				oldFile.Close()
			}
		}
	case common.WORK_P2P:
		//read md5 secret
		if b, err := c.GetShortContent(32); err != nil {
			logs.Error("p2p error,", err.Error())
		} else if t := file.GetDb().GetTaskByMd5Password(string(b)); t == nil {
			logs.Error("p2p error, failed to match the key successfully")
		} else {
			if v, ok := s.Client.Load(t.Client.Id); !ok {
				return
			} else {
				cl := v.(*Client)
				cl.mu.Lock()
				sig := cl.signal
				cl.mu.Unlock()
				if sig == nil {
					return
				}
				//向密钥对应的客户端发送与服务端udp建立连接信息，地址，密钥
				if _, err := sig.Write([]byte(common.NEW_UDP_CONN)); err != nil {
					logs.Warn("p2p write NEW_UDP_CONN error: %v", err)
					return
				}
				svrAddr := beego.AppConfig.String("p2p_ip") + ":" + beego.AppConfig.String("p2p_port")
				if err := sig.WriteLenContent([]byte(svrAddr)); err != nil {
					logs.Warn("p2p write svrAddr error: %v", err)
					return
				}
				if err := sig.WriteLenContent(b); err != nil {
					logs.Warn("p2p write secret error: %v", err)
					return
				}
				//向该请求者发送建立连接请求,服务器地址
				if err := c.WriteLenContent([]byte(svrAddr)); err != nil {
					logs.Warn("p2p write requester svrAddr error: %v", err)
				}
			}
		}
	}
	c.SetAlive(s.tunnelType)
	return
}

// register ip
func (s *Bridge) register(c *conn.Conn) {
	var hour int32
	if err := binary.Read(c, binary.LittleEndian, &hour); err == nil {
		s.Register.Store(common.GetIpByAddr(c.Conn.RemoteAddr().String()), time.Now().Add(time.Hour*time.Duration(hour)))
	}
}

func (s *Bridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error) {
	//if the proxy type is local
	if link.LocalProxy {
		target, err = net.Dial("tcp", link.Host)
		return
	}
	if v, ok := s.Client.Load(clientId); ok {
		//If ip is restricted to do ip verification
		if s.ipVerify {
			ip := common.GetIpByAddr(link.RemoteAddr)
			if v, ok := s.Register.Load(ip); !ok {
				return nil, errors.New(fmt.Sprintf("The ip %s is not in the validation list", ip))
			} else {
				if !v.(time.Time).After(time.Now()) {
					return nil, errors.New(fmt.Sprintf("The validity of the ip %s has expired", ip))
				}
			}
		}
		var tunnel *nps_mux.Mux
		cl := v.(*Client)
		cl.mu.Lock()
		if t != nil && t.Mode == "file" {
			tunnel = cl.file
		} else {
			tunnel = cl.tunnel
		}
		cl.mu.Unlock()
		if tunnel == nil {
			err = errors.New("the client connect error")
			return
		}
		if target, err = tunnel.NewConn(); err != nil {
			return
		}
		if t != nil && t.Mode == "file" {
			//TODO if t.mode is file ,not use crypt or compress
			link.Crypt = false
			link.Compress = false
			return
		}
		if _, err = conn.NewConn(target).SendInfo(link, ""); err != nil {
			logs.Info("new connect error ,the target %s refuse to connect", link.Host)
			return
		}
	} else {
		err = errors.New(fmt.Sprintf("the client %d is not connect", clientId))
	}
	return
}

// SendShellResize 向客户端下发 shell 终端尺寸（带外控制消息，经 mux 独立流发送，与终端数据零混流）。
// 客户端旧版本不认识 shellresize 类型，会走默认分支忽略，不影响现有终端功能。
func (s *Bridge) SendShellResize(clientId int, shellID string, cols, rows int) error {
	link := &conn.Link{ConnType: "shellresize", ShellID: shellID, Cols: cols, Rows: rows}
	_, err := s.SendLinkInfo(clientId, link, nil)
	return err
}

func (s *Bridge) ping() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			arr := make([]int, 0)
			s.Client.Range(func(key, value interface{}) bool {
				v := value.(*Client)
				v.mu.Lock()
				tunnel := v.tunnel
				signal := v.signal
				if tunnel == nil || signal == nil {
					v.mu.Unlock()
					if v.retryTime.Add(1) >= 3 {
						arr = append(arr, key.(int))
					}
					return true
				}
				isClose := tunnel.IsClose()
				v.mu.Unlock()
				if isClose {
					arr = append(arr, key.(int))
				}
				return true
			})
			for _, v := range arr {
				logs.Info("the client %d closed", v)
				s.DelClient(v)
			}
		}
	}
}

// get config and add task from client config
func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *file.Client) {
	var fail bool
loop:
	for {
		flag, err := c.ReadFlag()
		if err != nil {
			break
		}
		switch flag {
		case common.WORK_STATUS:
			if b, err := c.GetShortContent(32); err != nil {
				break loop
			} else {
				var str string
				id, err := file.GetDb().GetClientIdByVkey(string(b))
				if err != nil {
					break loop
				}
				file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
					v := value.(*file.Host)
					if v.Client.Id == id {
						str += v.Remark + common.CONN_DATA_SEQ
					}
					return true
				})
				file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
					v := value.(*file.Tunnel)
					//if _, ok := s.runList[v.Id]; ok && v.Client.Id == id {
					if _, ok := s.runList.Load(v.Id); ok && v.Client.Id == id {
						str += v.Remark + common.CONN_DATA_SEQ
					}
					return true
				})
				binary.Write(c, binary.LittleEndian, int32(len([]byte(str))))
				binary.Write(c, binary.LittleEndian, []byte(str))
			}
		case common.NEW_CONF:
			var err error
			if client, err = c.GetConfigInfo(); err != nil {
				fail = true
				c.WriteAddFail()
				break loop
			} else {
				// 注册上限校验（F1-8）：max_clients=0 表示不限（沿用存量语义）
				if maxClients := beego.AppConfig.DefaultInt("max_clients", 0); maxClients > 0 && file.GetDb().GetClientCount() >= maxClients {
					fail = true
					c.WriteAddFail()
					logs.Warn("register rejected: client count %d >= max_clients %d", file.GetDb().GetClientCount(), maxClients)
					break loop
				}
				if err = file.GetDb().NewClient(client); err != nil {
					fail = true
					c.WriteAddFail()
					break loop
				}
				c.WriteAddOk()
				c.Write([]byte(client.VerifyKey))
				// LoadOrStore：不覆盖活跃条目，旧连接（signal/tunnel/file）显式关闭（阶段三 G6）
				if old, loaded := s.Client.LoadOrStore(client.Id, NewClient(nil, nil, nil, "")); loaded {
					cl := old.(*Client)
					cl.mu.Lock()
					if cl.signal != nil {
						_ = cl.signal.Close()
						cl.signal = nil
					}
					if cl.tunnel != nil {
						_ = cl.tunnel.Close()
						cl.tunnel = nil
					}
					if cl.file != nil {
						_ = cl.file.Close()
						cl.file = nil
					}
					cl.mu.Unlock()
				}
			}
		case common.NEW_HOST:
			h, err := c.GetHostInfo()
			if err != nil {
				fail = true
				c.WriteAddFail()
				break loop
			}
			h.Client = client
			if h.Location == "" {
				h.Location = "/"
			}
			if !client.HasHost(h) {
				if file.GetDb().IsHostExist(h) {
					fail = true
					c.WriteAddFail()
					break loop
				} else {
					file.GetDb().NewHost(h)
					c.WriteAddOk()
				}
			} else {
				c.WriteAddOk()
			}
		case common.NEW_TASK:
			if t, err := c.GetTaskInfo(); err != nil {
				fail = true
				c.WriteAddFail()
				break loop
			} else {
				ports := common.GetPorts(t.Ports)
				targets := common.GetPorts(t.Target.TargetStr)
				if len(ports) > 1 && (t.Mode == "tcp" || t.Mode == "udp") && (len(ports) != len(targets)) {
					fail = true
					c.WriteAddFail()
					break loop
				} else if t.Mode == "secret" || t.Mode == "p2p" {
					ports = append(ports, 0)
				}
				if len(ports) == 0 {
					fail = true
					c.WriteAddFail()
					break loop
				}
				for i := 0; i < len(ports); i++ {
					tl := new(file.Tunnel)
					tl.Mode = t.Mode
					tl.Port = ports[i]
					tl.ServerIp = t.ServerIp
					if len(ports) == 1 {
						tl.Target = t.Target
						tl.Remark = t.Remark
					} else {
						tl.Remark = t.Remark + "_" + strconv.Itoa(tl.Port)
						tl.Target = new(file.Target)
						if t.TargetAddr != "" {
							tl.Target.TargetStr = t.TargetAddr + ":" + strconv.Itoa(targets[i])
						} else {
							tl.Target.TargetStr = strconv.Itoa(targets[i])
						}
					}
					tl.Id = int(file.GetDb().JsonDb.GetTaskId())
					tl.Status = true
					tl.Flow = new(file.Flow)
					tl.NoStore = true
					tl.Client = client
					tl.Password = t.Password
					tl.LocalPath = t.LocalPath
					tl.StripPre = t.StripPre
					tl.MultiAccount = t.MultiAccount
					if !client.HasTunnel(tl) {
						// 每客户端隧道数上限校验（F1-8）：max_tunnels_per_client=0 表示不限
						if maxTunnels := beego.AppConfig.DefaultInt("max_tunnels_per_client", 0); maxTunnels > 0 && file.GetDb().GetTaskCountByClient(client.Id) >= maxTunnels {
							fail = true
							c.WriteAddFail()
							logs.Warn("add task rejected: client %d task count >= max_tunnels_per_client %d", client.Id, maxTunnels)
							break loop
						}
						if err := file.GetDb().NewTask(tl); err != nil {
							logs.Notice("Add task error ", err.Error())
							fail = true
							c.WriteAddFail()
							break loop
						}
						if b := tool.TestServerPort(tl.Port, tl.Mode); !b && t.Mode != "secret" && t.Mode != "p2p" {
							fail = true
							c.WriteAddFail()
							break loop
						} else {
							s.OpenTask <- tl
						}
					}
					c.WriteAddOk()
				}
			}
		}
	}
	if fail && client != nil {
		s.DelClient(client.Id)
	}
	c.Close()
}
