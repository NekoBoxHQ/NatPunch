package pmux

import (
	"strings"
	"testing"
)

// cutHeaderLine 必须大小写不敏感、容忍冒号前空白 —— 否则 "HOST:" / "Host :"
// 这类写法会让复用端口一路读到 8KB 上限才丢弃连接。
func TestCutHeaderLine(t *testing.T) {
	cases := []struct {
		in      string
		wantKey string
		wantVal string
		wantOk  bool
	}{
		{"Host: a.example.com", "Host", " a.example.com", true},
		{"HOST:a.example.com", "HOST", "a.example.com", true},
		{"Host : a.example.com", "Host", " a.example.com", true},
		{"GET / HTTP/1.1", "", "", false},
		{": empty-key", "", "", false},
	}
	for _, c := range cases {
		k, v, ok := cutHeaderLine(c.in)
		if ok != c.wantOk || k != c.wantKey || v != c.wantVal {
			t.Errorf("cutHeaderLine(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, k, v, ok, c.wantKey, c.wantVal, c.wantOk)
		}
	}
	// Host 值要能按大小写不敏感的方式匹配出来
	_, v, _ := cutHeaderLine("hOsT:  1.2.3.4:8080 ")
	if got := strings.TrimSpace(v); got != "1.2.3.4:8080" {
		t.Errorf("host value = %q", got)
	}
}
