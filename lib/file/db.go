package file

import (
	"crypto/md5"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/rate"
)

type DbUtils struct {
	JsonDb *JsonDb
}

var (
	Db   *DbUtils
	once sync.Once

	// 客户端/隧道数量上限（从 natpunch.conf 读取，0=不限，存量语义）。
	// 启动时由 cmd/natpunch 初始化；数据层统一校验，使 bridge 协议注册、
	// Web 自注册、面板新建客户端等所有入口一致生效（复评🟠6：此前上限只在 bridge 路径校验）
	MaxClients          int
	MaxTunnelsPerClient int
)

// init csv from file
func GetDb() *DbUtils {
	once.Do(func() {
		jsonDb := NewJsonDb(common.GetRunPath())
		jsonDb.LoadClientFromJsonFile()
		jsonDb.LoadTaskFromJsonFile()
		jsonDb.LoadHostFromJsonFile()
		jsonDb.LoadGlobalFromJsonFile()
		jsonDb.LoadQuickCmdsFromJsonFile()
		Db = &DbUtils{JsonDb: jsonDb}
	})
	return Db
}

func (s *DbUtils) GetClientList(start, length int, search, sortField, order string, clientId int) ([]*Client, int) {
	all := make([]*Client, 0)
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		noDisplay, id := v.NoDisplay, v.Id
		verifyKey, remark, addr, localAddr := v.VerifyKey, v.Remark, v.Addr, v.LocalAddr
		v.RUnlock()
		if noDisplay {
			return true
		}
		if clientId != 0 && clientId != id {
			return true
		}
		if search != "" && !(id == common.GetIntNoErrByStr(search) || strings.Contains(verifyKey, search) || strings.Contains(remark, search) || strings.Contains(addr, search) || strings.Contains(localAddr, search)) {
			return true
		}
		all = append(all, v)
		return true
	})
	SortClients(all, sortField, order)
	return paginateClients(all, start, length)
}

func paginateClients(all []*Client, start, length int) ([]*Client, int) {
	cnt := len(all)
	if start < 0 {
		start = 0
	}
	if length <= 0 {
		return []*Client{}, cnt
	}
	if start >= cnt {
		return []*Client{}, cnt
	}
	end := start + length
	if end > cnt {
		end = cnt
	}
	return all[start:end], cnt
}

func (s *DbUtils) GetIdByVerifyKey(vKey string, addr string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		verifyKey := v.VerifyKey
		v.RUnlock()
		if common.ConstantTimeStrEq(common.Getverifyval(verifyKey), vKey) {
			v.Lock()
			if !v.Status {
				v.Unlock()
				return true
			}
			v.Addr = common.GetIpByAddr(addr)
			id = v.Id
			exist = true
			v.Unlock()
			return false
		}
		return true
	})
	if exist {
		return
	}
	return 0, errors.New("not found")
}

func (s *DbUtils) NewTask(t *Tunnel) (err error) {
	if t == nil || t.Client == nil {
		return errors.New("task or client is nil")
	}
	taskCreateLock.Lock()
	defer taskCreateLock.Unlock()
	// 每客户端隧道数上限（数据层统一校验，所有入口生效；0=不限）
	if MaxTunnelsPerClient > 0 && t.Client != nil && s.GetTaskCountByClient(t.Client.Id) >= MaxTunnelsPerClient {
		return errors.New(fmt.Sprintf("client %d task count >= max_tunnels_per_client %d", t.Client.Id, MaxTunnelsPerClient))
	}
	if t.Id == 0 {
		t.Id = int(s.JsonDb.GetTaskId())
	}
	t.Flow = new(Flow)
	s.JsonDb.Tasks.Store(t.Id, t)
	if err := s.JsonDb.StoreTasksToJsonFile(); err != nil {
		s.JsonDb.Tasks.Delete(t.Id)
		return err
	}
	return nil
}

func (s *DbUtils) UpdateTask(t *Tunnel) error {
	if t == nil {
		return errors.New("task is nil")
	}
	taskCreateLock.Lock()
	defer taskCreateLock.Unlock()
	s.JsonDb.Tasks.Store(t.Id, t)
	return s.JsonDb.StoreTasksToJsonFile()
}

func (s *DbUtils) DelTask(id int) error {
	taskCreateLock.Lock()
	defer taskCreateLock.Unlock()
	s.JsonDb.Tasks.Delete(id)
	return s.JsonDb.StoreTasksToJsonFile()
}

