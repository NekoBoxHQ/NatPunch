package server

import (
	"ehang.io/nps/lib/version"
	"errors"
	"math"
	stdnet "net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ehang.io/nps/bridge"
	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/server/proxy"
	"ehang.io/nps/server/tool"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

var (
	Bridge  *bridge.Bridge
	RunList sync.Map //map[int]interface{}
	once    sync.Once
)

func init() {
	RunList = sync.Map{}
}

// init task from db
func InitFromCsv() {
	//Add a public password
	if vkey := beego.AppConfig.String("public_vkey"); vkey != "" {
		c := file.NewClient(vkey, true, true)
		file.GetDb().NewClient(c)
		RunList.Store(c.Id, nil)
		//RunList[c.Id] = nil
	}
	//Initialize services in server-side files
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if value.(*file.Tunnel).Status {
			AddTask(value.(*file.Tunnel))
		}
		return true
	})
}

// get bridge command
func DealBridgeTask() {
	for {
		select {
		case t := <-Bridge.OpenTask:
			StartTask(t.Id)
		case t := <-Bridge.CloseTask:
			StopServer(t.Id)
		case id := <-Bridge.CloseClient:
			DelTunnelAndHostByClientId(id, true)
			if v, ok := file.GetDb().JsonDb.Clients.Load(id); ok {
				if v.(*file.Client).NoStore {
					file.GetDb().DelClient(id)
				}
			}
		case s := <-Bridge.SecretChan:
			logs.Trace("New secret connection, addr", s.Conn.Conn.RemoteAddr())
			if t := file.GetDb().GetTaskByMd5Password(s.Password); t != nil {
				if t.Status {
					go proxy.NewBaseServer(Bridge, t).DealClient(s.Conn, t.Client, t.Target.TargetStr, nil, common.CONN_TCP, nil, t.Flow, t.Target.LocalProxy, nil, nil)
				} else {
					s.Conn.Close()
					logs.Trace("This key %s cannot be processed,status is close", s.Password)
				}
			} else {
				logs.Trace("This key %s cannot be processed", s.Password)
				s.Conn.Close()
			}
		}
	}
}

// start a new server
func StartNewServer(bridgePort int, cnf *file.Tunnel, bridgeType string, bridgeDisconnect int) {
	Bridge = bridge.NewTunnel(bridgePort, bridgeType, common.GetBoolByStr(beego.AppConfig.String("ip_limit")), &RunList, bridgeDisconnect)
	// 启动流量持久化（只启动一次，避免每次 AddTask 创建泄漏的 goroutine）
	if minute, err := beego.AppConfig.Int("flow_store_interval"); err == nil && minute > 0 {
		go flowSession(time.Minute * time.Duration(minute))
	}
	// 启动后台 IO 速率采集，Dashboard 直接读缓存，无需 Sleep
	tool.StartIORateCollector()
	// 启动公网 IPv4 定时刷新（30 分钟自动重查，失败保留旧值）
	startPublicIPRefresher()
	go func() {
		if err := Bridge.StartTunnel(); err != nil {
			logs.Error("start server bridge error", err)
			os.Exit(0)
		}
	}()
	if p, err := beego.AppConfig.Int("p2p_port"); err == nil {
		go proxy.NewP2PServer(p).Start()
		go proxy.NewP2PServer(p + 1).Start()
		go proxy.NewP2PServer(p + 2).Start()
	}
	go DealBridgeTask()
	go dealClientFlow()
	go dealClientExpire()
	if svr := NewMode(Bridge, cnf); svr != nil {
		if err := svr.Start(); err != nil {
			logs.Error(err)
		}
		RunList.Store(cnf.Id, svr)
		//RunList[cnf.Id] = svr
	} else {
		logs.Error("Incorrect startup mode %s", cnf.Mode)
	}
}

func dealClientFlow() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			dealClientData()
		}
	}
}

