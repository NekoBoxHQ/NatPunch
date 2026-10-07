package controllers

import (
	"errors"
	"strings"

	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/server"
	"github.com/NekoBoxHQ/NatPunch/server/tool"

	"github.com/astaxie/beego"
)

type IndexController struct {
	BaseController
}

// caseKeyForTunnelMode 把隧道模式映射到「使用场景」那句的词条 id。
//
// **这一段放在服务端算**，不让页面按模式去 show/hide：那条路要求"模式值一定能匹配到
// 某个 span"，而列表页传过来的 type 可能是**空串**、模板渲染进 <script> 的值还会被
// html/template 把 '+' 转义成 &#43;（脚本里不还原）—— 两种都在真机上翻过车，
// 表现都是「使用场景」那一行空白。
//
// 模式只有三种：tcp+udp（双端隧道，含列表页传空串的情况）/ httpProxy / socks5。
// TCP隧道 / UDP隧道 已弃用，没有对着它们的词条了。
func caseKeyForTunnelMode(mode string) string {
	switch mode {
	case "httpProxy":
		return "info-casehttpproxy"
	case "socks5":
		return "info-casesocks5"
	case "shadowsocks":
		return "info-caseshadowsocks"
	default:
		return "info-casetcpudp"
	}
}

// normalizeTunnelMode 归一化隧道模式。
//
// 查询串里的 '+' 会被解码成**空格**：`?type=tcp+udp` 送到这里已经是 "tcp udp"。
// 这个值既用来筛列表 / 决定页面显示哪一种模式，也会被当成隧道的 Mode 存下来 ——
// 带个空格进去，服务端按 mode 起监听时认不出来，那条隧道等于没建起来。
// 以前这个坑被页面上的 `<select>` 掩盖了（值对不上选项时会回退到第一项），
// 换成隐藏域之后就直接暴露了。所以在入口统一收口。
func normalizeTunnelMode(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	return strings.ReplaceAll(v, " ", "+")
}

func (s *IndexController) Index() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["data"] = server.GetDashboardData()
	s.SetInfo("dashboard")
	s.display("index/index")
}

// Tcpudp 隧道管理：TCP / UDP 隧道统一列表
func (s *IndexController) Tcpudp() {
	s.SetInfo("tcpudp")
	s.SetType("tcp+udp")
	s.display("index/list")
}

func (s *IndexController) Socks5() {
	s.SetInfo("socks5")
	s.SetType("socks5")
	s.display("index/list")
}

func (s *IndexController) Shadowsocks() {
	s.SetInfo("shadowsocks")
	s.SetType("shadowsocks")
	s.display("index/list")
}

func (s *IndexController) Http() {
	s.SetInfo("http proxy")
	s.SetType("httpProxy")
	s.display("index/list")
}

// All 列出指定客户端的全部隧道（客户端列表页的「隧道」按钮进入）
func (s *IndexController) All() {
	s.Data["menu"] = "client"
	clientId := s.getEscapeString("client_id")
	s.Data["client_id"] = clientId
	s.SetInfo("client id:" + clientId)
	s.display("index/list")
}

func (s *IndexController) GetTunnel() {
	start, length := s.GetAjaxParams()
	taskType := normalizeTunnelMode(s.getEscapeString("type"))
	clientId := s.GetIntNoErr("client_id")
	// 非管理员仅可查看自己的隧道（F2-9 IDOR）
	if admin, ok := s.GetSession("isAdmin").(bool); !ok || !admin {
		cid, ok := s.GetSession("clientId").(int)
		if !ok {
			// 拿不到归属客户端时不能退化成 0：0 在 GetTunnel 里表示"不限客户端"，
			// 那等于把别人的隧道全给出去。宁可不显示。
			s.deny()
			return
		}
		clientId = cid
	}
	list, cnt := server.GetTunnel(start, length, taskType, clientId, s.getEscapeString("search"), s.getEscapeString("sort"), s.getEscapeString("order"))
	s.AjaxTable(list, cnt, cnt, nil)
}

