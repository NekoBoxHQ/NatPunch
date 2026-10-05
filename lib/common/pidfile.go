package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// isProcessAlive 是 IsProcessAlive 的间接层，便于测试注入。
// 平台实现差异较大（Windows 上恒为 false，不做强校验），注入后可在任意平台
// 验证单实例判定的完整逻辑（见 pidfile_test.go）。
var isProcessAlive = IsProcessAlive

// OwnPidFile 以原子方式占用 pid 文件；成功返回 nil，失败返回原因（通常表示已有实例在运行）。
//
// 原实现是「先 ReadFile 判断存活，再 WriteFile 覆盖」，两步之间存在 TOCTOU 竞态：
// 两个进程可同时通过存活检查，随后各自无条件截断写入，双双认为自己拿到单实例，
// 结果是两个进程同时运行、各持内存副本互写 clients.json（复评🟡）。
//
// 现改为用 O_CREATE|O_EXCL 主张所有权：内核保证同一路径只有一个创建者成功，
// 不存在"两者都通过"的窗口。若文件已存在，读出其中的 pid：
//   - 进程存活   → 拒绝启动（返回错误）；
//   - 进程已死   → 视为陈旧文件（例如被 SIGKILL，defer 未执行），删除后重试一次独占创建。
//
// 文件**不在退出时删除**：unlink 与"另一进程已打开同一 inode 并接管"之间会形成竞态
// （unlink 后第三方可创建新文件，从而出现两个持有者）。保留文件时，下一实例打开同一路径继续判断。
//
// 已知残余：pid 复用 —— 陈旧文件中的 pid 恰被无关进程占用时会误判为"已在运行"。
// 该情形失败方向是安全的（拒绝启动，而非双实例）；彻底消除需 OS 级文件锁（flock）。
func OwnPidFile(path string) error {
	err := tryCreateExclusive(path)
	if err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return err
	}

	// 文件已存在：读出 pid 判断持有者是否仍存活
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return rerr
	}
	pid, aerr := strconv.Atoi(strings.TrimSpace(string(b)))
	if aerr != nil || pid <= 0 {
		// 内容为空/不可解析：另一实例可能正处于「已创建、尚未写入 pid」的瞬间
		// （O_CREATE 与 WriteString 之间不是原子的）。此时保守拒绝，
		// 切不可当作陈旧文件删除 —— 那会删掉正在启动实例的 pid 文件，使两者都通过检查。
		return fmt.Errorf("pid file %s exists but holds no valid pid; remove it manually if no instance is running", path)
	}
	if isProcessAlive(pid) {
		return fmt.Errorf("already running (pid %d)", pid)
	}

	// 陈旧文件（持有者已死，例如被 SIGKILL）：删除后重试一次独占创建；若被他人抢先则本次退出
	if rmerr := os.Remove(path); rmerr != nil && !os.IsNotExist(rmerr) {
		return rmerr
	}
	if err = tryCreateExclusive(path); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("another instance took the pid file concurrently")
		}
		return err
	}
	return nil
}

// tryCreateExclusive 以 O_CREATE|O_EXCL 创建 pid 文件并写入当前进程 pid。
// 失败时不会留下半成品文件。
func tryCreateExclusive(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}