// md5 password
func (s *DbUtils) GetTask(id int) (t *Tunnel, err error) {
	if v, ok := s.JsonDb.Tasks.Load(id); ok {
		t = v.(*Tunnel)
		return
	}
	err = errors.New("not found")
	return
}

func (s *DbUtils) DelHost(id int) error {
	hostCreateLock.Lock()
	defer hostCreateLock.Unlock()
	s.JsonDb.Hosts.Delete(id)
	return s.JsonDb.StoreHostToJsonFile()
}

func (s *DbUtils) IsHostExist(h *Host) bool {
	var exist bool
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.Id != h.Id && v.Host == h.Host && h.Location == v.Location && (v.Scheme == "all" || v.Scheme == h.Scheme) {
			exist = true
			return false
		}
		return true
	})
	return exist
}

func (s *DbUtils) NewHost(t *Host) error {
	if t == nil || t.Client == nil {
		return errors.New("host or client is nil")
	}
	hostCreateLock.Lock()
	defer hostCreateLock.Unlock()
	if t.Location == "" {
		t.Location = "/"
	}
	if s.IsHostExist(t) {
		return errors.New("host has exist")
	}
	t.Flow = new(Flow)
	s.JsonDb.Hosts.Store(t.Id, t)
	if err := s.JsonDb.StoreHostToJsonFile(); err != nil {
		s.JsonDb.Hosts.Delete(t.Id)
		return err
	}
	return nil
}

func (s *DbUtils) GetHost(start, length int, id int, search, sortField, order string) ([]*Host, int) {
	all := make([]*Host, 0)
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if search != "" {
			vkey := ""
			if v.Client != nil {
				vkey = v.Client.VerifyKey
			}
			if !(v.Id == common.GetIntNoErrByStr(search) || strings.Contains(v.Host, search) || strings.Contains(v.Remark, search) || strings.Contains(vkey, search)) {
				return true
			}
		}
		if id != 0 && (v.Client == nil || v.Client.Id != id) {
			return true
		}
		all = append(all, v)
		return true
	})
	SortHosts(all, sortField, order)
	return paginateHosts(all, start, length)
}

func paginateHosts(all []*Host, start, length int) ([]*Host, int) {
	cnt := len(all)
	if start < 0 {
		start = 0
	}
	if length <= 0 {
		return []*Host{}, cnt
	}
	if start >= cnt {
		return []*Host{}, cnt
	}
	end := start + length
	if end > cnt {
		end = cnt
	}
	return all[start:end], cnt
}

func (s *DbUtils) DelClient(id int) error {
	clientCreateLock.Lock()
	defer clientCreateLock.Unlock()
	s.JsonDb.Clients.Delete(id)
	return s.JsonDb.StoreClientsToJsonFile()
}

// GetClientCount 返回计入 max_clients 配额的客户端数量（F1-8）。
// 跳过 NoStore 条目（内置公共 vkey 客户端等合成条目，NewClient(vkey,true,true) 创建）：
// 否则合成条目会占掉一个配额，导致 max_clients=1 时第一个真实客户端就被拒、
// max_clients=N 时实际只能注册 N-1 个（复评🟠2）。
func (s *DbUtils) GetClientCount() int {
	cnt := 0
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		if c, ok := value.(*Client); ok && c.NoStore {
			return true
		}
		cnt++
		return true
	})
	return cnt
}

// GetTaskCountByClient 返回指定客户端已创建的隧道数（F1-8：max_tunnels_per_client 限额用）
func (s *DbUtils) GetTaskCountByClient(clientId int) int {
	cnt := 0
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		if v, ok := value.(*Tunnel); ok {
			v.RLock()
			client := v.Client
			v.RUnlock()
			if client != nil {
				client.RLock()
				id := client.Id
				client.RUnlock()
				if id == clientId {
					cnt++
				}
			}
		}
		return true
	})
	return cnt
}

// NewClientRate 创建一个客户端的限速器，并把「速率采样」接到客户端的累计流量上。
//
// 速率(NowRate)从 Client.Flow 的 Inlet+Export 增量算（TCP+UDP 都算），每 2 秒采一次；
// 不再依赖 rateConn 的 consumed 计数 —— 那只有 TCP，漏掉了 UDP 的代理流量（QUIC/HTTP3）。
func NewClientRate(c *Client) *rate.Rate {
	r := rate.NewRate(rateLimitAddSize(c.RateLimit))
	r.SetFlowSource(func() int64 {
		c.RLock()
		f := c.Flow
		c.RUnlock()
		if f == nil {
			return 0
		}
		f.RLock()
		defer f.RUnlock()
		return f.InletFlow + f.ExportFlow
	})
	return r
}

