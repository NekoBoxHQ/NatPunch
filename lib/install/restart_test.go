package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestartHelperIsPOSIXClean 语法必须干净：这个脚本是升级流程的最后一步，
// 由 sh 在别人机器上执行，语法错就等于「二进制换了、服务没重启」——静默停在旧版本，
// 而且命令行上看着像成功了。
func TestRestartHelperIsPOSIXClean(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("环境里没有 sh，跳过语法检查")
	}
	p := filepath.Join(t.TempDir(), "natpunch_restart.test")
	if err := os.WriteFile(p, []byte(restartHelper), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sh, "-n", p).CombinedOutput(); err != nil {
		t.Fatalf("重启脚本语法错误: %v\n%s", err, out)
	}
}

// TestRestartHelperSelfDeleteIsSynchronous 钉住 2026-10-06 那条教训：
// 脚本在 `systemd-run --unit=... --collect` 的独立单元里跑，主进程一退出 systemd
// 立刻按 cgroup 清场，**丢给后台子 shell 的 rm 会被一起 SIGTERM、永远执行不到**，
// 于是 /tmp 里只增不减（真机上攒过 4 份 natpunch_apply.*）。自删必须同步做。
func TestRestartHelperSelfDeleteIsSynchronous(t *testing.T) {
	if !strings.Contains(restartHelper, `case "$0" in /tmp/natpunch_restart.*) rm -f "$0"`) {
		t.Error("自删没有限定在 /tmp/natpunch_restart.* 形态上（可能删到不相干的东西）")
	}
	for _, line := range strings.Split(restartHelper, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ") &" || strings.HasPrefix(trimmed, "( sleep") {
			t.Errorf("出现了后台子 shell 自删：%q —— --collect 下会被 cgroup 清场连坐", trimmed)
		}
	}
	// 裸的 rm -f "$0"（不带形态限定）也不允许
	if strings.Contains(restartHelper, "\nrm -f \"$0\"") {
		t.Error("有无条件的 rm -f \"$0\"，应当限定形态")
	}
}

// TestRestartHelperOnlyRestartsRunningService 服务本来就没在跑就不该被拉起来：
// 有人可能是故意停掉的，换二进制顺手启动属于改变别人机器的运行状态。
func TestRestartHelperOnlyRestartsRunningService(t *testing.T) {
	if !strings.Contains(restartHelper, "if ! running; then") {
		t.Error("缺少「服务没在跑就别动它」的判断")
	}
	if !strings.Contains(restartHelper, "未在运行，不自动重启") {
		t.Error("未运行时没有留下可排查的日志")
	}
}

// TestUpdatePathsCallRestart 钉住回归：两条 update 路径在替换完二进制之后都必须
// 真的去重启。原来只打印一句「更新成功，请重启服务」—— 而这两个子命令经常是从
// 「服务端 → 客户端隧道 → 目标机」的 SSH 上敲的，提示你重启等于让你把自己踢下线，
// 服务就此停着没人拉起来。shell 那条路（uninstall_client.sh update）早就有
// 脱离+看门狗，Go 这条一直缺。
func TestUpdatePathsCallRestart(t *testing.T) {
	src, err := os.ReadFile("install.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`restartServiceDetached("natpunch", "natpunch")`,
		`restartServiceDetached("natpunch-client", "natpunch-client")`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install.go 里缺少 %s —— 替换完二进制没有触发重启", want)
		}
	}
	// 反向断言：不该再出现「只提示、不重启」的老文案
	for _, bad := range []string{"更新成功，请重启服务", "更新成功，请重启客户端"} {
		if strings.Contains(s, bad) {
			t.Errorf("install.go 里仍有只提示不重启的老文案: %q", bad)
		}
	}
}

// TestRestartScriptPathIsUnderTmp 脚本落在 /tmp 下，与 uninstall_client.sh 的
// /tmp/natpunch_apply.$$ 保持一致；自删的形态限定也依赖这个前缀。
func TestRestartScriptPathIsUnderTmp(t *testing.T) {
	src, err := os.ReadFile("restart.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"/tmp/natpunch_restart.%d"`) {
		t.Error("重启脚本路径不是 /tmp/natpunch_restart.<pid> —— 与自删形态限定不一致")
	}
}
