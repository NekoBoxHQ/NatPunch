//go:build windows
// +build windows

package proxy

import (
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
)

func HandleTrans(c *conn.Conn, s *TunnelModeServer) error {
	return nil
}
