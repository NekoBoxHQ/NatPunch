package proxy

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
)

const (
	srcPoolCapacity = 16              // 单源池容量：池满立即丢包（不阻塞、不排队）
	srcPoolMaxPools = 4096            // 池 map 总量上限：超限拒绝新源（丢包 + 计数告警）
	srcPoolIdleTTL  = 5 * time.Minute // 池空闲淘汰阈值
	srcPoolSweepGap = 1 * time.Minute // 后台清理周期
)

// udpSourcePool 单源 ants 池 + 最近使用时间（F1-7）
type udpSourcePool struct {
	pool     *ants.PoolWithFunc
	lastUsed int64
}

// sourcePoolSet 按源地址分池的调度器：池 map 自带 TTL 淘汰与总量上限。
// 防止"按源建池"引入新的无界增长结构（攻击者换散列源地址打爆内存）。
type sourcePoolSet struct {
	mu       sync.Mutex
	pools    map[string]*udpSourcePool
	capacity int
	maxPools int
	worker   func(interface{})
	stopCh   chan struct{}
	stopOnce sync.Once
	dropped  int64 // 丢包计数（告警用）
}

func newSourcePoolSet(capacity, maxPools int, worker func(interface{})) *sourcePoolSet {
	s := &sourcePoolSet{
		pools:    make(map[string]*udpSourcePool),
		capacity: capacity,
		maxPools: maxPools,
		worker:   worker,
		stopCh:   make(chan struct{}),
	}
	go s.sweep()
	return s
}

// Submit 提交任务；池满或池 map 超上限时返回 false，调用方负责丢包与资源归还。
func (s *sourcePoolSet) Submit(key string, item interface{}) bool {
	s.mu.Lock()
	p, ok := s.pools[key]
	if !ok {
		if len(s.pools) >= s.maxPools {
			s.mu.Unlock()
			atomic.AddInt64(&s.dropped, 1)
			return false
		}
		var err error
		p = &udpSourcePool{}
		p.pool, err = ants.NewPoolWithFunc(s.capacity, s.worker, ants.WithNonblocking(true))
		if err != nil {
			s.mu.Unlock()
			return false
		}
		s.pools[key] = p
	}
	atomic.StoreInt64(&p.lastUsed, time.Now().UnixNano())
	s.mu.Unlock()
	if err := p.pool.Invoke(item); err != nil {
		atomic.AddInt64(&s.dropped, 1)
		return false
	}
	return true
}

// Dropped 返回累计丢包数（告警用）。
func (s *sourcePoolSet) Dropped() int64 {
	return atomic.LoadInt64(&s.dropped)
}

// sweep 后台清理：释放 5 分钟未活动的池并删除条目。
func (s *sourcePoolSet) sweep() {
	ticker := time.NewTicker(srcPoolSweepGap)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := time.Now().UnixNano()
			s.mu.Lock()
			stale := make([]*udpSourcePool, 0)
			for k, p := range s.pools {
				if now-atomic.LoadInt64(&p.lastUsed) > int64(srcPoolIdleTTL) {
					delete(s.pools, k)
					stale = append(stale, p)
				}
			}
			s.mu.Unlock()
			for _, p := range stale {
				p.pool.Release()
			}
		}
	}
}

// Close 停止清理并释放全部池。
func (s *sourcePoolSet) Close() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.mu.Lock()
		stale := make([]*udpSourcePool, 0, len(s.pools))
		for k, p := range s.pools {
			delete(s.pools, k)
			stale = append(stale, p)
		}
		s.mu.Unlock()
		for _, p := range stale {
			p.pool.Release()
		}
	})
}
