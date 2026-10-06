package install

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
)

// restartHelper 是「稍后把服务重启起来」的独立脚本，由 update 子命令生成后
// 交给一个脱离当前会话的进程执行。参数：$1=服务名  $2=可执行文件名。
//
// 为什么必须脱离：update 子命令（natpunch update / natpunch-client update）是一次性
// 进程 —— 它只把磁盘上的二进制换掉就退出，**正在跑的那个服务内存里还是旧代码**。
// 而这条命令经常是从「服务端 → 客户端隧道 → 目标机」的 SSH 上敲的（维护者的常态）：
// 原地重启会先把服务停掉、承载这次 SSH 的隧道随之断开，命令自己就被踢下线，
// 服务停在「已停止」再没人拉起来。所以交给一个不受本次会话/cgroup 影响的进程去做，
// 与 uninstall_client.sh 里那个 apply 脚本是同一套路。
const restartHelper = `#!/bin/sh
# 由 NatPunch 的 update 子命令生成。参数：$1=服务名  $2=可执行文件名
set -u
SVC="$1"
BIN="$2"
LOG="${NP_UPDATE_LOG:-/tmp/natpunch_update.log}"
say() { echo "$(date '+%F %T') $*" >>"$LOG" 2>/dev/null; }

# 等一会儿再动手：让父进程先把输出刷出去、让调用方（面板 / 脚本）先拿到返回。
sleep "${NP_RESTART_DELAY:-4}"

# 按 /proc/<pid>/exe 的 basename 判断服务在不在跑。busybox 与 GNU 都适用；
# 不用 pidof —— Debian 上它来自 sysvinit-utils，不保证装了。
running() {
    for d in /proc/[0-9]*; do
        [ -r "$d/exe" ] || continue
        e=$(basename "$(readlink "$d/exe" 2>/dev/null)" 2>/dev/null) || continue
        case "$e" in "$BIN"|"$BIN (deleted)") return 0 ;; esac
    done
    return 1
}

# 服务本来就没在跑就什么都别做：有人可能是故意停掉的，换个二进制不该顺手
# 把它拉起来（那属于改变别人机器的运行状态，不是升级该干的事）。
if ! running; then
    say "更新后 $SVC 未在运行，不自动重启（需要启动请手动执行）"
    case "$0" in /tmp/natpunch_restart.*) rm -f "$0" 2>/dev/null || true ;; esac
    exit 0
fi

do_restart() {
    if [ -x "/etc/init.d/$SVC" ]; then
        "/etc/init.d/$SVC" restart >>"$LOG" 2>&1 && return 0
        "/etc/init.d/$SVC" stop    >>"$LOG" 2>&1
        sleep 1
        "/etc/init.d/$SVC" start   >>"$LOG" 2>&1 && return 0
        return 1
    fi
    if command -v systemctl >/dev/null 2>&1; then
        systemctl restart "$SVC" >>"$LOG" 2>&1 && return 0
        systemctl start   "$SVC" >>"$LOG" 2>&1 && return 0
        return 1
    fi
    return 1
}

if do_restart; then
    say "更新后已自动重启 $SVC"
else
    say "更新后自动重启 $SVC 失败，请手动检查"
fi

# 自删必须**同步**做：这个脚本在 systemd-run --collect 的独立单元里跑，主进程一退出
# systemd 就按 cgroup 清场，丢给后台子 shell 的 rm 会被连坐掉、永远执行不到
# （2026-10-06 在 sg 上实测过：旧写法 4 秒后文件还在，同步写法当场就没了）。
# 只删这一种形态的 $0，别被别的调用方式带偏去删了不相干的东西。
case "$0" in /tmp/natpunch_restart.*) rm -f "$0" 2>/dev/null || true ;; esac
`

// restartServiceDetached 在二进制替换成功后，用一个脱离当前会话与 cgroup 的进程
// 把服务重启起来，让新二进制真正生效。
//
// service 是服务名（systemd unit 名 / init.d 脚本名），bin 是可执行文件名
// （用来判断服务当前是否在运行）。
//
// 拿不到可用的脱离手段时**不硬来**：原地重启比不重启更糟（会把调用方的会话一起
// 切断，而且服务停在半路），这时只打印人工提示，维持原来的行为。
func restartServiceDetached(service, bin string) {
	script := fmt.Sprintf("/tmp/natpunch_restart.%d", os.Getpid())
	if err := os.WriteFile(script, []byte(restartHelper), 0755); err != nil {
		log.Printf("写重启脚本失败，请手动重启 %s: %v", service, err)
		return
	}

	var cmd *exec.Cmd
	switch {
	case hasSystemdRun():
		// 独立单元 = 独立 cgroup：本次 SSH 会话随隧道断开时被清场也带不走它。
		unit := fmt.Sprintf("natpunch-restart-%d", os.Getpid())
		cmd = exec.Command("systemd-run", "--unit="+unit, "--collect",
			"/bin/sh", script, service, bin)
	default:
		// 没有 systemd（OpenWrt/procd 等）：setsid 换掉 session 就够了，
		// 那边不会因为服务停止而按 cgroup 清掉别的会话。
		cmd = exec.Command("/bin/sh", script, service, bin)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	// 不继承 stdin/stdout/stderr：父进程退出后它们可能指向已断开的管道，
	// 独立进程在里面写会吃到 SIGPIPE。脚本自己写日志文件。
	devNull, err := os.Open(os.DevNull)
	if err == nil {
		defer devNull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	}
	if err := cmd.Start(); err != nil {
		log.Printf("启动重启进程失败，请手动重启 %s: %v", service, err)
		_ = os.Remove(script)
		return
	}
	_ = cmd.Process.Release()
	fmt.Printf("已替换二进制，%s 将在几秒后自动重启（日志：/tmp/natpunch_update.log）\n", service)
	fmt.Println("若你的 SSH 正走这条隧道，连接会短暂断开后自动恢复。")
}

// hasSystemdRun 判断能不能用 systemd-run 起独立单元。
// 判据用 /run/systemd/system 是否存在（systemd 真的是 1 号进程），
// 不用 systemctl is-system-running —— 它在 degraded 状态下返回非零，
// 会把一台跑得好好的机器误判成「没有 systemd」。
func hasSystemdRun() bool {
	if st, err := os.Stat("/run/systemd/system"); err != nil || !st.IsDir() {
		return false
	}
	_, err := exec.LookPath("systemd-run")
	return err == nil
}
