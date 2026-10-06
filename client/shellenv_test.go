package client

import (
	"strings"
	"testing"
)

func TestPickHome(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		// 面板提示符显示 root@host:/# 的直接原因：HOME 是 "/"
		{"拒绝 /", []string{"/"}, "/root"},
		{"拒绝空串", []string{""}, "/root"},
		{"/ 之后还有候选就继续挑", []string{"/", "/root"}, "/root"},
		{"正常家目录直接用", []string{"/home/admin"}, "/home/admin"},
		{"第一个可用者胜出", []string{"/home/a", "/home/b"}, "/home/a"},
		{"一个候选都没有", nil, "/root"},
		{"只有 /", []string{"/"}, "/root"},
	}
	for _, c := range cases {
		if got := pickHome(c.in...); got != c.want {
			t.Errorf("%s: pickHome(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// envMap 把环境数组转成 key->value，顺便断言没有重复键。
// 重复键正是本文件要防的 bug：getenv 取第一个匹配项，注入值会被顶掉。
func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			t.Errorf("环境项格式异常: %q", kv)
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if _, dup := m[k]; dup {
			t.Errorf("环境里出现重复键 %q —— getenv 只取第一个，注入值会被静默顶掉", k)
		}
		m[k] = v
	}
	return m
}

func TestBuildShellEnv(t *testing.T) {
	// 模拟 procd / systemd 拉起客户端时的环境：HOME 是 "/"，HOSTNAME 陈旧。
	base := []string{
		"PATH=/usr/sbin:/usr/bin",
		"HOME=/",
		"HOSTNAME=iStore0S-DT",
		"PS1=$ ",
		"TERM=dumb",
		"PWD=/",
	}

	env := buildShellEnv(base, "/root", "iStoreOS-DT")
	m := envMap(t, env)

	if m["HOME"] != "/root" {
		t.Errorf("HOME = %q, want /root（继承来的 / 必须被顶掉）", m["HOME"])
	}
	if m["HOSTNAME"] != "iStoreOS-DT" {
		t.Errorf("HOSTNAME = %q, want iStoreOS-DT（必须用内核真名覆盖继承值）", m["HOSTNAME"])
	}
	if m["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", m["TERM"])
	}
	if m["PS1"] != "\\u@\\h:\\w\\$ " {
		t.Errorf("PS1 = %q, 转义被吃掉了", m["PS1"])
	}
	// 无关变量必须原样保留，否则 PATH 之类丢掉会让面板里连命令都找不到
	if m["PATH"] != "/usr/sbin:/usr/bin" {
		t.Errorf("PATH = %q, 无关变量不该被改动", m["PATH"])
	}
	if m["PWD"] != "/" {
		t.Errorf("PWD = %q, 无关变量不该被改动", m["PWD"])
	}
	// 注入的四个值必须真的在数组里（而不只是 map 去重后的假象）
	for _, want := range []string{"HOME=/root", "TERM=xterm-256color", "HOSTNAME=iStoreOS-DT"} {
		found := false
		for _, kv := range env {
			if kv == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("环境数组里找不到精确项 %q", want)
		}
	}
}

// 空环境也不能崩，且四个键一定注入齐全。
func TestBuildShellEnvEmptyBase(t *testing.T) {
	m := envMap(t, buildShellEnv(nil, "/root", "host"))
	for _, k := range []string{"HOME", "PS1", "TERM", "HOSTNAME"} {
		if m[k] == "" {
			t.Errorf("空环境下 %s 没有被注入", k)
		}
	}
}

// 前缀匹配不能误伤：HOMEBREW_* / TERMINFO 之类不能被当成 HOME=/TERM= 摘掉，
// 否则会把用户环境里的变量删掉（这是 Prefix 写法最容易踩的坑）。
func TestBuildShellEnvPrefixNotOvermatched(t *testing.T) {
	m := envMap(t, buildShellEnv([]string{
		"HOMEBREW_PREFIX=/opt/homebrew",
		"TERMINFO=/usr/share/terminfo",
		"HOSTNAME_SUFFIX=x",
	}, "/root", "host"))
	for _, k := range []string{"HOMEBREW_PREFIX", "TERMINFO", "HOSTNAME_SUFFIX"} {
		if m[k] == "" {
			t.Errorf("%s 被误删了（前缀误伤）", k)
		}
	}
}
