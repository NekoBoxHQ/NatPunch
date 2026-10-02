package controllers

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"ehang.io/nps/lib/conn"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/server"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
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
	s.Data["menu"] = "terminal"
	s.Data["client_id"] = s.GetIntNoErr("client_id")
	s.SetInfo("terminal")
	s.display("terminal/index")
}

// GetClients 返回客户端列表（终端选择用）
func (s *TerminalController) GetClients() {
	list, _ := server.GetClientList(0, 1000, "", "", "id", 0)
	type item struct {
		Id        int    `json:"id"`
		Remark    string `json:"remark"`
		IsConnect bool   `json:"is_connect"`
		LocalAddr string `json:"local_addr"`
		SshUser   string `json:"ssh_user"`
		SshPort   int    `json:"ssh_port"`
	}
	items := make([]item, 0, len(list))
	for _, c := range list {
		if !c.Status {
			continue
		}
		items = append(items, item{Id: c.Id, Remark: c.Remark, IsConnect: c.IsConnect, LocalAddr: c.LocalAddr, SshUser: c.SshUser, SshPort: c.SshPort})
	}
	s.Data["json"] = map[string]interface{}{"code": 1, "data": items}
	s.ServeJSON()
}

// Ws SSH 终端 WebSocket：
// 浏览器 <-> 服务端(SSH客户端) <-> 隧道 <-> 内网设备 sshd
// 文本帧=控制(JSON resize)，二进制帧=终端数据
func (s *TerminalController) Ws() {
	clientId := s.GetIntNoErr("client_id")
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
	if client.SshUser == "" {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH user not configured"))
		return
	}
	if client.SshPass == "" {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH password not configured"))
		return
	}
	port := client.SshPort
	if port <= 0 {
		port = 22
	}
	host := client.LocalAddr
	if host == "" {
		host = client.Addr
	}
	if host == "" {
		ws.WriteMessage(websocket.TextMessage, []byte("client has no address"))
		return
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	// 1. 经隧道建立到内网设备的 TCP
	link := conn.NewLink("tcp", addr, client.Cnf.Crypt, client.Cnf.Compress, "", false, "")
	t, err := server.Bridge.SendLinkInfo(clientId, link, nil)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("tunnel error: "+err.Error()))
		return
	}
	defer t.Close()

	// 2. 在该 TCP 上发起 SSH（自动登录）
	sshConf := &ssh.ClientConfig{
		User:            client.SshUser,
		Auth:            []ssh.AuthMethod{ssh.Password(client.SshPass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	sconn, chans, reqs, err := ssh.NewClientConn(t, addr, sshConf)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("ssh error: "+err.Error()))
		return
	}
	sc := ssh.NewClient(sconn, chans, reqs)
	defer sc.Close()

	session, err := sc.NewSession()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("ssh session error: "+err.Error()))
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm", 24, 80, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("ssh pty error: "+err.Error()))
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return
	}
	if err := session.Shell(); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("ssh shell error: "+err.Error()))
		return
	}

	// 3. 双向桥接
	// 内网 -> 浏览器（二进制帧）
	go func() {
		io.Copy(wsBinaryWriter{ws}, io.MultiReader(stdout, stderr))
		ws.Close()
	}()

	// 浏览器 -> 内网（二进制=数据，文本=控制）
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage {
			stdin.Write(data)
		} else if mt == websocket.TextMessage {
			var ctrl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				session.WindowChange(ctrl.Rows, ctrl.Cols)
			}
		}
	}
}
