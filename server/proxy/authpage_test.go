package proxy

import (
	"strings"
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/goroutine"
)

// 服务端必须把「IP 白名单授权页」接上。
//
// 没接上的话，不在白名单的访客会收到一个**空白的** 401 页面 ——
// lib/goroutine.CopyBuffer 在那条分支里只负责把 AuthPageHTML() 丢回去。
// 之前这里是直接 import web 读的，改成注入之后接线就成了一件必须被钉住的事。
//
// （反向那半 —— 客户端必须**没有**接 —— 用 go list -deps ./client 查更直接，
// 见升级/清理的说明：客户端恒传 task = nil，走不到这条分支，接上等于白背
// 整个面板的静态资源。）
func TestAuthPageHTMLWiredOnServer(t *testing.T) {
	if goroutine.AuthPageHTML == nil {
		t.Fatal("server/proxy 没有注入 goroutine.AuthPageHTML：IP 白名单授权页会返回空白")
	}
	page := goroutine.AuthPageHTML()
	if len(page) == 0 {
		t.Fatal("goroutine.AuthPageHTML() 返回空：web/static/page/auth.html 读不到")
	}
	if !strings.Contains(page, "${ip}") {
		t.Fatalf("授权页里没有 ${ip} 占位符，替换 IP 的那步会失效（len=%d）", len(page))
	}
}
