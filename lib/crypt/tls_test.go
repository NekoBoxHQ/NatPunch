package crypt

import (
	"crypto/tls"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// F2-2 回归：桥接证书持久化后指纹稳定（重启/重载不变，升级不换指纹）
func TestInitTlsPersistentFingerprint(t *testing.T) {
	dir := t.TempDir()
	pem := filepath.Join(dir, "bridge.pem")
	key := filepath.Join(dir, "bridge.key")

	if err := InitTls(pem, key); err != nil {
		t.Fatalf("first InitTls: %v", err)
	}
	fp1 := GetCertFingerprint()
	if len(fp1) != 64 {
		t.Fatalf("fingerprint len = %d, want 64 hex", len(fp1))
	}
	// 重新初始化（模拟重启）：必须复用落盘证书，指纹不变
	if err := InitTls(pem, key); err != nil {
		t.Fatalf("reload InitTls: %v", err)
	}
	if fp2 := GetCertFingerprint(); fp2 != fp1 {
		t.Fatalf("fingerprint changed after reload: %s -> %s", fp1, fp2)
	}
	for _, p := range []string{pem, key} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("cert file missing: %v", err)
		}
		// Windows 不强制 POSIX 权限位（统一报 0666），权限断言仅对部署目标（Linux/OpenWrt）生效
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0600 {
			t.Fatalf("cert file %s perm = %o, want 600", p, fi.Mode().Perm())
		}
	}
}

// F2-2 三态：正确指纹握手成功；错误指纹握手失败；空指纹（旧行为）握手成功
func TestTlsFingerprintThreeStates(t *testing.T) {
	dir := t.TempDir()
	if err := InitTls(filepath.Join(dir, "bridge.pem"), filepath.Join(dir, "bridge.key")); err != nil {
		t.Fatalf("InitTls: %v", err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", BuildTlsServerConfig())
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serve := func(done chan<- error) {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		buf := make([]byte, 1)
		_, err = io.ReadFull(c, buf)
		done <- err
	}

	handshake := func(fp string, wantOK bool) {
		SetTlsFingerprint(fp)
		conf := TlsDialConfig()
		conn, err := tls.Dial("tcp", ln.Addr().String(), conf)
		if wantOK {
			if err != nil {
				t.Fatalf("fp=%q want OK, got %v", fp, err)
			}
			defer conn.Close()
			conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			conn.Write([]byte{1})
		} else {
			if err == nil {
				t.Fatalf("fp=%q want handshake failure, got success", fp)
			}
			return
		}
	}

	done := make(chan error, 4)
	go serve(done)
	handshake(GetCertFingerprint(), true) // 正确指纹 → 成功
	if err := <-done; err != nil {
		t.Fatalf("server side error on correct fp: %v", err)
	}

	go serve(done)
	handshake("00"+GetCertFingerprint()[2:], false) // 错误指纹 → 握手失败
	if err := <-done; err == nil {
		t.Fatal("server accepted bad-fingerprint client, want handshake rejection")
	}

	go serve(done)
	handshake("", true) // 空指纹 → 旧行为（InsecureSkipVerify）→ 成功
	if err := <-done; err != nil {
		t.Fatalf("server side error on empty fp: %v", err)
	}
	SetTlsFingerprint("")
}
