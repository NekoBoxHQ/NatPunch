package proxy

import "sync/atomic"

// 全局连接数上限（阶段三 #4）：max_global_conn=0 表示不限（存量语义）。
// 计数覆盖所有走到 DealClient 的隧道转发连接，防止全局连接数打爆内存。
var (
	maxGlobalConn int64
	globalConn    int64
)

// SetMaxGlobalConn 由服务端启动时按配置设置（0 = 不限）
func SetMaxGlobalConn(n int64) {
	atomic.StoreInt64(&maxGlobalConn, n)
}

// TryAcquireGlobalConn 尝试占用一个全局连接名额
func TryAcquireGlobalConn() bool {
	for {
		cur := atomic.LoadInt64(&globalConn)
		if atomic.LoadInt64(&maxGlobalConn) > 0 && cur >= atomic.LoadInt64(&maxGlobalConn) {
			return false
		}
		if atomic.CompareAndSwapInt64(&globalConn, cur, cur+1) {
			return true
		}
	}
}

// ReleaseGlobalConn 释放一个全局连接名额
func ReleaseGlobalConn() {
	atomic.AddInt64(&globalConn, -1)
}

// GetGlobalConn 当前全局连接数（展示用）
func GetGlobalConn() int64 {
	return atomic.LoadInt64(&globalConn)
}
