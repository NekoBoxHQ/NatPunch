//go:build windows

package common

// IsProcessAlive 判断 pid 对应的进程是否存活（Windows）。
// Windows 上不做强校验（避免权限/句柄问题），单实例由服务管理方式保证。
func IsProcessAlive(pid int) bool {
	return false
}
