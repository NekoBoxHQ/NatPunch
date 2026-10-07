package rate

import (
	"context"
	"sync"
	"time"

	xrate "golang.org/x/time/rate"
)

type Rate struct {
	limiter   *xrate.Limiter
	addSize   int64
	mu        sync.Mutex
	stopChan  chan struct{}
	readTotal func() int64 // 每个采样周期读一次累计字节（来自 Client.Flow，含 TCP+UDP）
	NowRate   int64        // 字节/秒
}

func NewRate(addSize int64) *Rate {
	return &Rate{
		limiter: xrate.NewLimiter(xrate.Limit(addSize), int(addSize)),
		addSize: addSize,
	}
}

// SetFlowSource 告诉限速器「速率从哪个累计计数读」（通常是 Client.Flow 的 Inlet+Export 之和）。
// 用回调而不是直接引 file.Flow，是为了不让 lib/rate 依赖 lib/file 造成 import cycle。
// 必须在 Start() 之前调用一次；Start 会把当时的回调抓进采样 goroutine。
func (s *Rate) SetFlowSource(f func() int64) {
	s.mu.Lock()
	s.readTotal = f
	s.mu.Unlock()
}

// Start 启动「每 2 秒从累计字节算出速率」的采样 —— 与服务端 tool.collectIORate 同一口径：
// 读累计值 → 除时间间隔得字节/秒。速率从 Client.Flow 的增量来，所以 **TCP 和 UDP 都算**
// （旧的实现只数 rateConn 的 consumed，那是 TCP-only，漏掉了 UDP 的代理流量）。
//
// **幂等**：已经在跑就直接返回。客户端每次上报配置都会走 NewClient → Rate.Start()，
// 原来这里无条件 go func()，于是每秒多一个永不退出的 ticker（goroutine 泄漏）。
// 需要重新开始就先 Stop（面板改限速那条路径就是这么用的）。
func (s *Rate) Start() {
	s.mu.Lock()
	if s.stopChan != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.stopChan = stop
	read := s.readTotal
	s.mu.Unlock()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var lastTotal int64
		var haveLast bool
		for {
			select {
			case <-ticker.C:
				if read == nil {
					continue
				}
				total := read()
				if haveLast {
					delta := total - lastTotal
					if delta < 0 {
						delta = 0 // 计数被重置过（如清零流量），别显示成负速率
					}
					instant := delta / 2 // 2 秒窗口 → 字节/秒
					// 单客户端流量比整机更突发，做 50% 指数平滑，别在 0 和满格之间跳
					s.NowRate = (s.NowRate + instant) / 2
				}
				lastTotal = total
				haveLast = true
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
		size -= n
	}
}
