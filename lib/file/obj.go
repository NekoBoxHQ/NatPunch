package file

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/rate"
	"github.com/pkg/errors"
)

type Flow struct {
	ExportFlow int64
	InletFlow  int64
	FlowLimit  int64
	sync.RWMutex
}

func (s *Flow) Add(in, out int64) {
	s.Lock()
	defer s.Unlock()
	s.InletFlow += int64(in)
	s.ExportFlow += int64(out)
}

// Snapshot 一次性读回 (限额, 入, 出)。
//
// 转发路径（限速判断、流量超限检查、Dashboard）原本是直接读这三个字段，
// 而 Add 是持写锁累加的：数据竞争，且 32 位平台（armv7 / mipsle）上 int64
// 撕裂读会让限流误判（漏限或误杀）。读侧统一走这里。
func (s *Flow) Snapshot() (limit, in, out int64) {
	s.RLock()
	defer s.RUnlock()
	return s.FlowLimit, s.InletFlow, s.ExportFlow
}

// SetLimit 更新流量上限（持写锁，面板改配置走这里）。
func (s *Flow) SetLimit(limitMB int64) {
	s.Lock()
	s.FlowLimit = limitMB
	s.Unlock()
}

// SetFlows 直接改写累计流量（面板上的"清零/手工修正"走这里，同样持锁）。
func (s *Flow) SetFlows(in, out int64) {
	s.Lock()
	s.InletFlow = in
	s.ExportFlow = out
	s.Unlock()
}

type Config struct {
	U        string
	P        string
	Compress bool
	Crypt    bool
}

type Client struct {
	Cnf             *Config
	Id              int        //id
	VerifyKey       string     //verify key
	Addr            string     // client egress address as seen by server (RemoteAddr)
	LocalAddr       string     // client private/LAN addresses reported by natpunch-client
	Remark          string     //remark
	Status          bool       //is allow connect
	IsConnect       bool       //is the client connect
	RateLimit       int        //rate /kb
	Flow            *Flow      //flow setting
	Rate            *rate.Rate //rate limit
	NoStore         bool       //no store to file
	NoDisplay       bool       //no display on web
	MaxConn         int        //the max connection num of client allow
	NowConn         int32      //the connection num of now
	WebUserName     string     //the username of web login
	WebPassword     string     //the password of web login
	ConfigConnAllow bool       //is allow connected by config file
	MaxTunnelNum    int
	Version         string
	BlackIpList     []string
	CreateTime      string
	LastOnlineTime  string
	IpWhite         bool     // 是否启用ip白名单
	IpWhitePass     string   // ip授权密码
	IpWhiteList     []string // ip白名单
	ExpireTime      string   // 到期时间,留空表示永不过期,格式 2006-01-02 15:04:05
	sync.RWMutex
}

func NewClient(vKey string, noStore bool, noDisplay bool) *Client {
	return &Client{
		Cnf:       new(Config),
		Id:        0,
		VerifyKey: vKey,
		Addr:      "",
		LocalAddr: "",
		Remark:    "",
		Status:    true,
		IsConnect: false,
		RateLimit: 0,
		Flow:      new(Flow),
		Rate:      nil,
		NoStore:   noStore,
		RWMutex:   sync.RWMutex{},
		NoDisplay: noDisplay,
	}
}

func (s *Client) CutConn() {
	atomic.AddInt32(&s.NowConn, 1)
}

func (s *Client) AddConn() {
	atomic.AddInt32(&s.NowConn, -1)
}

// GetConn 尝试占用一个连接名额；check-then-increment 改为原子 CAS，
// 避免并发下突破 MaxConn 上限（阶段三 #4）
func (s *Client) GetConn() bool {
	for {
		cur := atomic.LoadInt32(&s.NowConn)
		if s.MaxConn != 0 && int(cur) >= s.MaxConn {
			return false
		}
		if atomic.CompareAndSwapInt32(&s.NowConn, cur, cur+1) {
			return true
		}
	}
}

func (s *Client) HasTunnel(t *Tunnel) (exist bool) {
	GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*Tunnel)
		if v.Client.Id == s.Id && v.Port == t.Port && t.Port != 0 {
			exist = true
			return false
		}
		return true
	})
	return
}

func (s *Client) GetTunnelNum() (num int) {
	GetDb().JsonDb.Tasks.Range(func(key, value interface{}) bool {
		v := value.(*Tunnel)
		if v.Client.Id == s.Id {
			num++
		}
		return true
	})

	GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.Client.Id == s.Id {
			num++
		}
		return true
	})
	return
}

func (s *Client) HasHost(h *Host) bool {
	var has bool
	GetDb().JsonDb.Hosts.Range(func(key, value interface{}) bool {
		v := value.(*Host)
		if v.Client.Id == s.Id && v.Host == h.Host && h.Location == v.Location {
			has = true
			return false
		}
		return true
	})
	return has
}

