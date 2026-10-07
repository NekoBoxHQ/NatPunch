package crypt

import (
	"crypto/md5"
	"crypto/rand"
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