// normAccessPath 规范化访问路径后缀：留空不启用；非空自动补 "/" 前缀
func normAccessPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func (s *IndexController) Add() {
	if s.Ctx.Request.Method == "GET" {
		addMode := normalizeTunnelMode(s.getEscapeString("type"))
		s.Data["type"] = addMode
		s.Data["case_key"] = caseKeyForTunnelMode(addMode)
		s.Data["client_id"] = s.getEscapeString("client_id")
		s.SetInfo("add tunnel")
		s.display()
	} else {
		mode := normalizeTunnelMode(s.getEscapeString("type"))
		username := s.getEscapeString("username")
		password := s.getEscapeString("password")

		// 非管理员仅可为自己的客户端创建隧道（F2-9 IDOR）
		clientId := s.GetIntNoErr("client_id")
		if admin, ok := s.GetSession("isAdmin").(bool); !ok || !admin {
			if cid, ok := s.GetSession("clientId").(int); ok {
				clientId = cid
			} else {
				clientId = 0
			}
		}

		// 创建单条隧道；type=tcp+udp 时按 tcp / udp 各建一条（同端口双协议监听）
		createOne := func(m string, remark string) (int, error) {
			id := int(file.GetDb().JsonDb.GetTaskId())
			t := &file.Tunnel{
				Port:         s.GetIntNoErr("port"),
				ServerIp:     s.getEscapeString("server_ip"),
				Mode:         m,
				Target:       &file.Target{TargetStr: s.getEscapeString("target"), LocalProxy: s.GetBoolNoErr("local_proxy")},
				Id:           id,
				Status:       true,
				Remark:       remark,
				Password:     s.getEscapeString("password"),
				LocalPath:    s.getEscapeString("local_path"),
				StripPre:     s.getEscapeString("strip_pre"),
				ProtoVersion: s.getEscapeString("proto_version"),
				AccessPath:   normAccessPath(s.getEscapeString("access_path")),
				Https:        s.GetBoolNoErr("https"),
				Flow:         &file.Flow{},
			}
			if t.Port <= 0 {
				t.Port = tool.GenerateServerPort(m)
			}
			if !tool.TestServerPort(t.Port, m) {
				return 0, errors.New("The port cannot be opened because it may has been occupied or is no longer allowed.")
			}
			var err error
			if t.Client, err = file.GetDb().GetClient(clientId); err != nil {
				return 0, err
			}
			if t.Client.MaxTunnelNum != 0 && t.Client.GetTunnelNum() >= t.Client.MaxTunnelNum {
				return 0, errors.New("The number of tunnels exceeds the limit")
			}
			// HTTP / SOCKS5 代理：隧道级账号密码（MultiAccount）
			if (m == "httpProxy" || m == "socks5") && username != "" && password != "" {
				t.MultiAccount = &file.MultiAccount{AccountMap: map[string]string{username: password}}
			}
			// Shadowsocks：密码框里装的就是 PSK（BaseServer 从 t.Password 取它）。
			// 这里**一定要归一化**：填的可能是随手输的口令、也可能是从 ss:// 链接里
			// 连着 %2B/%3D 一起粘过来的。原样存下去的话，Start() 才会在
			// "decode psk: illegal base64" 上失败 —— 隧道建出来了但起不来，
			// 面板上只看到一条 RunStatus=false 的记录，看不出是密钥的事。
			if m == "shadowsocks" {
				t.Password = crypt.NormalizeShadowsocksPSK(t.Password)
			}
			if err := file.GetDb().NewTask(t); err != nil {
				return 0, err
			}
			if err := server.AddTask(t); err != nil {
				file.GetDb().DelTask(id)
				return 0, err
			}
			return id, nil
		}

		// tcp+udp 为单条隧道（server 层同时监听 TCP/UDP）
		var firstId int
		var err error
		if firstId, err = createOne(mode, s.getEscapeString("remark")); err != nil {
			s.AjaxErr(err.Error())
			return
		}
		s.AjaxOkWithId("add success", firstId)
	}
}

