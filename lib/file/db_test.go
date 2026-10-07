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
