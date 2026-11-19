package bridge

import (
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
)

// ping 巡检是“先快照、后删”，中间客户端可能已重连换了新 tunnel。
// 快照与当前不一致时必须跳过删除，否则会把刚建立的 MAIN 踢掉（表现为“偶发掉两次”）。
func TestTunnelStillCurrentIdentity(t *testing.T) {
	oldTunnel := new(natpunch_mux.Mux)
	newTunnel := new(natpunch_mux.Mux)

	// 快照仍是当前 tunnel → 可以删
	cl := &Client{tunnel: oldTunnel}
	if !cl.tunnelStillCurrent(oldTunnel) {
		t.Fatal("快照与当前 tunnel 相同时应判定为仍有效")
	}

	// 客户端已重连 → 快照过期，不能按它删
	cl = &Client{tunnel: newTunnel}
	if cl.tunnelStillCurrent(oldTunnel) {
		t.Fatal("客户端已换新 tunnel，旧快照必须判定为过期")
	}

	// nil 快照（半连接分支）不参与身份校验
	cl = &Client{}
	if cl.tunnelStillCurrent(nil) {
		t.Fatal("nil 快照不应判定为有效")
	}
	var nilClient *Client
	if nilClient.tunnelStillCurrent(oldTunnel) {
		t.Fatal("nil client 不应判定为有效")
	}
}