// dealClientExpire 周期性扫描客户端到期时间，过期则自动暂停
func dealClientExpire() {
	// 启动时立即检查一次，避免重启后到期客户端最多 1 分钟内才被暂停
	checkClientExpire()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			checkClientExpire()
		}
	}
}

// checkClientExpire 遍历所有客户端，若 ExpireTime 已过则将 Status 置为 false 并断开连接
func checkClientExpire() {
	now := time.Now()
	changed := false
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v, ok := value.(*file.Client)
		if !ok || v == nil {
			return true
		}
		if v.ExpireTime == "" || !v.Status {
			return true
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05", v.ExpireTime, time.Local)
		if err != nil {
			return true
		}
		if now.Before(t) {
			return true
		}
		v.Status = false
		changed = true
		logs.Info("client id %d (remark: %s) expired at %s, auto paused", v.Id, v.Remark, v.ExpireTime)
		DelClientConnect(v.Id)
		return true
	})
	if changed {
		file.GetDb().JsonDb.StoreClientsToJsonFile()
	}
}

// multiService 组合服务：TCP+UDP 双协议同时监听（单条隧道）
type multiService struct {
	svcs []proxy.Service
}

func (m *multiService) Start() error {
	for _, svc := range m.svcs {
		go svc.Start()
	}
	return nil
}

