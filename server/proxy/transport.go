package proxy

import (
	"net"
	"strconv"
	"syscall"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
)

// HandleTrans 是 tcpTrans 隧道模式的服务端实现。
//
// 面板上没有入口（建不出这种隧道）。唯一能走到这里的是**客户端配置文件**：
// 用 -config 加载的配置里写 mode=tcpTrans，客户端把它当 NEW_TASK 上报，
// 服务端就照这个 mode 建（bridge.go 的 NEW_TASK → OpenTask → StartTask → NewMode）。
// CLI 那条路早没了 —— -local_type 参数在清理客户端时已删除。
// 所以不要按「界面里没入口」当死代码删掉：它不是残留，是一条（虽然绕）可达的路径。
func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	if addr, err := getAddress(c.Conn); err != nil {
		return err
	} else {
		return s.DealClient(c, s.task.Client, addr, nil, common.CONN_TCP, nil, s.task.Flow, s.task.Target.LocalProxy, s.task, nil)
	}
}

const SO_ORIGINAL_DST = 80

func getAddress(conn net.Conn) (string, error) {
	sysrawconn, f := conn.(syscall.Conn)
	if !f {
		return "", nil
	}
	rawConn, err := sysrawconn.SyscallConn()
	if err != nil {
		return "", nil
	}
	var ip string
	var port uint16
	// Control 的 err 忽略：SO_ORIGINAL_DST 读取失败时返回空地址（上游历史行为）
	_ = rawConn.Control(func(fd uintptr) {
		addr, err := syscall.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_IP, SO_ORIGINAL_DST)
		if err != nil {
			return
		}
		ip = net.IP(addr.Multiaddr[4:8]).String()
		port = uint16(addr.Multiaddr[2])<<8 + uint16(addr.Multiaddr[3])
	})
	return ip + ":" + strconv.Itoa(int(port)), nil
}
