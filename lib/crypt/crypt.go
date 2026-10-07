package crypt

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
)

// Generate 32-bit MD5 strings
func Md5(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Generating Random Verification Key
func GetRandomString(l int) string {
	str := "0123456789abcdefghijklmnopqrstuvwxyz"
	result := make([]byte, l)
	buf := make([]byte, l)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败属于系统性故障，绝不用可预测源兜底
		panic("crypto/rand failed: " + err.Error())
	}
	for i := 0; i < l; i++ {
		result[i] = str[int(buf[i])%len(str)]
	}
	return string(result)
}

// GetVkey 生成 128 bit 十六进制验证密钥（32 hex，crypto/rand）。
// 旧实现截取 UUID 前 10 位十六进制（40 bit）强度不足，且认证值为无盐 MD5（P1-4）。
func GetVkey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// NewShadowsocksPSK 生成一个 Shadowsocks 2022 用的 PSK：16 字节随机数 → 标准 base64。
//
// 16 字节不是随便定的 —— SIP022 要求 `2022-blake3-aes-128-gcm` 的预共享密钥
// 正好是密钥长度（16 字节），且必须是 base64 编码的随机字节，**不允许**从口令派生。
// 长度写死在 server/proxy 的 SSKeySize，两边要一起改。
func NewShadowsocksPSK() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// decodePSK16 尝试把一段文本解成 16 字节的 PSK。
// 四种 base64 变体都试：标准/无填充/URL-safe/URL-safe 无填充 ——
// 从别处粘过来的密钥常常是 URL-safe 的（`-` `_`），只认标准编码会把手里的真密钥
// 当成口令重新派生，那就成了一个谁也对不上的新密钥。
func decodePSK16(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 16 {
			return b, true
		}
	}
	return nil, false
}

// NormalizeShadowsocksPSK 把面板「密码」框里填的东西规整成一条真能用的 PSK。
//
//	留空                     → 随机生成 16 字节
//	本来就是 16 字节 base64  → 原样用（归一成标准 base64 再存）
//	其它（随手输的口令、从 ss:// 链接里连着 %2B / %3D 一起粘进来的、长度不对的）
//	                         → 当成**口令**派生出 16 字节
//
// 为什么最后一条不是报错而是派生：面板上那个框的语义是「密码」，
// 让人先学一遍 base64 的长度规则、否则隧道静默起不来（真发生过），
// 那是把协议的麻烦转嫁给使用者。派生出来的密钥会写回 tunnel.Password，
// 所以面板里存的、ss:// 链接里给的始终是合法的 base64。
//
// 代价说清楚：口令派生出来的密钥强度上限就是那句口令，别拿它当随机密钥用。
func NormalizeShadowsocksPSK(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return NewShadowsocksPSK()
	}
	if raw, ok := decodePSK16(s); ok {
		return base64.StdEncoding.EncodeToString(raw)
	}
	// 从 ss:// 链接里连着转义一起复制过来的形态：`2KNJUg%2B8hImzj1UHKvXHZw%3D%3D`。
	// 先把百分号转义还原再试一次 —— 这种输入是**有**正确密钥的，直接当口令派生
	// 会把用户手里的密钥换成另一个，谁都对不上。
	if unescaped, err := url.PathUnescape(s); err == nil && unescaped != s {
		if raw, ok := decodePSK16(unescaped); ok {
			return base64.StdEncoding.EncodeToString(raw)
		}
	}
	h := sha256.Sum256([]byte("natpunch/shadowsocks-2022/psk\x00" + s))
	return base64.StdEncoding.EncodeToString(h[:16])
}
