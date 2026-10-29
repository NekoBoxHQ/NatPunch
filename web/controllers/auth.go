package controllers

import (
	"net"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/file"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

type AuthController struct {
	beego.Controller
}

// ipWhiteLimiter：/auth/ipwhiteauth 的失败限速（每 IP 每分钟 10 次）。
//
// 这个接口是**公开**的：知道 vkey 就能无限次猜 IpWhitePass。原来没有任何限速，
// 配合 Access-Control-Allow-Origin: * 还能借访客浏览器分布式爆破。
var (
	ipWhiteMu    sync.Mutex
	ipWhiteFails = map[string]*ipWhiteRec{}
)

type ipWhiteRec struct {
	n    int
	last time.Time
}

func ipWhiteBlocked(ip string) bool {
	ipWhiteMu.Lock()
	defer ipWhiteMu.Unlock()
	r, ok := ipWhiteFails[ip]
	if !ok {
		return false
	}
	if time.Since(r.last) >= time.Minute {
		delete(ipWhiteFails, ip)
		return false
	}
	return r.n >= 10
}

func ipWhiteFail(ip string) {
	ipWhiteMu.Lock()
	defer ipWhiteMu.Unlock()
	r, ok := ipWhiteFails[ip]
	if !ok || time.Since(r.last) >= time.Minute {
		ipWhiteFails[ip] = &ipWhiteRec{n: 1, last: time.Now()}
		return
	}
	r.n++
	r.last = time.Now()
	// 简单清理，避免 map 无限增长
	if len(ipWhiteFails) > 4096 {
		for k, v := range ipWhiteFails {
			if time.Since(v.last) >= time.Minute {
				delete(ipWhiteFails, k)
			}
		}
	}
}

func (s *AuthController) IpWhiteAuth() {
	// 只允许 POST：这个接口会改白名单（有副作用），GET 会让凭据进 URL/日志，
	// 也能被 <img src> 这类跨站请求触发。
	if s.Ctx.Request.Method != "POST" {
		s.Ctx.Output.SetStatus(405)
		s.Data["json"] = map[string]interface{}{"success": false, "message": "method not allowed"}
		s.ServeJSON()
		return
	}
	// 不再回 ACAO:*（没有任何正当的跨站调用场景，却给爆破开了路）。
	s.Ctx.ResponseWriter.Header().Set("Cache-Control", "no-store")

	vkey := s.GetString("vkey")
	ip := s.GetString("ip")
	password := s.GetString("pass")
	reqIP := s.Ctx.Input.IP()

	if vkey == "" || password == "" {
		s.Data["json"] = map[string]interface{}{"success": false, "message": "参数错误"}
		s.ServeJSON()
		return
	}
	if ipWhiteBlocked(reqIP) {
		s.Ctx.Output.SetStatus(429)
		s.Data["json"] = map[string]interface{}{"success": false, "message": "尝试过于频繁，请稍后再试"}
		s.ServeJSON()
		return
	}

	// 如果未提供 ip，则使用请求中的客户端 IP
	if ip == "" {
		ip = reqIP
	} else if net.ParseIP(ip) == nil {
		// 原来 ip 完全由调用方给、转义后直接进白名单 —— 白名单会被灌进任意字符串。
		s.Data["json"] = map[string]interface{}{"success": false, "message": "参数错误"}
		s.ServeJSON()
		return
	}

	c, err := file.GetDb().GetClientByVkey(vkey)
	if err != nil {
		// 统一错误消息，避免"密钥错误/密码错误"区分构成凭据 oracle（阶段三 #18）
		ipWhiteFail(reqIP)
		s.Data["json"] = map[string]interface{}{"success": false, "message": "认证失败"}
		s.ServeJSON()
		// 不打印密码（阶段三 #18 日志脱敏）
		logs.Error("客户端IP白名单认证失败,密钥不存在:vkey [%s] ip [%s]", maskKey(vkey), ip)
		return
	}
	if !c.IpWhite || !c.Status {
		s.Data["json"] = map[string]interface{}{"success": false, "message": "认证失败"}
		s.ServeJSON()
		return
	}

	// 恒定时间比较：共享口令不该有时序侧信道
	if !common.ConstantTimeStrEq(c.IpWhitePass, password) {
		ipWhiteFail(reqIP)
		s.Data["json"] = map[string]interface{}{"success": false, "message": "认证失败"}
		s.ServeJSON()
		logs.Error("客户端IP白名单认证失败,授权密码错误:vkey [%s] ip [%s]", maskKey(vkey), ip)
		return
	}

	// 去重 + 追加在 client 的写锁内完成（原来读一遍再 append 是 check-then-act，
	// 并发两个授权请求会把同一个 IP 加两次；同时它与列表序列化也会撞）。
	if c.AddIpWhite(ip) {
		file.GetDb().UpdateClient(c)
	}

	s.Data["json"] = map[string]interface{}{"success": true, "message": "授权成功"}
	s.ServeJSON()

	logs.Info("客户端IP白名单认证授权成功:vkey [%s] ip [%s]", maskKey(vkey), ip)
}
