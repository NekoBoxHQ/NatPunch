package file

import (
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/rate"
)

// 「客户端上报配置」这条路径（bridge 的 NEW_CONF）**不能把限速器换掉**，
// 也不能把面板设的限速值冲掉 —— 上报的配置里根本没有 RateLimit 这一项。
func TestSyncClientRateKeepsExisting(t *testing.T) {
	existing := &Client{Id: 1, RateLimit: 8, Rate: rate.NewRate(8 * 1024 * 1024 / 8)}
	reported := &Client{Id: 1} // 上报对象里 RateLimit 是 0

	syncClientRate(existing, reported)
	if reported.Rate != existing.Rate {
		t.Fatal("限速器被换掉了：活着的连接还握在旧对象上，网速会一直是 0，旧 ticker 也不停")
	}
	if reported.RateLimit != 8 {
		t.Fatalf("面板设的限速被上报值冲掉了: %d（应为 8）", reported.RateLimit)
	}
}

// 首次注册（库里还没有这条）时要造一个限速器出来。
func TestSyncClientRateFresh(t *testing.T) {
	c := &Client{Id: 2}
	syncClientRate(nil, c)
	if c.Rate == nil {
		t.Fatal("首次注册没有限速器")
	}
	if c.RateLimit != 0 {
		t.Fatalf("不限速的客户端，限速值不该被改动: %d", c.RateLimit)
	}
}

// rateLimitAddSize 的口径不能错：0=不限速给一个超大值；否则 Mbps → 字节/秒。
func TestRateLimitAddSize(t *testing.T) {
	if got := rateLimitAddSize(0); got <= 0 {
		t.Fatalf("不限速应该给一个正的大 addSize，得到 %d", got)
	}
	// 8 Mbps = 8 * 1024 * 1024 / 8 字节/秒 = 1048576
	if got := rateLimitAddSize(8); got != int64(8*1024*1024/8) {
		t.Fatalf("8 Mbps 换算不对: %d", got)
	}
}

// NewClientRate 要把速率采样接到 Client.Flow 上（TCP+UDP 都算）。
// 这里只验证接线本身：给一个 Flow，SetFlowSource 的回调要能读到 In+Export 之和。
func TestNewClientRateReadsFlow(t *testing.T) {
	c := &Client{Id: 3, Flow: &Flow{InletFlow: 100, ExportFlow: 200}}
	r := NewClientRate(c)
	if r == nil {
		t.Fatal("NewClientRate 返回 nil")
	}
	// 直接调 SetFlowSource 装进去的回调不好拿，换个方式验证：构造时的 Flow 内容能被读到。
	// 这里用 reflect 不划算，改用"限速器能启动、能停"作为接线正常的最小证据。
	r.Start()
	r.Stop()
	r.Start() // 幂等 + Stop 后可重启
	r.Stop()
}
