package rate

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// 回归测试：采样协程写 NowRate 与 json.Marshal（面板列表 / clients.json 落盘）并发。
//
// 旧实现里 `s.NowRate = ...` 与 json 反射读字段之间没有任何同步：
// 在 armv7 / mipsle（都在发布矩阵里）上 int64 会撕裂，读出一半旧一半新的速率；
// 在 amd64 上则是纯粹的 data race（-race 直接报）。
func TestNowRateConcurrentMarshal(t *testing.T) {
	var total int64
	r := NewRate(1024)
	r.SetFlowSource(func() int64 { return atomic.AddInt64(&total, 8192) })
	r.Start()
	defer r.Stop()

	deadline := time.Now().Add(5500 * time.Millisecond)
	for time.Now().Before(deadline) {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// 形状必须保持 {"NowRate":N}，否则 clients.json / 列表前端会读不到
		var got map[string]int64
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if _, ok := got["NowRate"]; !ok {
			t.Fatalf("序列化结果缺少 NowRate: %s", b)
		}
	}
	if r.GetNowRate() <= 0 {
		t.Fatalf("采样两个周期后 NowRate 仍为 0（采样协程没跑？）")
	}
}
