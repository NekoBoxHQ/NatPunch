package crypt

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestGetVkey(t *testing.T) {
	v1 := GetVkey()
	v2 := GetVkey()
	if len(v1) != 32 {
		t.Fatalf("vkey length = %d, want 32 (128 bit)", len(v1))
	}
	if _, err := hex.DecodeString(v1); err != nil {
		t.Fatalf("vkey is not hex: %v", err)
	}
	if v1 == v2 {
		t.Fatal("two vkeys identical, expect random")
	}
}

func TestGetRandomString(t *testing.T) {
	charset := "0123456789abcdefghijklmnopqrstuvwxyz"
	for _, l := range []int{8, 16, 32} {
		s := GetRandomString(l)
		if len(s) != l {
			t.Fatalf("len = %d, want %d", len(s), l)
		}
		for _, c := range s {
			if !strings.ContainsRune(charset, c) {
				t.Fatalf("char %q outside charset", c)
			}
		}
	}
}

// 面板「密码」框里填什么都要能变成一条**真能启动**的 PSK ——
// 线上就是这么挂的：填了口令 → 原样存进 tunnel.Password → SS 服务启动时
// "decode psk: illegal base64"，隧道建出来了却 RunStatus=false。
func TestNormalizeShadowsocksPSK(t *testing.T) {
	// 空 → 自动生成一条合用的
	if _, ok := decodePSK16(NormalizeShadowsocksPSK("")); !ok {
		t.Fatal("空输入没有生成合用的 PSK")
	}

	// 本来就是 16 字节 base64 → 原样用
	std := "2KNJUg+8hImzj1UHKvXHZw=="
	if got := NormalizeShadowsocksPSK(std); got != std {
		t.Fatalf("合法密钥被改写了: %q → %q", std, got)
	}

	// 从 ss:// 链接里连着 URL 转义一起粘过来的 → 必须还原成**同一个**密钥，
	// 不能另派一个（那用户手里的密钥就对不上了）。
	// 用 QueryEscape 而不是 PathEscape：后者按 path segment 的规则放行 '+' 和 '='，
	// 压根不产生转义；链接里那份是 encodeURIComponent 的产物（%2B / %3D）。
	esc := url.QueryEscape(std)
	if esc == std {
		t.Fatal("测试用例本身没产生转义，换个密钥")
	}
	if got := NormalizeShadowsocksPSK(esc); got != std {
		t.Fatalf("转义过的密钥没还原: %q → %q（应为 %q）", esc, got, std)
	}
	// 照抄面板 ss:// 链接里的真实形态（encodeURIComponent 产物）
	if got := NormalizeShadowsocksPSK("2KNJUg%2B8hImzj1UHKvXHZw%3D%3D"); got != std {
		t.Fatalf("ss:// 链接里粘出来的密钥没还原: %q（应为 %q）", got, std)
	}

	// URL-safe base64 也要认
	raw, _ := base64.StdEncoding.DecodeString(std)
	urlSafe := base64.RawURLEncoding.EncodeToString(raw)
	if got := NormalizeShadowsocksPSK(urlSafe); got != std {
		t.Fatalf("URL-safe 密钥没被认出来: %q → %q", urlSafe, got)
	}

	// 随手输的口令 → 派生，且确定性、合用法、不同口令不撞
	p1 := NormalizeShadowsocksPSK("my pass")
	p2 := NormalizeShadowsocksPSK("my pass")
	if p1 != p2 {
		t.Fatalf("同一口令派生结果不一致: %q vs %q", p1, p2)
	}
	if _, ok := decodePSK16(p1); !ok {
		t.Fatalf("派生结果不是合用的 PSK: %q", p1)
	}
	if NormalizeShadowsocksPSK("my pass2") == p1 {
		t.Fatal("不同口令派生出了同一个密钥")
	}

	// **幂等**：归一化的结果再喂一遍必须原样返回。
	// AddTask 每次启动隧道都会跑一遍归一化（就是为了治好存量里那条不合法的密钥），
	// 一旦不幂等，每重启一次密钥就被重新派生一次 —— 上次给出的 ss:// 链接、
	// 客户端里配好的那份就全对不上了。
	for _, in := range []string{"", std, esc, urlSafe, "my pass"} {
		once := NormalizeShadowsocksPSK(in)
		if twice := NormalizeShadowsocksPSK(once); twice != once {
			t.Fatalf("归一化不幂等: %q → %q → %q", in, once, twice)
		}
	}
}
