package proxy

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/astaxie/beego/logs"
)

// provider 条目超过该时长未更新则视为失效并重建（F1-4：防 map 无限增长）
const p2pEntryTTL = 60 * time.Second

// p2pMaxEntries 并发 p2p 条目数硬上限。
// TTL 清扫只能约束"闲置"条目，无法约束"新 key 的到达速率"：攻击者以连续不同 key
// 发包时 map 仍会增长到 速率×TTL（例：10k key/s → 约 60 万条目）。此处直接封顶（复评🟠3）。
const p2pMaxEntries = 4096

type P2PServer struct {
	BaseServer
	p2pPort  int
	p2p      map[string]*p2p
	mu       sync.Mutex // 保护 p2p map 与条目字段（F1-4）
	listener *net.UDPConn
	srcPools *sourcePoolSet // 按源地址分池的 goroutine 调度（F1-7）
}

// p2pPacket 池化任务载体
type p2pPacket struct {
	addr *net.UDPAddr
	str  string
}

// p2pPoolWorker 由单源 ants 池调用，等价于原先的每包 goroutine（F1-7）。
func (s *P2PServer) p2pPoolWorker(item interface{}) {
	p := item.(*p2pPacket)
	s.handleP2P(p.addr, p.str)
}

type p2p struct {
	visitorAddr  *net.UDPAddr
	providerAddr *net.UDPAddr
	lastSeen     time.Time // 条目最近活动时间（F1-4）
}

func NewP2PServer(p2pPort int) *P2PServer {
	return &P2PServer{
		p2pPort: p2pPort,
		p2p:     make(map[string]*p2p),
	}
}

// cleanupLoop 后台清扫：TTL 仅在条目被再次访问时评估，若无后台清扫，
// 攻击者向 p2p 端口发 "<key>|provider" 即可永久创建条目导致 map 无界增长（复评🔴2）
func (s *P2PServer) cleanupLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for k, v := range s.p2p {
				if now.Sub(v.lastSeen) > p2pEntryTTL {
					delete(s.p2p, k)
				}
			}
			s.mu.Unlock()
		case <-stop:
			return
		}
	}
}

// Close 关闭监听并停止后台清扫（进程退出/服务回收时调用）
func (s *P2PServer) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.srcPools != nil {
		s.srcPools.Close()
	}
}

func (s *P2PServer) Start() error {
	logs.Info("start p2p server port", s.p2pPort)
	var err error
	s.listener, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: s.p2pPort})
	if err != nil {
		return err
	}
	s.srcPools = newSourcePoolSet(srcPoolCapacity, srcPoolMaxPools, s.p2pPoolWorker)
	stop := make(chan struct{})
	go s.cleanupLoop(stop)
	defer close(stop)
	for {
		buf := common.BufPoolUdp.Get().([]byte)
		n, addr, err := s.listener.ReadFromUDP(buf)
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				break
			}
			continue
		}
		// 按源分池调度：池满/超上限立即丢包（F1-7）
		if !s.srcPools.Submit(addr.String(), &p2pPacket{addr: addr, str: string(buf[:n])}) {
			logs.Warn("p2p source pool full or over limit, drop packet from %s (dropped total %d)", addr.String(), s.srcPools.Dropped())
			continue
		}
	}
	return nil
}

func (s *P2PServer) handleP2P(addr *net.UDPAddr, str string) {
	arr := strings.Split(str, common.CONN_DATA_SEQ)
	if len(arr) < 2 {
		return
	}
	s.mu.Lock()
	v, ok := s.p2p[arr[0]]
	if !ok && len(s.p2p) >= p2pMaxEntries {
		// 新 key 且已达硬上限：丢弃。防止"不同 key 高速发包"绕过 TTL 清扫（复评🟠3）
		s.mu.Unlock()
		logs.Warn("p2p entry table full (%d entries), drop packet from %s", p2pMaxEntries, addr.String())
		return
	}
	if !ok || time.Since(v.lastSeen) > p2pEntryTTL {
		// 新条目或条目已失效：重建，防止 provider 条目无限增长（F1-4）
		v = &p2p{lastSeen: time.Now()}
		s.p2p[arr[0]] = v
	}
	s.mu.Unlock()

	logs.Trace("new p2p connection ,role %s , password %s ,local address %s", arr[1], arr[0], addr.String())
	if arr[1] == common.WORK_P2P_VISITOR {
		s.mu.Lock()
		v.visitorAddr = addr
		s.mu.Unlock()
		// 20 秒内等待 provider 就绪；ticker + 超时，goroutine 可退出（F1-4）
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		timeout := time.NewTimer(20 * time.Second)
		defer timeout.Stop()
	waitProvider:
		for {
			s.mu.Lock()
			provider := v.providerAddr
			visitor := v.visitorAddr
			s.mu.Unlock()
			if provider != nil {
				s.listener.WriteTo([]byte(provider.String()), visitor)
				s.listener.WriteTo([]byte(visitor.String()), provider)
				break waitProvider
			}
			select {
			case <-ticker.C:
			case <-timeout.C:
				break waitProvider
			}
		}
		s.mu.Lock()
		delete(s.p2p, arr[0])
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		v.providerAddr = addr
		v.lastSeen = time.Now()
		s.mu.Unlock()
	}
}