type Tunnel struct {
	Id           int
	Sort         int
	Port         int
	ServerIp     string
	Mode         string
	Status       bool
	RunStatus    bool
	Client       *Client
	Ports        string
	Flow         *Flow
	Password     string
	Remark       string
	TargetAddr   string
	NoStore      bool
	LocalPath    string
	StripPre     string
	ProtoVersion string
	AccessPath   string // 访问地址自定义路径后缀（仅展示用，如 /ui，不影响监听端口）
	Https        bool   // 隧道端口启用 TLS（借用面板证书，服务端终止 TLS 后转发内网）
	Target       *Target
	MultiAccount *MultiAccount
	Health
	sync.RWMutex
}

type Health struct {
	HealthCheckTimeout  int
	HealthMaxFail       int
	HealthCheckInterval int
	HealthNextTime      time.Time
	HealthMap           map[string]int
	HttpHealthUrl       string
	HealthRemoveArr     []string
	HealthCheckType     string
	HealthCheckTarget   string
	sync.RWMutex
}

type Host struct {
	Id           int
	Host         string //host
	HeaderChange string //header change
	HostChange   string //host change
	Location     string //url router
	Remark       string //remark
	Scheme       string //http https all
	CertFilePath string
	KeyFilePath  string
	NoStore      bool
	IsClose      bool
	AutoHttps    bool // 自动https
	Flow         *Flow
	Client       *Client
	Target       *Target //目标
	Health       `json:"-"`
	sync.RWMutex
}

type Target struct {
	nowIndex   int
	TargetStr  string
	TargetArr  []string
	LocalProxy bool
	sync.RWMutex
}

type MultiAccount struct {
	AccountMap map[string]string // multi account and pwd
}

func (s *Target) GetRandomTarget() (string, error) {
	s.Lock()
	if s.TargetArr == nil {
		arr := strings.Split(s.TargetStr, "\n")
		s.TargetArr = make([]string, 0, len(arr))
		for _, v := range arr {
			v = strings.TrimRight(v, "\r")
			if v != "" {
				s.TargetArr = append(s.TargetArr, v)
			}
		}
	}
	if len(s.TargetArr) == 0 {
		s.Unlock()
		return "", errors.New("all inward-bending targets are offline")
	}
	if s.nowIndex >= len(s.TargetArr)-1 {
		s.nowIndex = -1
	}
	s.nowIndex++
	addr := s.TargetArr[s.nowIndex]
	s.Unlock()
	return addr, nil
}

type Glob struct {
	BlackIpList []string
	ServerUrl   string
	sync.RWMutex
}

// ---------------------------------------------------------------------------
// 序列化即快照。
//
// 这些对象是**活对象**：网桥在客户端连上/断开时会改 Client 的
// IsConnect / Version / LastOnlineTime / Addr，服务端在启停隧道时会改
// Tunnel 的 Status / RunStatus、健康检查会改 TargetArr / HealthRemoveArr，
// 面板改路径时还会改 Host.Location。
//
// 而 json.Marshal 发生在完全不同的 goroutine 里：每次面板列表请求、
// 每次 clients.json / tasks.json 落盘（连接一断就落一次）。
//
// 两个 goroutine 同时碰同一个 string 字段是什么后果：string 头部是
// (指针, 长度) 两个字，写入不是原子的。读方可能拿到「新指针 + 旧长度」，
// 于是把相邻的堆内存当成字符串尾部一起序列化出去（列表里出现乱码，
// 而且可能把别的内存内容写进 JSON）；int64 在 32 位平台（armv7/mipsle
// 都在发布矩阵里）还会直接撕裂，读出一半旧一半新的数字。
//
// 做法：MarshalJSON 里先 RLock 再序列化「同字段、无方法」的别名类型。
// 前提是所有写方都持写锁（bridge.go / server.go / controllers 已统一）。
// 别名的字段布局与原名完全一致，所以磁盘上的 JSON 格式不变。
// ---------------------------------------------------------------------------

// MarshalJSON 见上：对活对象加读锁后再序列化。
func (s *Client) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Client
	return json.Marshal((*plain)(s))
}

// MarshalJSON 见上。
func (s *Flow) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Flow
	return json.Marshal((*plain)(s))
}

// MarshalJSON 见上。Tunnel 内嵌的 *Client 会走到它自己的 MarshalJSON。
func (s *Tunnel) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Tunnel
	return json.Marshal((*plain)(s))
}

// MarshalJSON 见上。
func (s *Host) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Host
	return json.Marshal((*plain)(s))
}

// MarshalJSON 见上。TargetArr 由健康检查在 Tunnel/Host 写锁内维护。
func (s *Target) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Target
	return json.Marshal((*plain)(s))
}

// MarshalJSON 见上。
func (s *Glob) MarshalJSON() ([]byte, error) {
	s.RLock()
	defer s.RUnlock()
	type plain Glob
	return json.Marshal((*plain)(s))
}
