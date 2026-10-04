package controllers

import (
	"strconv"
	"strings"
	"time"

	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/crypt"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/lib/rate"
	"ehang.io/nps/server"
	"github.com/astaxie/beego"
)

// parseRateLimit 解析带宽限制输入（单位 Mbps，100M 宽带填 100）。
// 留空 = 0（不限速）；非法输入（如 "100M"）返回错误，禁止静默按 0 处理。
func (s *ClientController) parseRateLimit() (int, string) {
	raw := s.GetString("rate_limit")
	if raw == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, "带宽限制必须是数字（Mbps，如 100M 宽带填 100）"
	}
	return n, ""
}

// parseFlowLimit 解析流量限制输入（单位 MB，1024 进制，1GB 填 1024）。
// 留空 = 0（不限）；非法输入（如 "100G"）返回错误，禁止静默按 0 处理。
func (s *ClientController) parseFlowLimit() (int64, string) {
	raw := s.GetString("flow_limit")
	if raw == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, "流量限制必须是数字（MB，如 1GB 填 1024）"
	}
	return int64(n), ""
}

type ClientController struct {
	BaseController
}

func (s *ClientController) List() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		s.SetInfo("client")
		s.display("client/list")
		return
	}
	start, length := s.GetAjaxParams()
	clientIdSession := s.GetSession("clientId")
	var clientId int
	if clientIdSession == nil {
		clientId = 0
	} else {
		clientId = clientIdSession.(int)
	}
	list, cnt := server.GetClientList(start, length, s.getEscapeString("search"), s.getEscapeString("sort"), s.getEscapeString("order"), clientId)
	cmd := make(map[string]interface{})
	ip := s.Ctx.Request.Host
	cmd["ip"] = common.GetIpByAddr(ip)
	cmd["bridgeType"] = beego.AppConfig.String("bridge_type")
	cmd["bridgePort"] = server.Bridge.TunnelPort
	s.AjaxTable(list, cnt, cnt, cmd)
}

// 添加客户端
func (s *ClientController) Add() {
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		s.SetInfo("add client")
		s.display()
	} else {
		id := int(file.GetDb().JsonDb.GetClientId())
		rl, rlErr := s.parseRateLimit()
		if rlErr != "" {
			s.AjaxErr(rlErr)
			return
		}
		fl, flErr := s.parseFlowLimit()
		if flErr != "" {
			s.AjaxErr(flErr)
			return
		}
		// 客户端 web 密码 bcrypt 落库（F2-3）；空值保持空（不启用用户登录）
		webPasswordHash := ""
		if wp := s.getEscapeString("web_password"); wp != "" {
			var err error
			webPasswordHash, err = common.HashPassword(wp)
			if err != nil {
				s.AjaxErr("密码哈希失败: " + err.Error())
				return
			}
		}
		t := &file.Client{
			VerifyKey: s.getEscapeString("vkey"),
			Id:        id,
			Status:    true,
			Remark:    s.getEscapeString("remark"),
			Cnf: &file.Config{
				U:        s.getEscapeString("u"),
				P:        s.getEscapeString("p"),
				Compress: common.GetBoolByStr(s.getEscapeString("compress")),
				Crypt:    s.GetBoolNoErr("crypt"),
			},
			ConfigConnAllow: s.GetBoolNoErr("config_conn_allow"),
			RateLimit:       rl,
			MaxConn:         s.GetIntNoErr("max_conn"),
			WebUserName:     s.getEscapeString("web_username"),
			WebPassword:     webPasswordHash,
			MaxTunnelNum:    s.GetIntNoErr("max_tunnel"),
			Flow: &file.Flow{
				ExportFlow: 0,
				InletFlow:  0,
				FlowLimit:  fl,
			},
			BlackIpList: RemoveRepeatedElement(strings.Split(s.getEscapeString("blackiplist"), "\r\n")),
			IpWhite:     s.GetBoolNoErr("ipwhite"),
			IpWhitePass: s.getEscapeString("ipwhitepass"),
			IpWhiteList: RemoveRepeatedElement(strings.Split(s.getEscapeString("ipwhitelist"), "\r\n")),
			ExpireTime:  normalizeExpireTime(s.getEscapeString("expire_time")),
			CreateTime:  time.Now().Format("2006-01-02 15:04:05"),
		}
		if err := file.GetDb().NewClient(t); err != nil {
			s.AjaxErr(err.Error())
		}
		s.AjaxOkWithId("add success", id)
	}
}
func (s *ClientController) GetClient() {
	if s.Ctx.Request.Method == "POST" {
		id := s.GetIntNoErr("id")
		data := make(map[string]interface{})
		if c, err := file.GetDb().GetClient(id); err != nil {
			data["code"] = 0
		} else {
			data["code"] = 1
			data["data"] = c
		}
		s.Data["json"] = data
		s.ServeJSON()
	}
}

