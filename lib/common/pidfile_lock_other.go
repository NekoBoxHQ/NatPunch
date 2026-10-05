//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package common

import "os"

// pidFileLockSupported 本平台不支持 flock，改用 ownPidFilePortable 的
// 「O_CREATE|O_EXCL + 存活判断」路径（见 pidfile.go）。
const pidFileLockSupported = false

// lockPidFile 仅用于让本文件与 unix 版本保持相同符号；本平台不会被调用。
func lockPidFile(_ *os.File) bool { return false }
