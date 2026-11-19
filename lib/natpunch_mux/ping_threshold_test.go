package natpunch_mux

import "testing"

func TestPingThresholdInterpretsSeconds(t *testing.T) {
	cases := []struct {
		seconds int
		conn    string
		want    uint32
	}{
		// 默认 60 秒 → 12 个 5s tick（旧实现把 60 当次数，得到 300 秒）
		{60, "tcp", 12},
		{60, "kcp", 12},
		{0, "tcp", 12},
		{-1, "tcp", 12},
		// 不足 15 秒的配置一律抬到 3 个 tick，避免抖动导致误判
		{5, "tcp", 3},
		{10, "tcp", 3},
		{15, "tcp", 3},
		// 向上取整
		{16, "tcp", 4},
		{61, "tcp", 13},
		{300, "tcp", 60},
	}
	for _, tc := range cases {
		if got := pingThreshold(tc.seconds, tc.conn); got != tc.want {
			t.Errorf("pingThreshold(%d, %q) = %d，期望 %d", tc.seconds, tc.conn, got, tc.want)
		}
	}
}

func TestPingTickMatchesTickerInterval(t *testing.T) {
	// 换算依赖 pingTick 必须等于 ticker 的间隔；这里锁死，避免只改一处
	if pingTick.Seconds() != 5 {
		t.Fatalf("pingTick 应为 5 秒，实际 %v", pingTick)
	}
}