func (m *multiService) Close() error {
	var firstErr error
	for _, svc := range m.svcs {
		if err := svc.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// new a server by mode name
func NewMode(Bridge *bridge.Bridge, c *file.Tunnel) proxy.Service {
	var service proxy.Service
	switch c.Mode {
	case "tcp", "file":
		service = proxy.NewTunnelModeServer(proxy.ProcessTunnel, Bridge, c)
	case "socks5":
		service = proxy.NewSock5ModeServer(Bridge, c)
	case "httpProxy":
		service = proxy.NewTunnelModeServer(proxy.ProcessHttp, Bridge, c)
	case "tcpTrans":
		service = proxy.NewTunnelModeServer(proxy.HandleTrans, Bridge, c)
	case "udp":
		service = proxy.NewUdpModeServer(Bridge, c)
	case "tcp+udp":
		// 避免拷贝含 sync.RWMutex 的 Tunnel 结构（复制锁），逐字段构造两个独立副本
		tcpT := &file.Tunnel{
			Id: c.Id, Sort: c.Sort, Port: c.Port, ServerIp: c.ServerIp,
			Mode: "tcp", Status: c.Status, RunStatus: c.RunStatus, Client: c.Client,
			Ports: c.Ports, Flow: c.Flow, Password: c.Password, Remark: c.Remark,
			TargetAddr: c.TargetAddr, NoStore: c.NoStore, LocalPath: c.LocalPath,
			StripPre: c.StripPre, ProtoVersion: c.ProtoVersion, Target: c.Target,
			MultiAccount: c.MultiAccount,
			Health: file.Health{
				HealthCheckTimeout:  c.HealthCheckTimeout,
				HealthMaxFail:       c.HealthMaxFail,
				HealthCheckInterval: c.HealthCheckInterval,
				HealthNextTime:      c.HealthNextTime,
				HealthMap:           c.HealthMap,
				HttpHealthUrl:       c.HttpHealthUrl,
				HealthRemoveArr:     c.HealthRemoveArr,
				HealthCheckType:     c.HealthCheckType,
				HealthCheckTarget:   c.HealthCheckTarget,
			},
		}
		udpT := &file.Tunnel{
			Id: c.Id, Sort: c.Sort, Port: c.Port, ServerIp: c.ServerIp,
			Mode: "udp", Status: c.Status, RunStatus: c.RunStatus, Client: c.Client,
			Ports: c.Ports, Flow: c.Flow, Password: c.Password, Remark: c.Remark,
			TargetAddr: c.TargetAddr, NoStore: c.NoStore, LocalPath: c.LocalPath,
			StripPre: c.StripPre, ProtoVersion: c.ProtoVersion, Target: c.Target,
			MultiAccount: c.MultiAccount,
			Health: file.Health{
				HealthCheckTimeout:  c.HealthCheckTimeout,
				HealthMaxFail:       c.HealthMaxFail,
				HealthCheckInterval: c.HealthCheckInterval,
				HealthNextTime:      c.HealthNextTime,
				HealthMap:           c.HealthMap,
				HttpHealthUrl:       c.HttpHealthUrl,
				HealthRemoveArr:     c.HealthRemoveArr,
				HealthCheckType:     c.HealthCheckType,
				HealthCheckTarget:   c.HealthCheckTarget,
			},
		}
		service = &multiService{svcs: []proxy.Service{
			proxy.NewTunnelModeServer(proxy.ProcessTunnel, Bridge, tcpT),
			proxy.NewUdpModeServer(Bridge, udpT),
		}}
	case "webServer":
		InitFromCsv()
		t := &file.Tunnel{
			Port:   0,
			Mode:   "httpHostServer",
			Status: true,
		}
		AddTask(t)
		service = proxy.NewWebServer(Bridge)
	case "httpHostServer":
		httpPort, _ := beego.AppConfig.Int("http_proxy_port")
		httpsPort, _ := beego.AppConfig.Int("https_proxy_port")
		useCache, _ := beego.AppConfig.Bool("http_cache")
		cacheLen, _ := beego.AppConfig.Int("http_cache_length")
		addOrigin, _ := beego.AppConfig.Bool("http_add_origin_header")
		service = proxy.NewHttp(Bridge, c, httpPort, httpsPort, useCache, cacheLen, addOrigin)
	}
	return service
}

// stop server
func StopServer(id int) error {
	if v, ok := RunList.Load(id); ok {
		if svr, ok := v.(proxy.Service); ok {
			if err := svr.Close(); err != nil {
				logs.Error("stop server id %d error", id, err)
			}
		} else {
			logs.Warn("stop server id %d error", id)
		}
		RunList.Delete(id)
		if t, err := file.GetDb().GetTask(id); err == nil {
			t.Status = false
			logs.Info("close port %d,remark %s,client id %d,task id %d", t.Port, t.Remark, t.Client.Id, t.Id)
			file.GetDb().UpdateTask(t)
		}
		return nil
	}
	return errors.New("task is not running")
}

// add task
func AddTask(t *file.Tunnel) error {
	if t.Mode == "secret" || t.Mode == "p2p" {
		logs.Info("secret task %s start ", t.Remark)
		//RunList[t.Id] = nil
		RunList.Store(t.Id, nil)
		return nil
	}
	if t.Mode != "httpHostServer" {
		if t.Mode == "tcp+udp" {
			if !tool.TestServerPort(t.Port, "tcp") || !tool.TestServerPort(t.Port, "udp") {
				logs.Error("taskId %d start error port %d open failed", t.Id, t.Port)
				return errors.New("the port open error")
			}
		} else if b := tool.TestServerPort(t.Port, t.Mode); !b {
			logs.Error("taskId %d start error port %d open failed", t.Id, t.Port)
			return errors.New("the port open error")
		}
	}
	if svr := NewMode(Bridge, t); svr != nil {
		logs.Info("tunnel task %s start mode：%s port %d", t.Remark, t.Mode, t.Port)
		//RunList[t.Id] = svr
		RunList.Store(t.Id, svr)
		go func() {
			if err := svr.Start(); err != nil {
				logs.Error("clientId %d taskId %d start error %s", t.Client.Id, t.Id, err)
				//delete(RunList, t.Id)
				RunList.Delete(t.Id)
				return
			}
		}()
	} else {
		return errors.New("the mode is not correct")
	}
	return nil
}

// start task
func StartTask(id int) error {
	t, err := file.GetDb().GetTask(id)
	if err != nil {
		return err
	}
	if err := AddTask(t); err != nil {
		return err
	}
	t.Status = true
	file.GetDb().UpdateTask(t)
	return nil
}

// delete task
func DelTask(id int) error {
	//if _, ok := RunList[id]; ok {
	if _, ok := RunList.Load(id); ok {
		if err := StopServer(id); err != nil {
			return err
		}
	}
	return file.GetDb().DelTask(id)
}

// ReorderTasks 按新顺序重新分配 Sort（拖拽排序）
func ReorderTasks(ids []int) error {
	for newPos, oldId := range ids {
		if t, err := file.GetDb().GetTask(oldId); err == nil {
			t.Sort = newPos + 1
			file.GetDb().UpdateTask(t)
		}
	}
	return nil
}

// get task list by page num
func GetTunnel(start, length int, typeVal string, clientId int, search string, sortField string, order string) ([]*file.Tunnel, int) {
	all_list := make([]*file.Tunnel, 0)

	// collect + fill runtime fields before sort/paginate
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*file.Tunnel)
		if typeVal == "tcp+udp" {
			if v.Mode != "tcp+udp" && v.Mode != "tcp" && v.Mode != "udp" {
				return true
			}
		} else if (typeVal != "" && v.Mode != typeVal || (clientId != 0 && v.Client.Id != clientId)) || (typeVal == "" && clientId != v.Client.Id) {
			return true
		}
		if search != "" {
			targetStr := ""
			if v.Target != nil {
				targetStr = v.Target.TargetStr
			}
			if !(v.Id == common.GetIntNoErrByStr(search) || v.Port == common.GetIntNoErrByStr(search) || strings.Contains(v.Password, search) || strings.Contains(v.Remark, search) || strings.Contains(targetStr, search)) {
				return true
			}
		}
		if v.Client != nil {
			if _, ok := Bridge.Client.Load(v.Client.Id); ok {
				v.Client.IsConnect = true
			} else {
				v.Client.IsConnect = false
			}
		}
		if _, ok := RunList.Load(v.Id); ok {
			v.RunStatus = true
		} else {
			v.RunStatus = false
		}
		all_list = append(all_list, v)
		return true
	})

	file.SortTunnels(all_list, sortField, order)

	cnt := len(all_list)
	if start < 0 {
		start = 0
	}
	if length <= 0 || start >= cnt {
		return []*file.Tunnel{}, cnt
	}
	end := start + length
	if end > cnt {
		end = cnt
	}
	return all_list[start:end], cnt
}

