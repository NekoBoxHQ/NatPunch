package controllers

import (
	"encoding/json"
	"io"
	"net/http"

	"ehang.io/nps/lib/conn"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/server"

	"github.com/gorilla/websocket"
)

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

// GetClients 返回客户端列表（终端选择用）
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
		items = append(items, item{Id: c.Id, Remark: c.Remark, IsConnect: c.IsConnect, LocalAddr: c.LocalAddr})
	}
	s.Data["json"] = map[string]interface{}{"code": 1, "data": items}
	s.ServeJSON()
}

// Ws 终端 WebSocket：
// 浏览器 <-> 服务端 <-> 隧道(shell link) <-> 客户端本地 shell（PTY）
// 文本帧=控制(JSON resize)，二进制帧=终端数据
func (s *TerminalController) Ws() {
	clientId := s.GetIntNoErr("client_id")
	cols := s.GetIntNoErr("cols")
	rows := s.GetIntNoErr("rows")
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ws, err := up.Upgrade(s.Ctx.ResponseWriter, s.Ctx.Request, nil)
	if err != nil {
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
	link := conn.NewLink("shell", "", client.Cnf.Crypt, client.Cnf.Compress, "", false, "")
	link.Cols = cols
	link.Rows = rows
	t, err := server.Bridge.SendLinkInfo(clientId, link, nil)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("tunnel error: "+err.Error()))
		return
	}
	defer t.Close()

	// 双向桥接：内网 -> 浏览器（二进制帧）
	go func() {
		io.Copy(wsBinaryWriter{ws}, t)
		ws.Close()
	}()

	// 浏览器 -> 内网（二进制=数据，文本=控制）
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage {
			t.Write(data)
		}
	}
}
