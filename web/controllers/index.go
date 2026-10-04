package controllers

import (
	"errors"
	"strings"

	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/server"
	"github.com/NekoBoxHQ/NatPunch/server/tool"

	"github.com/astaxie/beego"
)

type IndexController struct {
	BaseController
}

func (s *IndexController) Index() {
	s.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
	s.Data["data"] = server.GetDashboardData()
	s.SetInfo("dashboard")
	s.display("index/index")
}
func (s *IndexController) Help() {
	s.SetInfo("about")
	s.display("index/help")
}

func (s *IndexController) Tcp() {
	s.SetInfo("tcp")
	s.SetType("tcp")
	s.display("index/list")
}

// Tcpudp 隧道管理：TCP / UDP 隧道统一列表
func (s *IndexController) Tcpudp() {
	s.SetInfo("tcpudp")
	s.SetType("tcp+udp")
	s.display("index/list")
}

func (s *IndexController) Udp() {
	s.SetInfo("udp")
	s.SetType("udp")
	s.display("index/list")
}

func (s *IndexController) Socks5() {
	s.SetInfo("socks5")
	s.SetType("socks5")
	s.display("index/list")
}

func (s *IndexController) Http() {
	s.SetInfo("http proxy")
	s.SetType("httpProxy")
	s.display("index/list")
}
func (s *IndexController) File() {
	s.SetInfo("file server")
	s.SetType("file")
	s.display("index/list")
}

func (s *IndexController) Secret() {
	s.SetInfo("secret")
	s.SetType("secret")
	s.display("index/list")
}
func (s *IndexController) P2p() {
	s.SetInfo("p2p")
	s.SetType("p2p")
	s.display("index/list")
}

func (s *IndexController) Host() {
	s.SetInfo("host")
	s.SetType("hostServer")
	s.display("index/list")
}

func (s *IndexController) All() {
	s.Data["menu"] = "client"
	clientId := s.getEscapeString("client_id")
	s.Data["client_id"] = clientId
	s.SetInfo("client id:" + clientId)
	s.display("index/list")
}

func (s *IndexController) GetTunnel() {
	start, length := s.GetAjaxParams()
	taskType := s.getEscapeString("type")
	clientId := s.GetIntNoErr("client_id")
	// 非管理员仅可查看自己的隧道（F2-9 IDOR）
	if admin, ok := s.GetSession("isAdmin").(bool); !ok || !admin {
		if cid, ok := s.GetSession("clientId").(int); ok {
			clientId = cid
		} else {
			clientId = 0
		}
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
		s.Data["type"] = s.getEscapeString("type")
		s.Data["client_id"] = s.getEscapeString("client_id")
		s.SetInfo("add tunnel")
		s.display()
	} else {
		mode := s.getEscapeString("type")
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

func (s *IndexController) Copy() {
	oldId := s.GetIntNoErr("id")
	if oldTask, err := file.GetDb().GetTask(oldId); err != nil {
		s.error()
	} else {
		if client, err := file.GetDb().GetClient(oldTask.Client.Id); err != nil {
			s.AjaxErr("modified error,the client is not exist")
			return
		} else {
			oldTask.Client = client
		}

		id := int(file.GetDb().JsonDb.GetTaskId())
		newTask := &file.Tunnel{
			Client:       oldTask.Client,
			Port:         tool.GenerateServerPort(oldTask.Mode),
			ServerIp:     oldTask.ServerIp,
			Mode:         oldTask.Mode,
			Target:       oldTask.Target,
			Id:           id,
			Status:       true,
			Remark:       oldTask.Remark,
			MultiAccount: oldTask.MultiAccount,
			Password:     oldTask.Password,
			LocalPath:    oldTask.LocalPath,
			StripPre:     oldTask.StripPre,
			ProtoVersion: oldTask.ProtoVersion,
			AccessPath:   oldTask.AccessPath,
			Https:        oldTask.Https,
			Flow:         &file.Flow{},
		}
		if !tool.TestServerPort(newTask.Port, newTask.Mode) {
			s.AjaxErr("The port cannot be opened because it may has been occupied or is no longer allowed.")
		}

		if newTask.Client.MaxTunnelNum != 0 && newTask.Client.GetTunnelNum() >= newTask.Client.MaxTunnelNum {
			s.AjaxErr("The number of tunnels exceeds the limit")
		}
		if err := file.GetDb().NewTask(newTask); err != nil {
			s.AjaxErr(err.Error())
		}
		if err := server.AddTask(newTask); err != nil {
			s.AjaxErr(err.Error())
		} else {
			s.AjaxOkWithId("add success", id)
		}
	}
}

func (s *IndexController) GetOneTunnel() {
	id := s.GetIntNoErr("id")
	data := make(map[string]interface{})
	if t, err := file.GetDb().GetTask(id); err != nil {
		data["code"] = 0
	} else {
		data["code"] = 1
		data["data"] = t
	}
	s.Data["json"] = data
	s.ServeJSON()
}
func (s *IndexController) Edit() {
	id := s.GetIntNoErr("id")
	if s.Ctx.Request.Method == "GET" {
		if t, err := file.GetDb().GetTask(id); err != nil {
			s.error()
		} else {
			s.Data["t"] = t
		}
		s.SetInfo("edit tunnel")
		s.display()
	} else {
		if t, err := file.GetDb().GetTask(id); err != nil {
			s.error()
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
			t.Mode = s.getEscapeString("type")
			t.Password = s.getEscapeString("password")
			// HTTP / SOCKS5 代理账号密码
			u := s.getEscapeString("username")
			p := s.getEscapeString("password")
			if (t.Mode == "httpProxy" || t.Mode == "socks5") && u != "" && p != "" {
				t.MultiAccount = &file.MultiAccount{AccountMap: map[string]string{u: p}}
			} else {
				t.MultiAccount = nil
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

// Reorder 拖拽排序：接收新顺序的 id 数组（ids=1&ids=3&ids=2）
func (s *IndexController) Reorder() {
	raw := s.Ctx.Request.Form["ids"]
	ids := make([]int, 0, len(raw))
	for _, str := range raw {
		if str == "" {
			continue
		}
		n := 0
		for _, c := range str {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > 0 {
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		s.AjaxErr("empty order")
		return
	}
	if err := server.ReorderTasks(ids); err != nil {
		s.AjaxErr("reorder error")
		return
	}
	s.AjaxOk("reorder success")
}

func (s *IndexController) Start() {
	id := s.GetIntNoErr("id")
	if err := server.StartTask(id); err != nil {
		s.AjaxErr("start error")
	}
	s.AjaxOk("start success")
}