// 修改客户端
func (s *ClientController) Edit() {
	id := s.GetIntNoErr("id")
	if s.Ctx.Request.Method == "GET" {
		s.Data["menu"] = "client"
		if c, err := file.GetDb().GetClient(id); err != nil {
			s.error()
		} else {
			s.Data["c"] = c
			s.Data["BlackIpList"] = strings.Join(c.BlackIpList, "\r\n")
			s.Data["IpWhiteList"] = strings.Join(c.IpWhiteList, "\r\n")
		}
		s.SetInfo("edit client")
		s.display()
	} else {
		// 客户端 web 密码 bcrypt 落库（F2-3）；空值保持原状（不清空）
		webPasswordHash := ""
		if wp := s.getEscapeString("web_password"); wp != "" {
			var err error
			webPasswordHash, err = common.HashPassword(wp)
			if err != nil {
				s.AjaxErr("密码哈希失败: " + err.Error())
				return
			}
		}
		if c, err := file.GetDb().GetClient(id); err != nil {
			s.error()
			s.AjaxErr("client ID not found")
			return
		} else {
			if s.getEscapeString("web_username") != "" {
				if s.getEscapeString("web_username") == beego.AppConfig.String("web_username") || !file.GetDb().VerifyUserName(s.getEscapeString("web_username"), c.Id) {
					s.AjaxErr("web login username duplicate, please reset")
					return
				}
			}
			if s.GetSession("isAdmin").(bool) {
				if !file.GetDb().VerifyVkey(s.getEscapeString("vkey"), c.Id) {
					s.AjaxErr("Vkey duplicate, please reset")
					return
				}
				c.VerifyKey = s.getEscapeString("vkey")
				fl, flErr := s.parseFlowLimit()
				if flErr != "" {
					s.AjaxErr(flErr)
					return
				}
				c.Flow.FlowLimit = fl
				rl, rlErr := s.parseRateLimit()
				if rlErr != "" {
					s.AjaxErr(rlErr)
					return
				}
				c.RateLimit = rl
				c.MaxConn = s.GetIntNoErr("max_conn")
				c.MaxTunnelNum = s.GetIntNoErr("max_tunnel")
			}
			if s.GetString("flow_inlet") != "" {
				c.Flow.InletFlow = int64(s.GetIntNoErr("flow_inlet"))
			}
			if s.GetString("flow_export") != "" {
				c.Flow.ExportFlow = int64(s.GetIntNoErr("flow_export"))
			}
			c.Remark = s.getEscapeString("remark")
			c.Cnf.U = s.getEscapeString("u")
			c.Cnf.P = s.getEscapeString("p")
			c.Cnf.Compress = common.GetBoolByStr(s.getEscapeString("compress"))
			c.Cnf.Crypt = s.GetBoolNoErr("crypt")
			b, err := beego.AppConfig.Bool("allow_user_change_username")
			if s.GetSession("isAdmin").(bool) || (err == nil && b) {
				c.WebUserName = s.getEscapeString("web_username")
			}
			if webPasswordHash != "" {
				c.WebPassword = webPasswordHash
			}
			c.ConfigConnAllow = s.GetBoolNoErr("config_conn_allow")
			c.IpWhite = s.GetBoolNoErr("ipwhite")
			c.IpWhitePass = s.getEscapeString("ipwhitepass")
			c.IpWhiteList = RemoveRepeatedElement(strings.Split(s.getEscapeString("ipwhitelist"), "\r\n"))
			if c.Rate != nil {
				c.Rate.Stop()
			}
			if c.RateLimit > 0 {
				// RateLimit 单位 Mbps（比特，1024 进制）：Mbps * 1024 * 1024 / 8 = 字节/秒
				c.Rate = rate.NewRate(int64(c.RateLimit * 1024 * 1024 / 8))
				c.Rate.Start()
			} else {
				c.Rate = rate.NewRate((2 << 23) * 1024)
				c.Rate.Start()
			}

			c.BlackIpList = RemoveRepeatedElement(strings.Split(s.getEscapeString("blackiplist"), "\r\n"))
			c.ExpireTime = normalizeExpireTime(s.getEscapeString("expire_time"))
			file.GetDb().JsonDb.StoreClientsToJsonFile()
		}
		s.AjaxOk("save success")
	}
}

