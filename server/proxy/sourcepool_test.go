package proxy

import "testing"

// TestSourcePoolSet_SubmitAfterCloseRejected：Close 之后 Submit 必须被拒绝。
// 原实现只清空 pools 并退出 sweep goroutine，Submit 仍会新建池 —— 新池无人清扫，
// 且 sweep 已退出，构成泄漏（复评🟡）。
func TestSourcePoolSet_SubmitAfterCloseRejected(t *testing.T) {
	s := newSourcePoolSet(2, 4, func(interface{}) {})

	// Close 前应可正常提交
	if !s.Submit("src-a", 1) {
		t.Fatal("Close 前 Submit 应返回 true")
	}

	s.Close()

	if s.Submit("src-b", 2) {
		t.Fatal("Close 后 Submit 必须返回 false（否则会复活无人清扫的池）")
	}

	s.mu.Lock()
	n := len(s.pools)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("Close 后 pools 应保持为空，实际 %d 项", n)
	}
}

// TestSourcePoolSet_CloseIdempotent：重复 Close 不应 panic。
func TestSourcePoolSet_CloseIdempotent(t *testing.T) {
	s := newSourcePoolSet(2, 4, func(interface{}) {})
	s.Close()
	s.Close() // stopOnce 保护
}
