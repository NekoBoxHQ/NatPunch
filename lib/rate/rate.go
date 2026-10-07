package rate

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	xrate "golang.org/x/time/rate"
)

type Rate struct {
	limiter  *xrate.Limiter
	addSize  int64
	mu       sync.Mutex
	stopChan chan struct{}
	consumed int64
	NowRate  int64
}

func NewRate(addSize int64) *Rate {
	return &Rate{
		limiter: xrate.NewLimiter(xrate.Limit(addSize), int(addSize)),
		addSize: addSize,
	}
}

// Start 启动"每秒把这一秒消耗的字节数记进 NowRate"的采样。
//
// **幂等**：已经在跑就直接返回。客户端每次上报配置都会走 NewClient → NewClient.Rate.Start()，
// 原来这里无条件 `go func()`，于是每秒多一个永不退出的 ticker（goroutine 泄漏）。
// 需要重新开始就先 Stop（面板改限速那条路径就是这么用的）。
func (s *Rate) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopChan != nil {
		return
	}
	stop := make(chan struct{})
	s.stopChan = stop
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.NowRate = atomic.SwapInt64(&s.consumed, 0)
			case <-stop:
				return
			}
		}
	}()
}

// Stop 停掉采样（幂等）；之后 Start 能再起来。
func (s *Rate) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopChan == nil {
		return
	}
	close(s.stopChan)
	s.stopChan = nil
}

func (s *Rate) Get(size int64) {
	if s.addSize <= 0 {
		return
	}
	if size <= 0 {
		return
	}
	// 按 burst 分块等待：WaitN 在 size > burst 时直接返回错误（大块读写绕过限速），
	// 必须拆成 burst 大小逐块排队（阶段三 #10）
	burst := int64(s.limiter.Burst())
	ctx := context.Background()
	for size > 0 {
		n := size
		if n > burst {
			n = burst
		}
		_ = s.limiter.WaitN(ctx, int(n))
		atomic.AddInt64(&s.consumed, n)
		size -= n
	}
}
