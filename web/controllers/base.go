package controllers

import (
	"html"
	"os"
	"strconv"
	"strings"

	"github.com/NekoBoxHQ/NatPunch/bridge"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/NekoBoxHQ/NatPunch/server"
	"github.com/astaxie/beego"
)

type BaseController struct {
	beego.Controller
	controllerName string
	actionName     string
}

// 初始化参数
func (s *BaseController) Prepare() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	controllerName, actionName := s.GetControllerAndAction()
	s.controllerName = strings.ToLower(controllerName[0 : len(controllerName)-10])
	s.actionName = strings.ToLower(actionName)
	// 纯会话认证（auth_key query 认证链路已移除，见 F1-1）
	if s.GetSession("auth") != true {
		// Redirect → http.Redirect 会立即 WriteHeader，路由据此跳过 action；这里再显式 return，
		// 使控制流清晰，且避免落入下方 isAdmin 分支造成 403 覆盖 302。
		s.Redirect(beego.AppConfig.String("web_base_url")+"/login/index", 302)
		return
	}
	// isAdmin 三路分支（fail-closed，定稿 v3 F1-1 第 6 条）
	switch v := s.GetSession("isAdmin").(type) {
	case bool:
		if v {
			s.Data["isAdmin"] = true
		} else if cid, ok := s.GetSession("clientId").(int); ok {
			s.Ctx.Input.SetData("client_id", cid)
			s.Ctx.Input.SetParam("client_id", strconv.Itoa(cid))
			s.Data["isAdmin"] = false
			s.Data["username"] = s.GetSession("username")
			s.CheckUserAuth()
		} else {
			// 已登录但身份不完整：原实现只把模板标志置 false 就放行动作执行，
			// 等于"检查被跳过"。fail-closed 拒绝（复评🟡）。
			s.Data["isAdmin"] = false
			s.deny()
		}
	default:
		// isAdmin 缺失或类型非法：同上，拒绝而非放行（复评🟡）。
		s.Data["isAdmin"] = false
		s.deny()
	}
	s.Data["allow_user_login"], _ = beego.AppConfig.Bool("allow_user_login")
	s.Data["allow_flow_limit"], _ = beego.AppConfig.Bool("allow_flow_limit")
	s.Data["allow_rate_limit"], _ = beego.AppConfig.Bool("allow_rate_limit")
	s.Data["allow_connection_num_limit"], _ = beego.AppConfig.Bool("allow_connection_num_limit")
	s.Data["allow_multi_ip"], _ = beego.AppConfig.Bool("allow_multi_ip")
	s.Data["system_info_display"], _ = beego.AppConfig.Bool("system_info_display")
	s.Data["allow_tunnel_num_limit"], _ = beego.AppConfig.Bool("allow_tunnel_num_limit")
	s.Data["allow_local_proxy"], _ = beego.AppConfig.Bool("allow_local_proxy")
	s.Data["allow_user_change_username"], _ = beego.AppConfig.Bool("allow_user_change_username")
	showHttpProxyPort := beego.AppConfig.DefaultBool("show_http_proxy_port", true)
	httpPort := beego.AppConfig.String("http_proxy_port")
	if httpPort != "80" && showHttpProxyPort {
		s.Data["http_proxy_port"] = ":" + httpPort
	}
}

// 加载模板
func (s *BaseController) display(tpl ...string) {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	var tplname string
	if s.Data["menu"] == nil {
		s.Data["menu"] = s.actionName
	}
	if len(tpl) > 0 {
		tplname = strings.Join([]string{tpl[0], "html"}, ".")
	} else {
		tplname = s.controllerName + "/" + s.actionName + ".html"
	}
	ip := s.Ctx.Request.Host
	s.Data["ip"] = common.GetIpByAddr(ip)

	global := file.GetDb().GetGlobal()
	if global != nil && global.ServerUrl != "" && global.ServerUrl != ip {
		// 替换掉 http:// 或者 https://
		ip = global.ServerUrl
		ip = strings.ReplaceAll(ip, "http://", "")
		ip = strings.ReplaceAll(ip, "https://", "")
		s.Data["ip"] = ip
	}

	s.Data["bridgeType"] = beego.AppConfig.String("bridge_type")
	if common.IsWindows() {
		s.Data["win"] = "npc.exe"
	} else {
		s.Data["win"] = "./npc"
	}

	s.Data["p"] = strconv.Itoa(server.Bridge.TunnelPort)
	s.Data["version"] = version.VERSION

	// 只有配置了真实证书文件才走 TLS，否则走明文
	certFile := beego.AppConfig.String("web_cert_file")
	keyFile := beego.AppConfig.String("web_key_file")
	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	useTls := bridge.ServerTlsEnable && certErr == nil && keyErr == nil
	tlsPort := strconv.Itoa(beego.AppConfig.DefaultInt("tls_bridge_port", 8025))
	s.Data["tls_p"] = tlsPort
	s.Data["tls_enable"] = useTls
	s.Data["p1"] = strconv.Itoa(server.Bridge.TunnelPort) + " / " + tlsPort
	// 桥接证书指纹：客户端「一键安装命令」下发 + 页面展示，供存量客户端手工补配（F2-2）
	s.Data["bridge_fingerprint"] = crypt.GetCertFingerprint()

	s.Data["proxyPort"] = beego.AppConfig.String("hostPort")
	s.Layout = "public/layout.html"
	s.TplName = tplname
}