// get client list
func GetClientList(start, length int, search, sortField, order string, clientId int) (list []*file.Client, cnt int) {
	// fill IsConnect / Version before sort so IsConnect ordering is correct
	dealClientData()
	list, cnt = file.GetDb().GetClientList(start, length, search, sortField, order, clientId)
	return
}

// GetHostList returns hosts with client online status filled before sort/pagination.
func GetHostList(start, length int, clientId int, search, sortField, order string) ([]*file.Host, int) {
	// ensure Client.IsConnect is current for sort and display
	dealClientData()
	return file.GetDb().GetHost(start, length, clientId, search, sortField, order)
}

func dealClientData() {

	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if vv, ok := Bridge.Client.Load(v.Id); ok {
			v.IsConnect = true
			v.LastOnlineTime = time.Now().Format("2006-01-02 15:04:05")
			v.Version = vv.(*bridge.Client).Version
		} else {
			v.IsConnect = false
		}

		return true
	})
	return
}

// delete all host and tasks by client id
func DelTunnelAndHostByClientId(clientId int, justDelNoStore bool) {
	var ids []int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*file.Tunnel)
		if justDelNoStore && !v.NoStore {
			return true
		}
		if v.Client.Id == clientId {
			ids = append(ids, v.Id)
		}
		return true
	})
	for _, id := range ids {
		DelTask(id)
	}
	ids = ids[:0]
	file.GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*file.Host)
		if justDelNoStore && !v.NoStore {
			return true
		}
		if v.Client.Id == clientId {
			ids = append(ids, v.Id)
		}
		return true
	})
	for _, id := range ids {
		file.GetDb().DelHost(id)
	}
}

