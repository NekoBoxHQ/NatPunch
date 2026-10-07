package controllers

import (
	"strconv"
	"strings"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/rate"
	"github.com/NekoBoxHQ/NatPunch/server"
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
		// vkey 防呆：留空自动生成 32 位 hex；手填必须 ≥8 位，杜绝短 key 导致
		// 服务端/客户端不一致（升级掉线）的误操作。
		vkey := s.getEscapeString("vkey")
		if vkey == "" {
			vkey = crypt.GetVkey()
		} else if len(vkey) < 8 {
			s.AjaxErr("验证密钥至少 8 位（建议留空自动生成 32 位）")
			return
		}
		if !file.GetDb().VerifyVkey(vkey, 0) {
			s.AjaxErr("验证密钥已存在，请更换")
			return
		}
		t := &file.Client{
			VerifyKey: vkey,
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
			// DTO 脱敏：绝不下发 WebPassword（阶段三 #18）；VerifyKey 为客户端连接凭证，
			// 对非管理员隐藏（管理员编辑页需要展示/下发）。
			// 显式字段映射，避免整体拷贝内嵌 sync.RWMutex 触发 lock copy 告警。
			admin, _ := s.GetSession("isAdmin").(bool)
			vkey := c.VerifyKey
			ipWhitePass := c.IpWhitePass
			if !admin {
				// 与 VerifyKey 同样的脱敏口径：两者都是可用于接入的凭据。
				vkey = ""
				ipWhitePass = ""
			}
			// 字段名与 Client struct 序列化一致（前端 bootstrap-table 使用 Go 字段名）
			data["data"] = map[string]interface{}{
				"Id":              c.Id,
				"VerifyKey":       vkey,
				"Addr":            c.Addr,
				"LocalAddr":       c.LocalAddr,
				"Remark":          c.Remark,
				"Status":          c.Status,
				"IsConnect":       c.IsConnect,
				"RateLimit":       c.RateLimit,
				"Flow":            c.Flow,
				"WebUserName":     c.WebUserName,
				"ConfigConnAllow": c.ConfigConnAllow,
				"MaxConn":         c.MaxConn,
				"MaxTunnelNum":    c.MaxTunnelNum,
				"Version":         c.Version,
				"BlackIpList":     c.BlackIpList,
				"CreateTime":      c.CreateTime,
				"LastOnlineTime":  c.LastOnlineTime,
				"IpWhite":         c.IpWhite,
				"IpWhitePass":     ipWhitePass,
				"IpWhiteList":     c.IpWhiteList,
			}
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
			// error() 只设 TplName，不中断请求；不 return 的话下面 display() 会把它
			// 覆盖成 client/edit.html，用空数据渲染出一个"客户端字段全空"的编辑页。
			s.error()
			return
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
			// 这条分支走 AJAX：AjaxErr 已经 ServeJSON + StopRun，上面原来那句
			// s.error() 是纯空转（设了 TplName 也没人渲染），删掉。
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
				// vkey 防呆：留空 = 保持原值（绝不因编辑覆盖成空/变短导致客户端掉线）
				nv := s.getEscapeString("vkey")
				if nv == "" {
					nv = c.VerifyKey
				} else if len(nv) < 8 {
					s.AjaxErr("验证密钥至少 8 位（留空保持原值）")
					return
				}
				if !file.GetDb().VerifyVkey(nv, c.Id) {
					s.AjaxErr("Vkey duplicate, please reset")
					return
				}
				c.VerifyKey = nv
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