// rateLimitAddSize 把面板的限速值（Mbps）换算成限速器的 addSize（字节/秒）。
func rateLimitAddSize(limit int) int64 {
	if limit == 0 {
		return (2 << 23) * 1024 // 不限速：约 16 GiB/s，实际等于不限
	}
	// RateLimit 单位 Mbps（比特，1024 进制）：Mbps * 1024 * 1024 / 8 = 字节/秒
	return int64(limit * 1024 * 1024 / 8)
}

// syncClientRate 给「客户端上报配置」这条路径选出该用的限速器。
//
// 限速是**服务端**（面板）设的，客户端自报的配置里没有这一项 —— 所以老对象还在就
// 沿用它，连同它的限速器一起。（面板改限速走的是 UpdateClient，那条路径本来就是
// 只在限速值真的变了才重建、并停掉旧的。）
//
// ⚠️ 为什么不能像原来那样无条件换一个新对象：
//   - 连接是在 GetConn 那一刻把 *Rate 指针抓走的。换掉之后，**已经在跑**的连接继续
//     往旧对象里记数，而面板读的是新对象 —— 表现是"网速一直是 0，可明明在跑"；
//   - 旧对象的采样 ticker 永远不停，于是客户端每上报一次配置就泄漏一个 goroutine。
func syncClientRate(existing *Client, c *Client) {
	if existing != nil && existing != c {
		existing.RLock()
		rateLimit, clientRate := existing.RateLimit, existing.Rate
		existing.RUnlock()
		if clientRate != nil {
			c.RateLimit = rateLimit
			c.Rate = clientRate
			return
		}
	}
	if existing == c && c.Rate != nil {
		return
	}
	c.Rate = NewClientRate(c)
}