// close the client
func DelClientConnect(clientId int) {
	Bridge.DelClient(clientId)
}

func GetDashboardData() map[string]interface{} {
	data := make(map[string]interface{})
	data["version"] = version.VERSION
	data["hostCount"] = common.GeSynctMapLen(&file.GetDb().JsonDb.Hosts)
	data["clientCount"] = common.GeSynctMapLen(&file.GetDb().JsonDb.Clients)
	if beego.AppConfig.String("public_vkey") != "" { //remove public vkey
		data["clientCount"] = data["clientCount"].(int) - 1
	}
	dealClientData()
	c := 0
	var in, out int64
	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*file.Client)
		if v.IsConnect {
			c += 1
		}
		in += v.Flow.InletFlow
		out += v.Flow.ExportFlow
		return true
	})
	data["clientOnlineCount"] = c
	data["inletFlowCount"] = int(in)
	data["exportFlowCount"] = int(out)
	var tcp, udp, secret, socks5, p2p, http int
	file.GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		switch value.(*file.Tunnel).Mode {
		case "tcp", "tcp+udp":
			tcp += 1
		case "socks5":
			socks5 += 1
		case "httpProxy":
			http += 1
		case "udp":
			udp += 1
		case "p2p":
			p2p += 1
		case "secret":
			secret += 1
		}
		return true
	})

	data["tcpC"] = tcp
	data["udpCount"] = udp
	data["socks5Count"] = socks5
	data["httpProxyCount"] = http
	data["secretCount"] = secret
	data["p2pCount"] = p2p
	data["bridgeType"] = beego.AppConfig.String("bridge_type")
	data["httpProxyPort"] = beego.AppConfig.String("http_proxy_port")
	data["httpsProxyPort"] = beego.AppConfig.String("https_proxy_port")
	data["ipLimit"] = beego.AppConfig.String("ip_limit")
	data["flowStoreInterval"] = beego.AppConfig.String("flow_store_interval")
	localV4, localV6 := "", ""
	if conn, err := stdnet.Dial("udp", "223.5.5.5:80"); err == nil {
		if udpAddr, ok := conn.LocalAddr().(*stdnet.UDPAddr); ok {
			localV4 = udpAddr.IP.String()
		}
		conn.Close()
	}
	// NAT 环境：本机是内网 IP，后台查询公网 IP（不阻塞页面；此后由定时刷新器接管）
	if isPrivateIP(localV4) && cachedPublicIP() == "" {
		go func() {
			if ip := getPublicIPByHTTP(); ip != "" {
				storePublicIP(ip)
			}
		}()
	}
	// 获取本机 IPv6（全局单播）
	if addrs, err := stdnet.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*stdnet.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() == nil && ipNet.IP.IsGlobalUnicast() {
					if localV6 == "" {
						localV6 = ipNet.IP.String()
					}
				}
			}
		}
	}
	if localV4 == "" {
		localV4 = beego.AppConfig.String("p2p_ip")
	}
	displayV4 := localV4
	if ip := cachedPublicIP(); ip != "" {
		displayV4 = ip
	}
	data["serverIp"] = displayV4
	data["serverIpv6"] = localV6
	data["p2pPort"] = beego.AppConfig.String("p2p_port")
	data["logLevel"] = beego.AppConfig.String("log_level")
	tcpCount := 0

	file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
		tcpCount += int(value.(*file.Client).NowConn)
		return true
	})
	data["tcpCount"] = tcpCount
	cpuPercet, _ := cpu.Percent(0, true)
	var cpuAll float64
	for _, v := range cpuPercet {
		cpuAll += v
	}
	loads, _ := load.Avg()
	data["load"] = loads.String()
	data["cpu"] = math.Round(cpuAll / float64(len(cpuPercet)))
	swap, _ := mem.SwapMemory()
	data["swap_mem"] = math.Round(swap.UsedPercent)
	vir, _ := mem.VirtualMemory()
	data["virtual_mem"] = math.Round(vir.UsedPercent)
	// IO 速率从后台缓存读取，无需 Sleep 500ms
	if cached, ok := tool.IORateCache.Load().(map[string]uint64); ok {
		data["io_send"] = cached["io_send"]
		data["io_recv"] = cached["io_recv"]
	}
	conn, _ := net.ProtoCounters(nil)
	for _, v := range conn {
		data[v.Protocol] = v.Stats["CurrEstab"]
	}
	//chart
	var fg int
	tool.ServerStatusMu.RLock()
	statusLen := len(tool.ServerStatus)
	if statusLen >= 10 {
		fg = statusLen / 10
		for i := 0; i <= 9; i++ {
			data["sys"+strconv.Itoa(i+1)] = tool.ServerStatus[i*fg]
		}
	}
	tool.ServerStatusMu.RUnlock()
	return data
}

