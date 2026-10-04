package crypt

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

// GetVkey 生成 128 bit 验证密钥：base62（0-9A-Za-z）22 位，crypto/rand。
// 旧实现截取 UUID 前 10 位十六进制（40 bit）强度不足（P1-4）；
// 上一实现为 32 位小写 hex（128 bit）。当前格式大小写混合、更短易辨识，
// 与旧风格明显区分（重置 VKEY 需求：不保持原风格）。
func GetVkey() string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const size = 22 // 62^22 ≈ 2^131 > 2^128
	// rejection sampling 消除模偏差（256 % 62 != 0）
	const limit = 256 - (256 % len(alphabet)) // 248
	buf := make([]byte, size)
	tmp := make([]byte, 1)
	for i := 0; i < size; {
		if _, err := rand.Read(tmp); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		if int(tmp[0]) < limit {
			buf[i] = alphabet[int(tmp[0])%len(alphabet)]
			i++
		}
	}
	return string(buf)
}

func Base64Decoding(encodedString string) (string, error) {
	// 先尝试 base64 解码，兼容原先的 "nps " 前缀
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedString)
	decodedString := string(decodedBytes)

	if err == nil {
		if len(decodedString) >= 4 && decodedString[:4] == "nps " {
			return decodedString[4:], nil
		}
	}
	// 兼容直接以 "nps:" 开头的旧格式：
	// nps:name|addr|key|tls[|fp]
	if len(decodedString) >= 4 && strings.HasPrefix(decodedString, "nps:") {
		return joinQuickCmd(decodedString[4:])
	}
	// 面板当前生成的格式：name|addr|key|tls[|fp]（无前缀，F2-2 快速命令携带指纹）
	if strings.Contains(decodedString, "|") {
		return joinQuickCmd(decodedString)
	}

	return "", errors.New("快捷启动命令错误，请检查")
}

// joinQuickCmd 把 "name|addr|vkey|tls[|fp]" 拼成 "addr vkey tls[ fp]"（startNpcServer 用 Fields 解析）
func joinQuickCmd(s string) (string, error) {
	parts := strings.Split(s, "|")
	if len(parts) < 4 {
		return "", errors.New("快捷启动命令格式错误，请检查")
	}
	addr := strings.TrimSpace(parts[1])
	key := strings.TrimSpace(parts[2])
	tls := strings.TrimSpace(parts[3])
	ret := addr + " " + key + " " + tls
	if len(parts) > 4 && strings.TrimSpace(parts[4]) != "" {
		ret += " " + strings.TrimSpace(parts[4])
	}
	return ret, nil
}
