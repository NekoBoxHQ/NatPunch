package controllers

import (
	"os"
	"strings"
	"testing"
)

// ClientController.Edit 会遍历所有客户端做 vkey/用户名唯一性校验；
// 这些校验必须发生在 c.Lock() 之前。否则遍历到当前 client 时会再次请求
// 它自己的读锁，形成永久自锁，表现为保存客户端一直转圈。
func TestClientEditValidatesBeforeClientWriteLock(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (s *ClientController) Edit()")
	if start < 0 {
		t.Fatal("ClientController.Edit not found")
	}
	end := strings.Index(text[start:], "func RemoveRepeatedElement")
	if end < 0 {
		t.Fatal("end of ClientController.Edit not found")
	}
	editBody := text[start : start+end]

	verify := strings.Index(editBody, "VerifyVkey(")
	lock := strings.Index(editBody, "c.Lock()")
	if verify < 0 || lock < 0 {
		t.Fatalf("required calls not found: VerifyVkey=%d c.Lock=%d", verify, lock)
	}
	if verify > lock {
		t.Fatal("ClientController.Edit calls VerifyVkey while holding c.Lock; this self-deadlocks on the current client")
	}
}