// 实例化流量数据到文件
func flowSession(m time.Duration) {
	once.Do(func() {
		ticker := time.NewTicker(m)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				file.GetDb().JsonDb.StoreHostToJsonFile()
				file.GetDb().JsonDb.StoreTasksToJsonFile()
				file.GetDb().JsonDb.StoreClientsToJsonFile()
				file.GetDb().JsonDb.StoreGlobalToJsonFile()
			}
		}
	})
}

// isPrivateIP 判断是否为内网 IP
func isPrivateIP(ip string) bool {
	if ip == "" {
		return true
	}
	return strings.HasPrefix(ip, "10.") ||
		strings.HasPrefix(ip, "172.16.") ||
		strings.HasPrefix(ip, "172.17.") ||
		strings.HasPrefix(ip, "172.18.") ||
		strings.HasPrefix(ip, "172.19.") ||
		strings.HasPrefix(ip, "172.2") ||
		strings.HasPrefix(ip, "172.30.") ||
		strings.HasPrefix(ip, "172.31.") ||
		strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "127.")
}

// publicIPCache 缓存公网 IPv4（atomic.Value：页面请求无锁读，后台定期刷新，永不阻塞面板）
var publicIPCache atomic.Value // ipEntry

// ipEntry 公网 IPv4 缓存条目
type ipEntry struct {
	ip      string
	updated time.Time
}

const publicIPRefreshInterval = 30 * time.Minute

// cachedPublicIP 读取缓存（未初始化返回空串）
func cachedPublicIP() string {
	v := publicIPCache.Load()
	if v == nil {
		return ""
	}
	return v.(ipEntry).ip
}

// storePublicIP 写入缓存
func storePublicIP(ip string) {
	publicIPCache.Store(ipEntry{ip: ip, updated: time.Now()})
}

// startPublicIPRefresher 后台定期刷新公网 IPv4；查询失败保留旧值，下一周期重试
func startPublicIPRefresher() {
	go func() {
		for {
			time.Sleep(publicIPRefreshInterval)
			if ip := getPublicIPByHTTP(); ip != "" {
				storePublicIP(ip)
			}
		}
	}()
}

// getPublicIPByHTTP 后台查公网 IPv4（不阻塞请求，多源探测）
func getPublicIPByHTTP() string {
	urls := []string{
		"https://ip.3322.net", // 国内可达，优先
		"https://api.ipify.org",
		"https://ipinfo.io/ip",
	}
	// 禁用环境代理：公网 IP 查询必须走本机真实出口，避免升级/系统代理干扰显示
	client := http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	for _, u := range urls {
		if resp, err := client.Get(u); err == nil {
			buf := make([]byte, 64)
			n, _ := resp.Body.Read(buf)
			resp.Body.Close()
			if n > 0 {
				ip := strings.TrimSpace(string(buf[:n]))
				if parsed := stdnet.ParseIP(ip); parsed != nil && parsed.To4() != nil && !isPrivateIP(ip) {
					return ip
				}
			}
		}
	}
	return ""
}