func RemoveRepeatedElement(arr []string) (newArr []string) {
	newArr = make([]string, 0)
	for i := 0; i < len(arr); i++ {
		// 过滤空IP
		if strings.TrimSpace(arr[i]) == "" {
			continue
		}
		repeat := false
		for j := i + 1; j < len(arr); j++ {
			if arr[i] == arr[j] {
				repeat = true
				break
			}
		}
		if !repeat {
			newArr = append(newArr, arr[i])
		}
	}
	return
}

// expireTimeFormats 支持的到期时间输入格式
var expireTimeFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseExpireTime 将多种格式的到期时间字符串解析为 time.Time
// 留空或无法解析返回 ok=false
func ParseExpireTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, f := range expireTimeFormats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// normalizeExpireTime 将用户提交的到期时间统一为 "2006-01-02 15:04:05"
// 无法解析则返回空串表示不限制
func normalizeExpireTime(s string) string {
	if t, ok := ParseExpireTime(s); ok {
		return t.Format("2006-01-02 15:04:05")
	}
	return ""
}

// 更改状态
func (s *ClientController) ChangeStatus() {
	id := s.GetIntNoErr("id")
	if client, err := file.GetDb().GetClient(id); err == nil {
		client.Status = s.GetBoolNoErr("status")
		if client.Status == false {
			server.DelClientConnect(client.Id)
		}
		s.AjaxOk("modified success")
	}
	s.AjaxErr("modified fail")
}

// 删除客户端
func (s *ClientController) Del() {
	id := s.GetIntNoErr("id")
	if err := file.GetDb().DelClient(id); err != nil {
		s.AjaxErr("delete error")
	}
	server.DelTunnelAndHostByClientId(id, false)
	server.DelClientConnect(id)
	s.AjaxOk("delete success")
}

// ResetVkey 重新生成客户端 VKEY（F2-4）：存量 40bit vkey 迁移 / 泄漏处置用。
// 仅管理员、POST-only；返回新 vkey，需同步更新客户端配置。
func (s *ClientController) ResetVkey() {
	if s.Ctx.Request.Method != "POST" {
		s.AjaxErr("only POST allowed")
		return
	}
	if admin, ok := s.GetSession("isAdmin").(bool); !ok || !admin {
		s.AjaxErr("admin only")
		return
	}
	id := s.GetIntNoErr("id")
	c, err := file.GetDb().GetClient(id)
	if err != nil {
		s.AjaxErr("client not found")
		return
	}
	for i := 0; i < 3; i++ {
		nv := crypt.GetVkey()
		if file.GetDb().VerifyVkey(nv, c.Id) {
			c.VerifyKey = nv
			file.GetDb().UpdateClient(c)
			s.Data["json"] = map[string]interface{}{"status": 1, "msg": "vkey reset success", "vkey": nv}
			s.ServeJSON()
			return
		}
	}
	s.AjaxErr("generate vkey duplicate, please retry")
}
