//go:build natpunchgui
// +build natpunchgui

package proxy

import (
	"errors"

	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
)

// GUI 客户端构建（-tags natpunchgui）不包含 tcp.go 中的完整隧道服务，
// 此处提供占位实现，保证 client/local.go 等引用可以编译。

type process func(c *conn.Conn, s *TunnelModeServer) error

type TunnelModeServer struct{}

func NewTunnelModeServer(p process, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return new(TunnelModeServer)
}

func (s *TunnelModeServer) Start() error {
	return errors.New("tunnel mode server is not available in GUI build")
}

func (s *TunnelModeServer) Close() error {
	return nil
}
