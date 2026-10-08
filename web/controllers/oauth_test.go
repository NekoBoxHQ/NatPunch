package controllers

import (
	"encoding/hex"
	"testing"
)

// 白名单匹配是 GitHub 登录唯一的授权闸门：一旦退化成"前缀匹配 / contains / 忽略空值"，
// 就等于把面板管理员权限交给任意 GitHub 账号。用例里同时钉住"不能放宽"和"该放宽的放宽"。
func TestGithubIsAdminIsExactMatchOnly(t *testing.T) {
	// githubOAuthConf 解析后已统一小写
	admins := []string{"alice", "bob-2"}

	cases := []struct {
		login string
		want  bool
		why   string
	}{
		{"alice", true, "精确命中"},
		{"ALICE", true, "GitHub 用户名大小写不敏感，服务端应统一小写后比对"},
		{"Alice", true, "同上（混合大小写）"},
		{" alice ", true, "配置里手滑留的空格不应导致登录失败"},
		{"bob-2", true, "带连字符的用户名要能命中"},
		{"alice2", false, "前缀匹配必须不成立"},
		{"bob", false, "前缀匹配必须不成立"},
		{"xalice", false, "后缀匹配必须不成立"},
		{"", false, "空用户名不能命中任何白名单"},
		{"admin", false, "不在名单里就是不在"},
	}
	for _, c := range cases {
		if got := githubIsAdmin(c.login, admins); got != c.want {
			t.Errorf("githubIsAdmin(%q) = %v, want %v —— %s", c.login, got, c.want, c.why)
		}
	}

	if githubIsAdmin("alice", nil) {
		t.Error("白名单为空时不得放行任何账号（否则配置漏填 = 人人都是管理员）")
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