// ValidVerifyKey 校验 vkey 的字符集与长度。
//
// vkey 会原样写回客户端、落进 clients.json，还会被面板拼进「一键部署」命令
// （`sh -s -- '<vkey>'`）和 HTML 属性 —— 带引号/分号/尖括号就是命令注入或 XSS。
// 合法字符集只留字母、数字和 `._-`（自动生成的 32 位 hex 天然满足）。
func ValidVerifyKey(k string) bool {
	if len(k) < 8 || len(k) > 64 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

func (s *DbUtils) NewClient(c *Client) error {
	if c == nil {
		return errors.New("client is nil")
	}
	var isNotSet bool
	// 客户端 web 密码统一哈希：bridge 的 pub 模式注册会将客户端配置里的明文
	// WebPassword 原样带入并覆盖已哈希值。集中在此处理可覆盖全部入口；
	// 已哈希（$2 前缀）的不二次哈希，面板路径预先哈希过，同样不受影响（复评🟡）。
	if c.WebPassword != "" && !strings.HasPrefix(c.WebPassword, "$2") {
		h, herr := common.HashPassword(c.WebPassword)
		if herr != nil {
			return herr
		}
		c.WebPassword = h
	}

	// 配额检查、唯一性检查、Id 分配与写入必须作为一个注册事务串行完成。
	// 否则并发 NEW_CONF / 面板注册会同时看到“还有一个名额”，最终突破 max_clients。
	clientCreateLock.Lock()
	defer clientCreateLock.Unlock()

	existing, _ := s.GetClient(c.Id)
	isNew := existing == nil
	if MaxClients > 0 && !c.NoStore && isNew && s.GetClientCount() >= MaxClients {
		return errors.New(fmt.Sprintf("client count %d >= max_clients %d", s.GetClientCount(), MaxClients))
	}
	if c.WebUserName != "" && !s.VerifyUserName(c.WebUserName, c.Id) {
		return errors.New("web login username duplicate, please reset")
	}
reset:
	if c.VerifyKey == "" || isNotSet {
		isNotSet = true
		c.VerifyKey = crypt.GetVkey()
	}
	syncClientRate(existing, c)
	c.Rate.Start()
	if !s.VerifyVkey(c.VerifyKey, c.Id) {
		if isNotSet {
			goto reset
		}
		return errors.New("Vkey duplicate, please reset")
	}
	// 分配 Id 与 Store 必须在同一把锁内：原来 GetClientId() 只锁"扫描+返回"，
	// 拿到号之后才 Store —— 两个并发注册会取到同一个空号，后写覆盖先写。
	clientIdLock.Lock()
	if c.Id == 0 {
		c.Id = int(s.JsonDb.nextClientIdLocked())
	}
	if c.Flow == nil {
		c.Flow = new(Flow)
	}
	s.JsonDb.Clients.Store(c.Id, c)
	clientIdLock.Unlock()
	// 落盘失败必须冒泡：调用方（面板/桥接注册）以为保存成功，重启后这条记录就没了。
	if err := s.JsonDb.StoreClientsToJsonFile(); err != nil {
		if existing != nil {
			s.JsonDb.Clients.Store(existing.Id, existing)
		} else {
			s.JsonDb.Clients.Delete(c.Id)
		}
		return err
	}
	return nil
}

func (s *DbUtils) VerifyVkey(vkey string, id int) (res bool) {
	res = true
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		existingKey, existingID := v.VerifyKey, v.Id
		v.RUnlock()
		if existingKey == vkey && existingID != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) VerifyUserName(username string, id int) (res bool) {
	res = true
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		existingName, existingID := v.WebUserName, v.Id
		v.RUnlock()
		if existingName == username && existingID != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) UpdateClient(t *Client) error {
	if t == nil {
		return errors.New("client is nil")
	}
	clientCreateLock.Lock()
	defer clientCreateLock.Unlock()
	// 先取旧条目：既要比较 RateLimit 变化，也要停掉被替换对象的限速器
	var old *Client
	if v, ok := s.JsonDb.Clients.Load(t.Id); ok {
		old = v.(*Client)
	}
	oldRateLimit := -1
	var oldRate *rate.Rate
	if old != nil && old != t {
		old.RLock()
		oldRateLimit = old.RateLimit
		oldRate = old.Rate
		old.RUnlock()
	}

	t.Lock()
	if old == t {
		oldRateLimit = t.RateLimit
		oldRate = t.Rate
	}
	// web 密码防呆：与 NewClient 同口径，避免经编辑接口写入明文（复评🟡）
	if t.WebPassword != "" && !strings.HasPrefix(t.WebPassword, "$2") {
		h, herr := common.HashPassword(t.WebPassword)
		if herr != nil {
			t.Unlock()
			return herr
		}
		t.WebPassword = h
	}

	s.JsonDb.Clients.Store(t.Id, t)
	var newRate *rate.Rate

	// 仅在限速值变化或新对象尚未持有限速器时重建。
	// 原实现只在 RateLimit==0 时重建，导致「从不限速改为限速」不生效（复评🟡）。
	if oldRateLimit != t.RateLimit || t.Rate == nil {
		newRate = NewClientRate(t)
		t.Rate = newRate
	}
	t.Unlock()

	// 服务端主动轮换限速器时停掉旧采样器，避免 ticker 泄漏。
	if newRate != nil {
		if oldRate != nil && oldRate != newRate {
			oldRate.Stop()
		}
		newRate.Start()
	}
	// UpdateClient 是“写入并持久化”的统一入口。只改内存却返回 nil 会让调用方
	//（IP 白名单授权等）误以为已经保存；flow_store_interval=0 时甚至永远不会落盘。
	return s.JsonDb.StoreClientsToJsonFile()
}

func (s *DbUtils) IsPubClient(id int) bool {
	client, err := s.GetClient(id)
	if err == nil {
		return client.NoDisplay
	}
	return false
}

func (s *DbUtils) GetClient(id int) (c *Client, err error) {
	if v, ok := s.JsonDb.Clients.Load(id); ok {
		c = v.(*Client)
		return
	}
	err = errors.New("未找到客户端")
	return
}

func (s *DbUtils) GetGlobal() (c *Glob) {
	return s.JsonDb.Global
}

// SaveQuickCmds 持久化快捷命令（面板服务器存储）
func (s *DbUtils) SaveQuickCmds(q *QuickCmds) error {
	// Quick 是**裸指针**字段：原来的写法在并发「保存」与「读取」之间是数据竞争
	// （两个管理员同时点保存、或保存的同时有人在开终端页）。用一个锁把指针换手护住。
	s.JsonDb.quickMu.Lock()
	s.JsonDb.Quick = q
	s.JsonDb.quickMu.Unlock()
	return s.JsonDb.StoreQuickCmdsToJsonFile()
}

func (s *DbUtils) GetQuickCmds() *QuickCmds {
	s.JsonDb.quickMu.Lock()
	defer s.JsonDb.quickMu.Unlock()
	if s.JsonDb.Quick == nil {
		s.JsonDb.Quick = &QuickCmds{Customs: []QuickCmdItem{}, Hidden: []string{}}
	}
	return s.JsonDb.Quick
}

func (s *DbUtils) GetClientIdByVkey(vkey string) (id int, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		verifyKey := v.VerifyKey
		v.RUnlock()
		if common.ConstantTimeStrEq(crypt.Md5(verifyKey), vkey) {
			exist = true
			id = v.Id
			return false
		}
		return true
	})
	if exist {
		return
	}
	err = errors.New("未找到客户端")
	return
}

