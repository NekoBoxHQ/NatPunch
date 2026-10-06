package common

import "syscall"

// IsProcessAlive 判断 pid 对应的进程是否存活。
// 单实例保护用：进程被杀后 pid 文件残留，但该 pid 已无存活进程时放行。
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
