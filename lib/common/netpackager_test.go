package common

import (
	"bytes"
	"testing"
)

// F1-3 回归测试：UDP 报文长度字段不再被完全信任。
// 门禁：Rsv=0xFFFF 头 → 返回 error 不 panic；Rsv=0 正常载荷回归通过；
//       Rsv=0 且载荷超过缓冲区上限 → 不 panic、数据被截断到上限。

func TestReadUDPDatagramRejectsHugeRsv(t *testing.T) {
	// 头: Rsv=0xFFFF, Frag=0, atype=ipV4, addr=1.2.3.4:8080
	header := []byte{0xFF, 0xFF, 0x00, 0x01, 0x01, 0x02, 0x03, 0x04, 0x1F, 0x90}
	payload := bytes.Repeat([]byte{0xAA}, 64)
	_, err := ReadUDPDatagram(bytes.NewReader(append(header, payload...)))
	if err == nil {
		t.Fatal("expected error for Rsv=0xFFFF, got nil (no panic is not enough)")
	}
}

func TestReadUDPDatagramNormal(t *testing.T) {
	// 标准 SOCKS5 UDP 数据报：Rsv=0, Frag=0, atype=ipV4, addr=1.2.3.4:8080, data="hello"
	packet := append([]byte{0x00, 0x00, 0x00, 0x01, 0x01, 0x02, 0x03, 0x04, 0x1F, 0x90}, []byte("hello")...)
	d, err := ReadUDPDatagram(bytes.NewReader(packet))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(d.Data) != "hello" {
		t.Fatalf("data mismatch: got %q", d.Data)
	}
	if d.Header.Addr == nil || d.Header.Addr.Host != "1.2.3.4" || d.Header.Addr.Port != 8080 {
		t.Fatalf("addr mismatch: %+v", d.Header.Addr)
	}
}

func TestReadUDPDatagramPayloadAtBufferLimit(t *testing.T) {
	// Rsv=0 且载荷远大于缓冲区剩余容量：不得 panic，数据必须被截断到缓冲区上限以内。
	header := []byte{0x00, 0x00, 0x00, 0x01, 0x01, 0x02, 0x03, 0x04, 0x1F, 0x90}
	payload := bytes.Repeat([]byte{0xBB}, PoolSizeUdp*2)
	d, err := ReadUDPDatagram(bytes.NewReader(append(header, payload...)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.Data) > PoolSizeUdp {
		t.Fatalf("data length %d exceeds buffer cap %d", len(d.Data), PoolSizeUdp)
	}
	if len(d.Data) == 0 {
		t.Fatal("expected truncated data, got empty")
	}
}
