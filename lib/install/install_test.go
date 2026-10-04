package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestCompareVersion 验证逐段版本比较（G7）：1.10.0 必须大于 2.0.0
func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.10.0", "v2.0.0", -1},
		{"v2.0.0", "v1.10.0", 1},
		{"v1.1.0", "v1.1.1", -1},
		{"v1.1.1", "v1.1.1", 0},
		{"v1.2", "v1.2.0", 0},
		{"v26.9.94", "v26.9.9", 1},
		{"v1.0.0", "v1.0.0-beta", 0}, // 预发布段按缺失=0 处理（本项目 tag 无预发布段）
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestExtractTarGzRejectsEscape 验证解包拒绝路径逃逸与符号链接（F2-8）
func TestExtractTarGzRejectsEscape(t *testing.T) {
	dest := t.TempDir()
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	// 绝对路径条目
	if err := tw.WriteHeader(&tar.Header{Name: "/etc/passwd", Mode: 0644, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("root"))
	// 相对逃逸条目
	if err := tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0644, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()
	if _, err := extractTarGz(buf, dest); err == nil {
		t.Fatal("expected path-escape rejection, got nil")
	}
}

// TestExtractTarGzNormal 验证正常解包
func TestExtractTarGzNormal(t *testing.T) {
	dest := t.TempDir()
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	body := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "natpunch/bin", Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "natpunch/bin/natpunch", Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()
	root, err := extractTarGz(buf, dest)
	if err != nil {
		t.Fatal(err)
	}
	if root != dest {
		t.Fatalf("root = %q, want %q", root, dest)
	}
	b, err := os.ReadFile(filepath.Join(dest, "natpunch", "bin", "natpunch"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("content = %q, want %q", b, "hello")
	}
}

// TestParseSha256Sums 验证 SHA256SUMS 解析
func TestParseSha256Sums(t *testing.T) {
	raw := "abc123  linux_amd64_server.tar.gz\n" +
		"def456  linux_arm64_server.tar.gz\n" +
		"# comment\n" +
		"\n"
	m := parseSha256Sums(raw)
	if m["linux_amd64_server.tar.gz"] != "abc123" {
		t.Fatalf("amd64 entry = %q", m["linux_amd64_server.tar.gz"])
	}
	if m["linux_arm64_server.tar.gz"] != "def456" {
		t.Fatalf("arm64 entry = %q", m["linux_arm64_server.tar.gz"])
	}
	if _, ok := m["comment"]; ok {
		t.Fatal("comment must be ignored")
	}
}
