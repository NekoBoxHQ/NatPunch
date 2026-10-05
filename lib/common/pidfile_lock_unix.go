//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package common

import (
	"os"
	"syscall"
)

// pidFileLockSupported 本平台支持 OS 级 pid 文件锁（编译期常量，见 pidfile.go 的使用）。
const pidFileLockSupported = true

// lockPidFile 对已打开的 pid 文件加非阻塞排他锁（flock）。
//
// flock 锁属于「打开文件描述」，内核会在进程退出时（包括 SIGKILL）自动释放，
// 因此不存在陈旧文件与 PID 复用误判 —— 拿不到锁就是真有实例在跑。
// 同一进程对同一路径再次 open+flock 也会冲突，故并发调用天然只有一个成功。
func lockPidFile(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
