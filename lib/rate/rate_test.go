package rate

import (
	"runtime"
	"testing"
	"time"
)

// settleGoroutines 等到 goroutine 数**稳定**（连续三次采样相同）再返回。
//
// 直接取 runtime.NumGoroutine() 当基准是不行的：上一条用例 Stop 之后，它的采样
// goroutine 是异步退出的，很可能还没退干净 —— 基准取高了，"起没起来"就永远判不成立
// （这个坑第一次写这组用例时就踩了，顺序跑才暴露）。
func settleGoroutines() int {
	last, stable := -1, 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n := runtime.NumGoroutine()
		if n == last {
			stable++
			if stable >= 3 {
				return n
			}
		} else {
			stable, last = 0, n
		}
		time.Sleep(50 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// waitGoroutines 轮询到条件满足或超时，超时返回当前值让调用方判。
func waitGoroutines(want func(int) bool) int {
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if want(n) || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Start 必须幂等。客户端**每次上报配置**都会走 NewClient → Rate.Start()，
// 原来那里无条件 `go func()`，每调一次就多一个永不退出的 ticker（goroutine 泄漏）。
func TestStartIsIdempotent(t *testing.T) {
	r := NewRate(1 << 20)
	base := settleGoroutines()
	for i := 0; i < 50; i++ {
		r.Start()
	}
	// 等到"至少起来一个"为止（起来就说明采样在跑），再要求它**没有涨成一堆**
	got := waitGoroutines(func(n int) bool { return n > base })
	if got <= base {
		t.Fatal("50 次 Start 之后一个采样 goroutine 都没起来")
	}
	if got > base+5 {
		t.Fatalf("Start 不幂等：goroutine 从 %d 涨到 %d（调了 50 次）", base, got)
	}
	r.Stop()
	settleGoroutines() // 别把没退干净的算进下一条用例的基准
}

// Stop 之后还能再 Start（更新隧道那条路径就是同一个对象 Stop→Start），
// 而且 Stop 本身要幂等（不能 panic）。
func TestStopThenStartAgain(t *testing.T) {
	r := NewRate(1 << 20)
	base := settleGoroutines()

	r.Start()
	if got := waitGoroutines(func(n int) bool { return n > base }); got <= base {
		t.Fatalf("Start 没起采样 goroutine：%d -> %d", base, got)
	}

	r.Stop()
	r.Stop() // 幂等
	if got := waitGoroutines(func(n int) bool { return n <= base }); got > base {
		t.Fatalf("Stop 没停掉采样 goroutine：%d -> %d", base, got)
	}

	r.Start()
	if got := waitGoroutines(func(n int) bool { return n > base }); got <= base {
		t.Fatalf("Stop 之后 Start 起不来了：%d", got)
	}
	r.Stop()
	settleGoroutines()
}
