package routers

import (
	"github.com/NekoBoxHQ/NatPunch/web/controllers"
	"github.com/astaxie/beego"
)

func Init() {
	// beego 的 AutoRouter 会把控制器上**所有导出方法**都挂成路由（只排除一个固定名单：
	// Init/Prepare/Render/GetString…）。我们把 BaseController 的导出 helper 全挂进去了：
	//   /index/getintnoerr、/client/ajaxok、/terminal/setinfo …
	// 它们以 0 参数被反射调用 → 参数不足 panic → beego recover 成 500 并打一整串 [C]
	// 堆栈。任何登录用户都能拿它刷日志、把真错误埋掉。
	for _, m := range []string{
		"GetIntNoErr", "GetBoolNoErr", "AjaxOk", "AjaxOkWithId", "AjaxErr",
		"AjaxTable", "GetAjaxParams", "SetInfo", "SetType", "CheckUserAuth",
	} {
		beego.ExceptMethodAppend(m)
	}
	web_base_url := beego.AppConfig.String("web_base_url")
	if len(web_base_url) > 0 {
		ns := beego.NewNamespace(web_base_url,
			beego.NSRouter("/", &controllers.IndexController{}, "*:Index"),
			beego.NSAutoRouter(&controllers.IndexController{}),
			beego.NSAutoRouter(&controllers.LoginController{}),
			beego.NSRouter("/login/github", &controllers.LoginController{}, "get:GithubLogin"),
			beego.NSRouter("/login/github/callback", &controllers.LoginController{}, "get:GithubCallback"),
			beego.NSAutoRouter(&controllers.ClientController{}),
			beego.NSAutoRouter(&controllers.AuthController{}),
			beego.NSRouter("/auth/ipwhiteauth", &controllers.AuthController{}, "*:IpWhiteAuth"),
			beego.NSAutoRouter(&controllers.TerminalController{}),
			beego.NSRouter("/terminal/getcmds", &controllers.TerminalController{}, "get:GetCmds"),
			beego.NSRouter("/terminal/savecmds", &controllers.TerminalController{}, "post:SaveCmds"),
		)
		beego.AddNamespace(ns)
	} else {
		beego.Router("/", &controllers.IndexController{}, "*:Index")
		beego.AutoRouter(&controllers.IndexController{})
		beego.AutoRouter(&controllers.LoginController{})
		beego.Router("/login/github", &controllers.LoginController{}, "get:GithubLogin")
		beego.Router("/login/github/callback", &controllers.LoginController{}, "get:GithubCallback")
		beego.AutoRouter(&controllers.ClientController{})
		beego.AutoRouter(&controllers.AuthController{})
		beego.Router("/auth/ipwhiteauth", &controllers.AuthController{}, "*:IpWhiteAuth")
		beego.AutoRouter(&controllers.TerminalController{})
		beego.Router("/terminal/getcmds", &controllers.TerminalController{}, "get:GetCmds")
		beego.Router("/terminal/savecmds", &controllers.TerminalController{}, "post:SaveCmds")
	}
}
