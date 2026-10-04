package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"ehang.io/nps/lib/conn"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/server"

	"github.com/gorilla/websocket"
)

// shellSessions：ShellID -> clientId，供 WS 收到 resize 控制帧时定位目标客户端
var shellSessions sync.Map

type TerminalController struct {
	BaseController
}

// wsBinaryWriter 把 gorilla websocket 封装为 io.Writer（写二进制帧）
type wsBinaryWriter struct{ c *websocket.Conn }

func (w wsBinaryWriter) Write(p []byte) (int, error) {
	if err := w.c.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Index 渲染终端页面（可选 client_id 参数直达指定客户端）
func (s *TerminalController) Index() {
	// 禁用页面缓存，避免浏览器缓存旧版终端页
	s.Ctx.Output.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	s.Ctx.Output.Header("Pragma", "no-cache")
	s.Ctx.Output.Header("Expires", "0")
	s.Data["menu"] = "terminal"
	s.Data["client_id"] = s.GetIntNoErr("client_id")
	s.SetInfo("terminal")
	s.display("terminal/index")
}

// GetCmds 返回服务端持久化的快捷命令
func (s *TerminalController) GetCmds() {
	s.Ctx.Output.Header("Cache-Control", "no-store")
	s.Data["json"] = file.GetDb().GetQuickCmds()
	s.ServeJSON()
}

// SaveCmds 保存快捷命令到面板服务器
func (s *TerminalController) SaveCmds() {
	var q file.QuickCmds
	b, err := io.ReadAll(s.Ctx.Request.Body)
	if err != nil || len(b) == 0 {
		s.Data["json"] = map[string]interface{}{"code": 0, "msg": "empty body"}
		s.ServeJSON()
		return
	}
	if err := json.Unmarshal(b, &q); err != nil {
		s.Data["json"] = map[string]interface{}{"code": 0, "msg": "bad request"}
		s.ServeJSON()
		return
	}
	if q.Customs == nil {
		q.Customs = []file.QuickCmdItem{}
	}
	if q.Hidden == nil {
		q.Hidden = []string{}
	}
	file.GetDb().SaveQuickCmds(&q)
	s.Data["json"] = map[string]interface{}{"code": 1}
	s.ServeJSON()
}

// GetClients 返回客户端列表（终端选择用）；非管理员仅返回自己的客户端（F2-6）
func (s *TerminalController) GetClients() {
	list, _ := server.GetClientList(0, 1000, "", "", "id", 0)
	type item struct {
		Id        int    `json:"id"`
		Remark    string `json:"remark"`
		IsConnect bool   `json:"is_connect"`
		LocalAddr string `json:"local_addr"`
	}
	items := make([]item, 0, len(list))
	for _, c := range list {
		if !c.Status {
			continue
		}
		if !s.canOperateClient(c.Id) {
			continue
		}
		items = append(items, item{Id: c.Id, Remark: c.Remark, IsConnect: c.IsConnect, LocalAddr: c.LocalAddr})
	}
	s.Data["json"] = map[string]interface{}{"code": 1, "data": items}
	s.ServeJSON()
}

// canOperateClient 终端归属校验：管理员可操作任意客户端；普通登录用户仅可操作自己的客户端（F2-6）
func (s *TerminalController) canOperateClient(cid int) bool {
	if admin, ok := s.GetSession("isAdmin").(bool); ok && admin {
		return true
	}
	mine, ok := s.GetSession("clientId").(int)
	return ok && mine == cid
}

// checkWsOrigin 跨站 WebSocket 劫持防护（F2-6）：允许无 Origin 的非浏览器客户端；
// 浏览器 Origin 必须为 localhost/回环，或与页面所在 Host 同源。
func (s *TerminalController) checkWsOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return u.Host == r.Host
}

// Ws 终端 WebSocket：
// 浏览器 <-> 服务端 <-> 隧道(shell link) <-> 客户端本地 shell（PTY）
// 文本帧=控制(JSON resize)，二进制帧=终端数据
func (s *TerminalController) Ws() {
	// 注意：BaseController.Prepare 会把会话 clientId 写入 Ctx.Input 参数（base.go SetParam），
	// 覆盖 URL 的 client_id；这里必须读原始 URL query，否则归属校验永远比对到会话自身（F2-6）
	clientId, _ := strconv.Atoi(s.Ctx.Request.URL.Query().Get("client_id"))
	cols := s.GetIntNoErr("cols")
	rows := s.GetIntNoErr("rows")
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	// 归属校验必须在 Upgrade 之前：非管理员仅可连接自己的客户端（F2-6）
	if clientId <= 0 || !s.canOperateClient(clientId) {
		s.Ctx.Output.SetStatus(403)
		return
	}
	up := websocket.Upgrader{CheckOrigin: s.checkWsOrigin}
	ws, err := up.Upgrade(s.Ctx.ResponseWriter, s.Ctx.Request, nil)
	if err != nil {
		// 不设置状态码会让 beego 尝试渲染 terminalcontroller/ws.tpl（不存在）→ 请求级 panic
		s.Ctx.Output.SetStatus(400)
		return
	}
	defer ws.Close()

	if clientId <= 0 {
		ws.WriteMessage(websocket.TextMessage, []byte("client_id required"))
		return
	}
	client, err := file.GetDb().GetClient(clientId)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("client not found"))
		return
	}

	// 经隧道请求客户端在本地启动 shell（零凭据）
	shellID := fmt.Sprintf("%d-%d", clientId, time.Now().UnixNano())
	shellSessions.Store(shellID, clientId)
	defer shellSessions.Delete(shellID)
	link := conn.NewLink("shell", "", client.Cnf.Crypt, client.Cnf.Compress, "", false, "")
	link.Cols = cols
	link.Rows = rows
	link.ShellID = shellID
	t, err := server.Bridge.SendLinkInfo(clientId, link, nil)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("tunnel error: "+err.Error()))
		return
	}
	defer t.Close()

	// 双向桥接：内网 -> 浏览器（二进制帧）
	// 统一流量语义：隧道->公网侧 = 出口流量(ExportFlow)，公网侧->隧道 = 入口流量(InletFlow)
	go func() {
		n, _ := io.Copy(wsBinaryWriter{ws}, t)
		if n > 0 {
			// 内网数据流出 = 出口流量，同时投喂网速
			client.Flow.Add(0, n)
			client.Rate.Get(n)
		}
		ws.Close()
	}()

	// 浏览器 -> 内网：二进制帧=终端数据直接转发；文本帧=控制（resize 尺寸，带外下发客户端）
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage {
			n, werr := t.Write(data)
			if n > 0 {
				// 浏览器数据进入隧道 = 入口流量，同时投喂网速
				client.Flow.Add(int64(n), 0)
				client.Rate.Get(int64(n))
			}
			if werr != nil {
				break
			}
		} else if mt == websocket.TextMessage {
			var r struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &r) == nil && r.Type == "resize" && r.Cols > 0 && r.Rows > 0 {
				if cid, ok := shellSessions.Load(shellID); ok {
					_ = server.Bridge.SendShellResize(cid.(int), shellID, r.Cols, r.Rows)
				}
			}
		}
	}
}
