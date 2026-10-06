package controllers

import (
	"fmt"
	"testing"

	"github.com/astaxie/beego"
)

// 面板终端 /terminal/ws 每开一次就让服务端 panic 一次的那个坑，闸门只有一个：
// beego 在 action 返回之后判断
//
//	if !context.ResponseWriter.Started && context.Output.Status == 0 {
//	    if BConfig.WebConfig.AutoRender { execController.Render() }
//	}
//
// 而 WebSocket 的 hijack 不经过 beego 的 Write/WriteHeader —— 这两个条件恒成立，
// 于是它按 控制器名/动作名 去渲染 views/terminalcontroller/ws.tpl，而那个模板不存在
// → template.go:75 panic → 被 beego 的 recover 抓住，打一整串 [C] 级别堆栈。
// 成功的终端会话结束时也走这个判断，所以频率是"每开一次终端 panic 一次"。
//
// TerminalController.Ws() 里那行 s.EnableRender = false 就是关掉它。
// 本测试把"闸门"本身钉住：同一个不存在的模板名，EnableRender=true 必须 panic、
// false 必须安静返回 nil。哪天 beego 升级或有人改动了 Render() 里的这个判断，这里先红。
//
// 真机实测过的量级：升级前当天 24 条 [C] Handler crashed 全部来自 /terminal/ws；
// 把堆栈行拆掉之后，当天真实的 [E] 错误是 0 条 —— 也就是说这些 [C] 一直在掩盖真错误。
func TestEnableRenderIsTheTerminalPanicGate(t *testing.T) {
	c := &beego.Controller{}
	// 故意指向一个不存在的模板（就是真机上那个名字）
	c.TplName = "terminalcontroller/ws.tpl"

	c.EnableRender = true
	if err := renderRecoverPanic(c); err == nil {
		t.Fatal("EnableRender=true 渲染不存在的模板居然没 panic —— Render() 里的闸门判断变了？")
	}

	c.EnableRender = false
	if err := renderRecoverPanic(c); err != nil {
		t.Fatalf("EnableRender=false 时 Render() 仍出错（%v）—— 关不掉渲染，Ws() 会继续每次开终端都 panic", err)
	}
}

// renderRecoverPanic 调用 Render()，把它内部的 panic 转成 error 返回。
// 模板不存在时 beego 是 panic 而不是返回 error，所以必须 recover 才能断言。
func renderRecoverPanic(c *beego.Controller) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Render()
}
