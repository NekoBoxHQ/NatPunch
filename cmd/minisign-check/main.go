// Command minisign-check 是 NatPunch 发布物签名校验器。
//
// 用于没有 minisign 工具的环境（如 OpenWrt 无 minisign 软件包）：
// 安装脚本从 Release 资产下载本工具（静态编译，与目标架构匹配），
// 用发布方公钥校验 SHA256SUMS 的 minisign 签名。
//
// 用法:
//
//	minisign-check <publickey-file> <minisig-file> <file>
//
// 退出码: 0 = 校验通过; 1 = 校验失败; 2 = 参数/文件错误
package main

import (
	"fmt"
	"os"

	"github.com/jedisct1/go-minisign"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: minisign-check <publickey> <minisig> <file>")
		os.Exit(2)
	}

	pk, err := minisign.NewPublicKeyFromFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR load public key:", err)
		os.Exit(2)
	}
	sig, err := minisign.NewSignatureFromFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR load signature:", err)
		os.Exit(2)
	}

	ok, err := pk.VerifyFromFile(os.Args[3], sig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR verify:", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "FAIL: signature does not match")
		os.Exit(1)
	}
	fmt.Println("OK: signature verified")
}
