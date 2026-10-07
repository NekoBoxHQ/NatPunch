package crypt

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
