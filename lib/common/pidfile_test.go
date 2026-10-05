package common

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// withAliveStub 把存活判定替换为"仅本进程存活"。
// 平台实现差异大（Windows 的 IsProcessAlive 恒为 false），注入后可让单实例判定的
// 完整逻辑在任意平台被真实执行到，而不是被 skip 掉。
func withAliveStub(t *testing.T) {
	t.Helper()
	old := isProcessAlive
	isProcessAlive = func(pid int) bool { return pid == os.Getpid() }
	t.Cleanup(func() { isProcessAlive = old })
}

// TestOwnPidFile_LiveOwnerRejected：持有者存活时必须拒绝第二个实例。
// 原实现用「读文件判断存活 → 无条件 WriteFile 覆盖」，此处验证不会再覆盖。
func TestOwnPidFile_LiveOwnerRejected(t *testing.T) {
	withAliveStub(t)
	path := filepath.Join(t.TempDir(), "natpunch.pid")

	if err := OwnPidFile(path); err != nil {
		t.Fatalf("首次占用应成功，却返回: %v", err)
	}
	if err := OwnPidFile(path); err == nil {
		t.Fatal("持有者存活时，第二次占用必须被拒绝")
	}
	// 且不得被第二次调用覆盖
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid 文件被覆盖，期望 %d，实际 %q", os.Getpid(), string(b))
	}
}

// TestOwnPidFile_ReclaimsStaleFile：持有者已死（SIGKILL 后残留）时必须能接管。
func TestOwnPidFile_ReclaimsStaleFile(t *testing.T) {
	withAliveStub(t)
	path := filepath.Join(t.TempDir(), "natpunch.pid")
	if err := os.WriteFile(path, []byte("999999"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := OwnPidFile(path); err != nil {
		t.Fatalf("陈旧 pid 文件应可接管，却返回: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("接管后应写入自身 pid，实际 %q", string(b))
	}
}

// TestOwnPidFile_EmptyFileRejected：可移植路径下，内容为空（另一实例刚创建、尚未写入 pid）
// 时必须保守拒绝，绝不能当作陈旧文件删除 —— 删除会让两个实例都通过检查，正是要消除的竞态。
// 走 flock 的平台无需该规则：拿不到锁就是真有实例，不存在这种歧义，故直接测可移植实现。
func TestOwnPidFile_EmptyFileRejected(t *testing.T) {
	withAliveStub(t)
	path := filepath.Join(t.TempDir(), "natpunch.pid")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ownPidFilePortable(path); err == nil {
		t.Fatal("空 pid 文件必须被拒绝（保守策略），而不是被接管")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("空 pid 文件不应被删除: %v", err)
	}
}

// TestOwnPidFile_ConcurrentOnlyOneWins：并发抢占时恰好一个成功。
// 这是对原 TOCTOU 的直接回归测试：原实现（读→判断→无条件覆盖）下多个调用都会"成功"。
func TestOwnPidFile_ConcurrentOnlyOneWins(t *testing.T) {
	withAliveStub(t)
	path := filepath.Join(t.TempDir(), "natpunch.pid")

	const n = 32
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := OwnPidFile(path); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d 个并发实例中应恰好 1 个成功，实际 %d", n, wins)
	}
}
