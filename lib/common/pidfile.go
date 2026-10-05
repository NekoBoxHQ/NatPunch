package common

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// isProcessAlive 是 IsProcessAlive 的间接层，便于测试注入。
// 平台实现差异较大（Windows 上恒为 false，不做强校验），注入后可在任意平台
// 验证单实例判定的完整逻辑（见 pidfile_test.go）。
var isProcessAlive = IsProcessAlive

// heldPidFile 持有中的 pid 文件句柄。
// 走 flock 路径时**必须保持打开**：锁随句柄关闭而释放，进程退出（含 SIGKILL）由内核回收。
var heldPidFile *os.File

// OwnPidFile 以原子方式占用 pid 文件；成功返回 nil，失败返回原因（通常表示已有实例在运行）。
//
// 先尝试 OS 级排他文件锁（flock，见 pidfile_lock_unix.go）：锁由内核在进程退出时
// 自动释放，因此既无 TOCTOU 窗口，也不存在陈旧文件与 PID 复用误判。
// 平台不支持锁时退回 ownPidFilePortable（O_CREATE|O_EXCL + 存活判断），
// 该路径可消除 TOCTOU，但残留"pid 复用导致误判已在运行"（失败方向安全）。
//
// 注意：不支持锁的平台**必须直接走可移植路径**，不可先 open(O_CREATE) 再判断 ——
// open 本身就会创建文件，会让后续的 O_EXCL 永远拿到 EEXIST。
func OwnPidFile(path string) error {
	if !pidFileLockSupported {
		return ownPidFilePortable(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if !lockPidFile(f) {
		b, _ := io.ReadAll(f)
		_ = f.Close()
		owner := strings.TrimSpace(string(b))
		if owner == "" {
			owner = "unknown"
		}
		return fmt.Errorf("already running (pid %s)", owner)
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Sync()
	heldPidFile = f // 保持打开以持有锁，直到进程退出
	return nil
}

// ownPidFilePortable 不支持文件锁的平台所用的占用方式：
// O_CREATE|O_EXCL 主张所有权（内核保证同一路径只有一个创建者成功，消除 TOCTOU）。
// 若文件已存在，读出其中的 pid：
//   - 进程存活 → 拒绝启动；
//   - 进程已死 → 视为陈旧文件（例如被 SIGKILL），删除后重试一次独占创建。
//
// 文件**不在退出时删除**：unlink 与"另一进程已打开同一 inode 并接管"之间会形成竞态。
// 已知残余：pid 复用会误判为"已在运行"（失败方向安全）。
func ownPidFilePortable(path string) error {
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