func (s *DbUtils) GetClientByVkey(vkey string) (c *Client, err error) {
	var exist bool
	s.JsonDb.Clients.Range(func(key, value interface{}) bool {
		v := value.(*Client)
		v.RLock()
		verifyKey := v.VerifyKey
		v.RUnlock()
		// 与 GetClientIdByVkey 同口径：vkey 是连接凭据，比较走恒定时间，
		// 别让响应时间泄露"前缀对了几位"。
		if common.ConstantTimeStrEq(fmt.Sprintf("%x", md5.Sum([]byte(verifyKey))), vkey) {
			exist = true
			c = v
			return false
		}
		return true
	})
	if exist {
		return
	}
	err = errors.New("未找到客户端")
	return
}

// SetStatus 改「是否允许连接」标志（持写锁）。
func (c *Client) SetStatus(status bool) {
	c.Lock()
	c.Status = status
	c.Unlock()
}

// AddIpWhite 把 ip 加进白名单；已存在返回 false。持写锁，
// 避免与「列表序列化 / clients.json 落盘」并发读写同一 slice。
func (c *Client) AddIpWhite(ip string) bool {
	c.Lock()
	defer c.Unlock()
	for _, v := range c.IpWhiteList {
		if v == ip {
			return false
		}
	}
	c.IpWhiteList = append(c.IpWhiteList, ip)
	return true
}

func (c *Client) RemoveIpWhite(ip string) {
	c.Lock()
	defer c.Unlock()
	c.IpWhiteList = common.RemoveArrVal(c.IpWhiteList, ip)
}

func (s *DbUtils) GetHostById(id int) (h *Host, err error) {
	if v, ok := s.JsonDb.Hosts.Load(id); ok {
		h = v.(*Host)
		return
	}
	err = errors.New("The host could not be parsed")
	return
}

// get key by host from x
func (s *DbUtils) GetInfoByHost(host string, r *http.Request) (h *Host, err error) {
	var hosts []*Host
	//Handling Ported Access
	host = common.GetIpByAddr(host)
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.IsClose {
			return true
		}
		//Remove http(s) http(s)://a.proxy.com
		//*.proxy.com *.a.proxy.com  Do some pan-parsing
		if v.Scheme != "all" && v.Scheme != r.URL.Scheme {
			return true
		}
		tmpHost := v.Host
		if strings.Contains(tmpHost, "*") {
			tmpHost = strings.Replace(tmpHost, "*", "", -1)
			if strings.Contains(host, tmpHost) {
				hosts = append(hosts, v)
			}
		} else if v.Host == host {
			hosts = append(hosts, v)
		}
		return true
	})

	for _, v := range hosts {
		// 默认匹配所有路径。**不要**把 "/" 写回 v.Location：
		// 这是每个请求都会走的读路径，写共享对象会和面板编辑 Host 打架（数据竞争）。
		loc := v.Location
		if loc == "" {
			loc = "/"
		}
		// "*" means SNI-based HTTPS lookup where actual URI is unknown, skip location filter
		if r.RequestURI == "*" {
			if h == nil || (len(loc) > len(h.Location)) {
				h = v
			}
			continue
		}
		if strings.Index(r.RequestURI, loc) == 0 {
			if h == nil || (len(loc) > len(h.Location)) {
				h = v
			}
		}
	}
	if h != nil {
		return
	}
	err = errors.New("The host could not be parsed")
	return
}
