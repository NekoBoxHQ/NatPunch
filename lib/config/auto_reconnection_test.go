package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConf(t *testing.T, extra string) string {
	t.Helper()
	body := "[common]\nserver_addr=1.2.3.4:8025\nvkey=abcdef0123456789\nconn_type=tcp\n" + extra + "\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "client.conf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("写临时配置失败: %v", err)
	}
	return p
}

// 缺键必须默认 true：配置模式下客户端常驻，false 会让掉线后永久离线。
func TestAutoReconnectionDefaultsToTrueWhenKeyMissing(t *testing.T) {
	c, err := NewConfig(writeTempConf(t, ""))
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	if c.CommonConfig == nil {
		t.Fatal("CommonConfig 为 nil")
	}
	if !c.CommonConfig.AutoReconnection {
		t.Fatal("缺省 auto_reconnection 应为 true（否则掉线后永久离线）")
	}
}

// 显式 false 必须被尊重（用户明确要求不自动重连）。
func TestAutoReconnectionExplicitFalseIsHonored(t *testing.T) {
	c, err := NewConfig(writeTempConf(t, "auto_reconnection=false"))
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	if c.CommonConfig.AutoReconnection {
		t.Fatal("显式 auto_reconnection=false 应被尊重")
	}
}
