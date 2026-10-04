package routers

import (
	"github.com/NekoBoxHQ/NatPunch/web/controllers"
	"github.com/astaxie/beego"
)

func Init() {
	web_base_url := beego.AppConfig.String("web_base_url")
	if len(web_base_url) > 0 {
		ns := beego.NewNamespace(web_base_url,
			beego.NSRouter("/", &controllers.IndexController{}, "*:Index"),
			beego.NSAutoRouter(&controllers.IndexController{}),
			beego.NSAutoRouter(&controllers.LoginController{}),
			beego.NSAutoRouter(&controllers.ClientController{}),
			beego.NSAutoRouter(&controllers.AuthController{}),
			beego.NSRouter("/auth/ipwhiteauth", &controllers.AuthController{}, "*:IpWhiteAuth"),
			beego.NSAutoRouter(&controllers.GlobalController{}),
			beego.NSAutoRouter(&controllers.TerminalController{}),
			beego.NSRouter("/terminal/getcmds", &controllers.TerminalController{}, "get:GetCmds"),
			beego.NSRouter("/terminal/savecmds", &controllers.TerminalController{}, "post:SaveCmds"),
		)
		beego.AddNamespace(ns)
	} else {
		beego.Router("/", &controllers.IndexController{}, "*:Index")
		beego.AutoRouter(&controllers.IndexController{})
		beego.AutoRouter(&controllers.LoginController{})
		beego.AutoRouter(&controllers.ClientController{})
		beego.AutoRouter(&controllers.AuthController{})
		beego.Router("/auth/ipwhiteauth", &controllers.AuthController{}, "*:IpWhiteAuth")
		beego.AutoRouter(&controllers.GlobalController{})
		beego.AutoRouter(&controllers.TerminalController{})
		beego.Router("/terminal/getcmds", &controllers.TerminalController{}, "get:GetCmds")
		beego.Router("/terminal/savecmds", &controllers.TerminalController{}, "post:SaveCmds")
	}
}
