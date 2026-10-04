package controllers

import (
	"github.com/astaxie/beego/logs"
	"html"
	"time"

	"ehang.io/nps/lib/file"

	"github.com/astaxie/beego"
)

type AuthController struct {
	beego.Controller
}

func (s *AuthController) GetTime() {
	m := make(map[string]interface{})
	m["time"] = time.Now().Unix()
	s.Data["json"] = m
	s.ServeJSON()
}

func (s *AuthController) IpWhiteAuth() {
	s.Ctx.ResponseWriter.Header().Set("Access-Control-Allow-Origin", "*")
	s.Ctx.ResponseWriter.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	s.Ctx.ResponseWriter.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	vkey := s.getEscapeString("vkey")
	ip := s.getEscapeString("ip")
	password := s.getEscapeString("pass")

	if vkey == "" || password == "" {
		s.Data["json"] = map[string]interface{}{"success": false, "message": "参数错误"}
		s.ServeJSON()
		return
	}

	// 如果未提供 ip，则使用请求中的客户端 IP（支持代理头）
	if ip == "" {
		ip = s.Ctx.Input.IP()
		ip = html.EscapeString(ip)
	}

	c, err := file.GetDb().GetClientByVkey(vkey)
	if err != nil {
		// 统一错误消息，避免"密钥错误/密码错误"区分构成凭据 oracle（阶段三 #18）
		s.Data["json"] = map[string]interface{}{"success": false, "message": "认证失败"}
		s.ServeJSON()
		// 不打印密码（阶段三 #18 日志脱敏）
		logs.Error("客户端IP白名单认证失败,密钥不存在:vkey [%s] ip [%s]", vkey, ip)
		return
	}

	if c.IpWhitePass != password {
		s.Data["json"] = map[string]interface{}{"success": false, "message": "认证失败"}
		s.ServeJSON()
		logs.Error("客户端IP白名单认证失败,授权密码错误:vkey [%s] ip [%s]", vkey, ip)
		return
	}

	ipExists := false
	for _, existingIp := range c.IpWhiteList {
		if existingIp == ip {
			ipExists = true
			break
		}
	}

	if !ipExists {
		c.IpWhiteList = append(c.IpWhiteList, ip)
		file.GetDb().UpdateClient(c)
	}

	s.Data["json"] = map[string]interface{}{"success": true, "message": "授权成功"}
	s.ServeJSON()

	logs.Info("客户端IP白名单认证授权成功:vkey [%s] ip [%s]", vkey, ip)

}

func (s *AuthController) getEscapeString(key string) string {
	return html.EscapeString(s.GetString(key))
}