func (s *IndexController) Edit() {
	id := s.GetIntNoErr("id")
	if s.Ctx.Request.Method == "GET" {
		if t, err := file.GetDb().GetTask(id); err != nil {
			// error() 只设 TplName，不会中断请求：这里不 return 的话，下面那句
			// display() 会把 TplName 覆盖成 index/edit.html，拿一份空数据把编辑页
			// 渲染出来（看着像"隧道字段全空了"）。返回才能真的出错误页。
			s.error()
			return
		} else {
			s.Data["t"] = t
			s.Data["case_key"] = caseKeyForTunnelMode(t.Mode)
		}
		s.SetInfo("edit tunnel")
		s.display()
	} else {
		if t, err := file.GetDb().GetTask(id); err != nil {
			// 这条分支走 AJAX，回 HTML 错误页没用；原来那句 s.error() 也会被紧随其后的
			// AjaxOk 覆盖掉 —— 结果是 id 不存在时反而回 "modified success"。
			s.AjaxErr("task ID not found")
			return
		} else {
			// 非管理员编辑时 client 强制为本人的（F2-9 IDOR）
			editClientId := s.GetIntNoErr("client_id")
			if admin, ok := s.GetSession("isAdmin").(bool); !ok || !admin {
				if cid, ok := s.GetSession("clientId").(int); ok {
					editClientId = cid
				} else {
					editClientId = 0
				}
			}
			if client, err := file.GetDb().GetClient(editClientId); err != nil {
				s.AjaxErr("modified error,the client is not exist")
				return
			} else {
				t.Client = client
			}
			if s.GetIntNoErr("port") != t.Port {
				t.Port = s.GetIntNoErr("port")

				if t.Port <= 0 {
					t.Port = tool.GenerateServerPort(t.Mode)
				}

				if !tool.TestServerPort(t.Port, t.Mode) {
					s.AjaxErr("The port cannot be opened because it may has been occupied or is no longer allowed.")
					return
				}
			}
			t.ServerIp = s.getEscapeString("server_ip")
			t.Mode = normalizeTunnelMode(s.getEscapeString("type"))
			// 注意 t 是库里那个对象的指针，赋值就是改原值 —— 新的 PSK 想"留空保留"
			// 的话必须先把旧的存下来。
			oldPassword := t.Password
			t.Password = s.getEscapeString("password")
			// HTTP / SOCKS5 代理账号密码
			u := s.getEscapeString("username")
			p := s.getEscapeString("password")
			if (t.Mode == "httpProxy" || t.Mode == "socks5") && u != "" && p != "" {
				t.MultiAccount = &file.MultiAccount{AccountMap: map[string]string{u: p}}
			} else {
				t.MultiAccount = nil
			}
			// Shadowsocks：密码框里是 PSK。
			//   留空     → 不动原密钥（编辑页不回填它，列表里的 ss:// 链接随时能取；
			//              当成"清空"的话，用户改个备注就会把密钥抹掉、客户端全失联）
			//   填了东西 → 一律归一化成能用的 PSK（口令也行，见 NormalizeShadowsocksPSK）
			if t.Mode == "shadowsocks" {
				if strings.TrimSpace(t.Password) == "" {
					t.Password = oldPassword
				} else {
					t.Password = crypt.NormalizeShadowsocksPSK(t.Password)
				}
			}
			t.Id = id
			t.LocalPath = s.getEscapeString("local_path")
			t.ProtoVersion = s.getEscapeString("proto_version")
			t.StripPre = s.getEscapeString("strip_pre")
			t.AccessPath = normAccessPath(s.getEscapeString("access_path"))
			t.Https = s.GetBoolNoErr("https")
			t.Remark = s.getEscapeString("remark")
			t.Target.LocalProxy = s.GetBoolNoErr("local_proxy")
			if s.GetString("flow_inlet") != "" {
				t.Flow.InletFlow = int64(s.GetIntNoErr("flow_inlet"))
			}
			if s.GetString("flow_export") != "" {
				t.Flow.ExportFlow = int64(s.GetIntNoErr("flow_export"))
			}
			file.GetDb().UpdateTask(t)
			server.StopServer(t.Id)
			server.StartTask(t.Id)
		}
		s.AjaxOk("modified success")
	}
}

func (s *IndexController) Stop() {
	id := s.GetIntNoErr("id")
	if err := server.StopServer(id); err != nil {
		s.AjaxErr("stop error")
	}
	s.AjaxOk("stop success")
}

func (s *IndexController) Del() {
	id := s.GetIntNoErr("id")
	if err := server.DelTask(id); err != nil {
		s.AjaxErr("delete error")
	}
	s.AjaxOk("delete success")
}

func (s *IndexController) Start() {
	id := s.GetIntNoErr("id")
	if err := server.StartTask(id); err != nil {
		s.AjaxErr("start error")
	}
	s.AjaxOk("start success")
}
