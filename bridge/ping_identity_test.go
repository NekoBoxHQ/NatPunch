package bridge

import (
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
)

// ping 巡检是“先快照、后删”，中间客户端可能已重连。
// 快照与当前不一致时必须跳过删除，否则会把刚建立的 MAIN 踢掉（表现为“偶发掉两次”）。
//
// 必须同时比对 tunnel 与 signal：客户端重连时 MAIN 先到、CHAN 后到，
// 只比 tunnel 会在“新 MAIN 已上、新 CHAN 未到”的间隙放行删除。
func TestConnStillCurrentIdentity(t *testing.T) {
	oldTunnel := new(natpunch_mux.Mux)
	newTunnel := new(natpunch_mux.Mux)
	oldSignal := conn.NewConn(nil)
	newSignal := conn.NewConn(nil)

	// 快照与当前完全一致 → 可以删
	cl := &Client{tunnel: oldTunnel, signal: oldSignal}
	if !cl.connStillCurrent(oldTunnel, oldSignal) {
		t.Fatal("快照与当前一致时应判定为仍然有效")
	}

	// CHAN 已重连（MAIN 还没换）→ 过期
	cl = &Client{tunnel: newTunnel, signal: oldSignal}
	if cl.connStillCurrent(oldTunnel, oldSignal) {
		t.Fatal("CHAN 已换新 tunnel 时旧快照必须判定为过期")
	}

	// MAIN 已重连、CHAN 尚未到（tunnel 仍是旧的）→ 同样过期，
	// 这是只比 tunnel 时漏掉的那个窗口。
	cl = &Client{tunnel: oldTunnel, signal: newSignal}
	if cl.connStillCurrent(oldTunnel, oldSignal) {
		t.Fatal("MAIN 已换新连接、tunnel 未变时，旧快照也必须判定为过期")
	}

	// 半连接快照（tunnel 为 nil）也要参与比对
	cl = &Client{tunnel: nil, signal: oldSignal}
	if !cl.connStillCurrent(nil, oldSignal) {
		t.Fatal("半连接快照与当前一致时应判定为仍然有效")
	}
	if cl.connStillCurrent(nil, newSignal) {
		t.Fatal("半连接状态下 signal 被替换时，旧快照必须判定为过期")
	}

	// 已被整体替换（tunnel 与 signal 都换了）
	cl = &Client{tunnel: newTunnel, signal: newSignal}
	if cl.connStillCurrent(oldTunnel, oldSignal) {
		t.Fatal("客户端已整体重连时旧快照必须判定为过期")
	}

	var nilClient *Client
	if nilClient.connStillCurrent(oldTunnel, oldSignal) {
		t.Fatal("nil client 不应判定为有效")
	}
}
