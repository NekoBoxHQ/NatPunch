//go:build !natpunchgui
// +build !natpunchgui

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
// 面板上没有入口（建不出这种隧道），但**属于配置 / CLI 可达**：客户端用
// -local_type=tcpTrans、或配置文件里写 mode=tcpTrans，依然能建出来，这条就是给它用的。
// 所以不要按「界面里没入口」当死代码删掉 —— 和 file / secret / p2p 三种模式同一个标准，
// 那三种也是按「配置/CLI 可达」留下的。
//
// transport_natpunchgui.go 是同名函数在 natpunchgui 构建标签下的变体（GUI 包不含这条），
// 两个文件靠 build tag 互斥 —— 改这个函数时另一个也要一起看。
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