// 错误
func (s *BaseController) error() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Layout = "public/layout.html"
	s.TplName = "public/error.html"
}

// getEscapeString
func (s *BaseController) getEscapeString(key string) string {
	return html.EscapeString(s.GetString(key))
}

// 去掉没有err返回值的int
func (s *BaseController) GetIntNoErr(key string, def ...int) int {
	strv := s.Ctx.Input.Query(key)
	if len(strv) == 0 && len(def) > 0 {
		return def[0]
	}
	val, _ := strconv.Atoi(strv)
	return val
}

// 获取去掉错误的bool值
func (s *BaseController) GetBoolNoErr(key string, def ...bool) bool {
	strv := s.Ctx.Input.Query(key)
	if len(strv) == 0 && len(def) > 0 {
		return def[0]
	}
	val, _ := strconv.ParseBool(strv)
	return val
}

// ajax正确返回
func (s *BaseController) AjaxOk(str string) {
	s.Data["json"] = ajax(str, 1)
	s.ServeJSON()
	s.StopRun()
}

// ajax正确返回
func (s *BaseController) AjaxOkWithId(str string, id int) {
	s.Data["json"] = ajaxWithId(str, 1, id)
	s.ServeJSON()
	s.StopRun()
}

// ajax错误返回
func (s *BaseController) AjaxErr(str string) {
	s.Data["json"] = ajax(str, 0)
	s.ServeJSON()
	s.StopRun()
}

// 组装ajax
func ajax(str string, status int) map[string]interface{} {
	json := make(map[string]interface{})
	json["status"] = status
	json["msg"] = str
	return json
}

// 组装ajax
func ajaxWithId(str string, status int, id int) map[string]interface{} {
	json := make(map[string]interface{})
	json["status"] = status
	json["msg"] = str
	json["id"] = id
	return json
}

// ajax table返回
func (s *BaseController) AjaxTable(list interface{}, cnt int, recordsTotal int, kwargs map[string]interface{}) {
	json := make(map[string]interface{})
	json["rows"] = list
	json["total"] = recordsTotal
	if kwargs != nil {
		for k, v := range kwargs {
			if v != nil {
				json[k] = v
			}
		}
	}
	s.Data["json"] = json
	s.ServeJSON()
	s.StopRun()
}

// ajax table参数
func (s *BaseController) GetAjaxParams() (start, limit int) {
	return s.GetIntNoErr("offset"), s.GetIntNoErr("limit")
}

func (s *BaseController) SetInfo(name string) {
	s.Data["name"] = name
}

func (s *BaseController) SetType(name string) {
	s.Data["type"] = name
}

// CheckUserAuth 普通登录用户（非管理员）的归属校验（F2-9）：
//   - client 控制器：仅可操作自己的客户端；不可注册新客户端
//   - index 控制器：id 必须属于本人（任务或 host 任一命中即放行）
//   - global 控制器：全局设置仅管理员
//
// 管理员不受限制。
func (s *BaseController) CheckUserAuth() {
	myClientId := -1
	if cid, ok := s.GetSession("clientId").(int); ok {
		myClientId = cid
	}
	if admin, ok := s.GetSession("isAdmin").(bool); ok && admin {
		return
	}
	// beego GetControllerAndAction 返回原始类型名（如 "Global"），统一转小写再比较
	switch strings.ToLower(s.controllerName) {
	case "client":
		if s.actionName == "add" {
			s.deny()
			return
		}
		if id := s.GetIntNoErr("id"); id != 0 && id != myClientId {
			s.deny()
			return
		}
	case "index":
		// Reorder 的参数是 ids（不含 id），且 server.ReorderTasks 会重写*所有*任务的 Sort
		// （全局排序），无法按归属校验 → 非管理员一律拒绝（复评🟡）。
		if s.actionName == "reorder" {
			s.deny()
			return
		}
		if id := s.GetIntNoErr("id"); id != 0 && !s.tunnelBelongsToMe(id, myClientId) {
			s.deny()
			return
		}
	case "global":
		s.deny()
	}
}

// deny 拒绝非授权请求：beego 1.12 的 StopRun 是 panic，会跳过 WriteHeader
// （仅 SetStatus 不落 header，响应仍为 200）；CustomAbort 立即写状态码+body 再中止（F2-9 门禁）。
func (s *BaseController) deny() {
	s.CustomAbort(403, "forbidden")
}

// tunnelBelongsToMe 显式归属校验：任务或 host 任一属于本人即放行。
// 替代原 strings.Contains(actionName, "h") 字符串猜谜（"help"/"http" 会被误判，F2-9）。
func (s *BaseController) tunnelBelongsToMe(id, myClientId int) bool {
	if v, ok := file.GetDb().JsonDb.Tasks.Load(id); ok {
		if v.(*file.Tunnel).Client.Id == myClientId {
			return true
		}
	}
	if v, ok := file.GetDb().JsonDb.Hosts.Load(id); ok {
		if v.(*file.Host).Client.Id == myClientId {
			return true
		}
	}
	return false
}

// getPublicIP 返回本机第一个公网 IPv4，找不到返回空串
