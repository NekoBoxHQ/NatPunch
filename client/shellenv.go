package client

import (
	"os"
	"os/user"
	"strings"
)

// 面板终端（ConnType == "shell"）起 shell 时的环境构造。
//
// 为什么要单独拎出来：面板 shell 是**整个继承客户端进程环境**的，而客户端由 procd /
// systemd 拉起，那份环境跟「电脑 SSH 登录」拿到的完全不是一回事 —— 实测踩到的两个坑
// 都属于这一类：
//
//   - HOME 是 "/" 甚至没设 → bash 在 HOME="/" 时不做 ~ 替换，提示符显示 root@host:/#
//     而不是电脑 SSH 那样的 root@host:~#。
//   - HOSTNAME 可能是陈旧值 → bash 与 busybox ash 都只在 $HOSTNAME **未设置**时才用内核
//     真名补上，继承来的值会被原样留着；任何用 $HOSTNAME 拼提示符的 profile 都会显示它。
//
// （注：`\h` / `\H` 本身读的是内核 hostname —— bash 5.x 与 busybox ash 的 lineedit 都是
// 如此，已实测确认 —— 所以真正会把主机名带偏的是 profile 里的 $HOSTNAME。）

// pickHome 从候选里挑第一个可用家目录，明确拒绝 "/"。
// 全部不可用时兜底 /root（面板终端以 root 运行是常态）。
func pickHome(cands ...string) string {
	for _, c := range cands {
		if c != "" && c != "/" {
			return c
		}
	}
	return "/root"
}

// shellHome 决定面板终端 shell 的家目录。
//
// 顺序：/etc/passwd 里当前用户的家目录（CGO_ENABLED=0 下 os/user 用纯 Go 解析
// /etc/passwd，OpenWrt 一样可用）→ 进程 $HOME → /root。
// 不能只信 os.UserHomeDir()：那是客户端进程的 $HOME。
func shellHome() string {
	var cands []string
	if u, err := user.Current(); err == nil {
		cands = append(cands, u.HomeDir)
	}
	if h, err := os.UserHomeDir(); err == nil {
		cands = append(cands, h)
	}
	return pickHome(cands...)
}

// buildShellEnv 在 base（通常是 os.Environ()）之上构造面板 shell 的环境变量。
//
// HOME / PS1 / TERM / HOSTNAME 四个键**先摘掉再追加**，而不是直接 append：
// 环境数组里出现重复项时 getenv 取的是第一个匹配项，直接 append 的注入值会被继承来的
// 旧值静默顶掉 —— 那正是「明明注入了却不起作用」的经典写法。其余变量原样保留。
func buildShellEnv(base []string, home, hostname string) []string {
	env := make([]string, 0, len(base)+4)
	for _, kv := range base {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PS1=") ||
			strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "HOSTNAME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		// PS1 兜底：个别系统 profile / bash.bashrc 不设 PS1 时仍给出完整提示符。
		// 登录 shell 会先读 /etc/profile，那份若设了 PS1 会覆盖这里，属预期。
		"PS1=\\u@\\h:\\w\\$ ",
		// 与电脑 SSH 一致的标准终端类型，vim / top / htop 的颜色与全屏布局才正常
		"TERM=xterm-256color",
		// 用内核真名覆盖，见文件头注释
		"HOSTNAME="+hostname,
	)
}
