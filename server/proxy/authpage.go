package proxy

import (
	"github.com/NekoBoxHQ/NatPunch/lib/goroutine"
	"github.com/NekoBoxHQ/NatPunch/web"
)

// 把面板里的「IP 白名单授权页」注入给 lib/goroutine。
//
// lib/goroutine.CopyBuffer 在转发第一条请求时会判断 task.Client.IpWhite：不在白名单
// 就把这张页面（把 ${ip} 换成对方 IP）当作 401 响应丢回去，让访客输密码自助放行。
//
// 这张页面存在 web/static 里，而 web 包用 go:embed 把整个面板（static + views，
// 约 3.6MB）编进二进制。lib/goroutine 被客户端也依赖着，直接 import web 等于让
// 客户端白白背上整个面板 —— 而客户端的转发调用恒传 task = nil，这段代码永远走不到。
//
// 所以改成注入：只有服务端（本包被 cmd/natpunch 引入）在 init 里接上，
// 客户端二进制里 AuthPageHTML 保持 nil，那条分支自然跳过。
func init() {
	goroutine.AuthPageHTML = func() string {
		b, _ := web.ReadStaticFile("page/auth.html")
		return string(b)
	}
}
