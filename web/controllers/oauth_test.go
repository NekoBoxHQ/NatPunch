package controllers

import (
	"encoding/hex"
	"testing"
)

// 白名单必须使用不可变的 GitHub 数字 ID；用户名只用于日志展示。
func TestGithubIsAdminIsExactMatchOnly(t *testing.T) {
	admins := []int64{1001, 2002}

	cases := []struct {
		id   int64
		want bool
		why  string
	}{
		{1001, true, "精确命中"},
		{2002, true, "第二个 ID 也应命中"},
		{100, false, "前缀匹配必须不成立"},
		{10010, false, "扩展 ID 必须不成立"},
		{0, false, "零值不能命中"},
		{-1, false, "负数不能命中"},
	}
	for _, c := range cases {
		if got := githubIsAdmin(c.id, admins); got != c.want {
			t.Errorf("githubIsAdmin(%d) = %v, want %v —— %s", c.id, got, c.want, c.why)
		}
	}

	if githubIsAdmin(1001, nil) {
		t.Error("白名单为空时不得放行任何账号（否则配置漏填 = 人人都是管理员）")
	}
}

func TestParseGithubAdminIDsRejectsInvalidValues(t *testing.T) {
	got := parseGithubAdminIDs(" 1001, 2002; bogus, -1, 0, 3003 ")
	want := []int64{1001, 2002, 3003}
	if len(got) != len(want) {
		t.Fatalf("parseGithubAdminIDs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseGithubAdminIDs()[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// 这条钉的是 v26.10.46 留给 v26.10.47 修的一个社会工程载荷：
// 失败文案当时直接放在回跳 URL 上，于是任何人都能用
// /login/index?oauth_error=<任意文本> 在**真登录页**上弹出他想要的文字。
// 修法是文案只认一张表 —— 表外的输入必须得到空串，页面才不会显示任何东西。
func TestGithubOAuthErrorTextOnlyKnowsItsTable(t *testing.T) {
	for _, code := range []string{
		"",             // 没带参数
		"unknown",      // 不认识的码
		"账号已停用，请联系管理员", // 想借 URL 回显的任意文本
		"<img src=x onerror=alert(1)>",
		"state ", // 多一个空格也算不认识（不做宽松匹配）
		"STATE",
	} {
		if got := githubOAuthErrorText(code); got != "" {
			t.Errorf("githubOAuthErrorText(%q) = %q, want 空串 —— 表外的输入不许产出任何文案", code, got)
		}
	}

	// 每个码都要有文案，且两两不同（防复制粘贴写重）
	seen := map[string]string{}
	for _, code := range []string{"rate", "init", "state", "denied", "nocode", "token", "user", "notadmin"} {
		msg := githubOAuthErrorText(code)
		if msg == "" {
			t.Errorf("错误码 %q 没有对应文案", code)
			continue
		}
		if prev, dup := seen[msg]; dup {
			t.Errorf("错误码 %q 与 %q 的文案重复：%q", code, prev, msg)
		}
		seen[msg] = code
	}
}

// state 是 CSRF / 授权码注入的唯一防线，必须来自 crypto/rand 且足够长。
func TestGithubRandomStateIsRandomHex(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		s, err := githubRandomState()
		if err != nil {
			t.Fatalf("生成 state 失败：%v", err)
		}
		if len(s) != 32 {
			t.Fatalf("state 长度 = %d, want 32（128bit 的 hex）", len(s))
		}
		if _, err := hex.DecodeString(s); err != nil {
			t.Fatalf("state 不是合法 hex：%q", s)
		}
		if seen[s] {
			t.Fatalf("state 出现重复：%q —— 随机源退化成了可预测序列？", s)
		}
		seen[s] = true
	}
}
