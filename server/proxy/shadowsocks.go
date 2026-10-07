package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/astaxie/beego/logs"

	"github.com/sagernet/sing-shadowsocks"
	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Shadowsocks 代理（面板第四种隧道模式）。
//
// 它跟 SOCKS5 / HTTP 代理是同一件事的第三种入口：公网服务器的这个端口收下
// Shadowsocks 流量，解密之后照常交给隧道送给客户端。所以**客户端侧一个字节都没改** ——
// 转发那一步直接复用 BaseServer.DealClient。
//
// 只支持一种加密：面板上不给"加密方式"这个选项，就是为了少一类
// 「配错了连不上、还看不出哪里错」的故障。

// SSMethod 是唯一支持的加密方式。它决定 PSK 必须是 16 字节（base64 后 24 字符）。
const SSMethod = "2022-blake3-aes-128-gcm"

// SSKeySize 是该加密方式要求的 PSK 字节数（见 SIP022 的表）。
const SSKeySize = 16

// ssUDPTimeout 是 UDP 会话的空闲回收秒数（sing 那边的参数）。
const ssUDPTimeout = 300

// ssUDPReserve 是交给 sing 那侧写数据时要预留的空间（见 NewPacketConnection）：
// 前面要给 SS 的头（Buffer.ExtendHeader 要求 start 足够大），后面要给 AEAD 的
// 16 字节 tag 和可能的填充（Buffer.Extend 要求还有剩余容量）。
const ssUDPReserve = 128

// NewShadowsocksModeServer 构造。字段赋值口径同 NewSock5ModeServer。
func NewShadowsocksModeServer(bridge NetBridge, task *file.Tunnel) *ShadowsocksModeServer {
	s := new(ShadowsocksModeServer)
	s.bridge = bridge
	s.task = task
	return s
}

// ssMetadata 只是 M.Metadata 的别名，用来躲开 SA1019。
//
// sing 的 shadowsocks.Handler 接口签名用的就是 M.Metadata，而上游在 v0.7.6 里
// 把它标成了 deprecated（注释原文就是 "wtf is this?"），却仍在自己的接口里用 ——
// 我们要实现这个接口就绕不开。收口成别名 + 这一处 nolint，免得每个引用点都被报。
type ssMetadata = M.Metadata //nolint:staticcheck // 上游接口签名就用它，绕不开

type ShadowsocksModeServer struct {
	BaseServer
	listener   net.Listener
	packetConn net.PacketConn
	service    shadowsocks.Service
}

func (s *ShadowsocksModeServer) Start() error {
	// PSK 存在 task.Password 里（面板的「密码」输入框，留空时由控制器生成）。
	// 长度/编码不对会在这里直接报错 —— 建隧道时就失败，而不是等第一个连接进来才炸。
	service, err := shadowaead_2022.NewServiceWithPassword(SSMethod, s.task.Password, ssUDPTimeout, &ssHandler{server: s}, time.Now)
	if err != nil {
		logs.Error("shadowsocks task id %d: 密钥不可用（需要 %d 字节 base64）: %v", s.task.Id, SSKeySize, err)
		return err
	}
	s.service = service

	addr := net.JoinHostPort(s.task.ServerIp, strconv.Itoa(s.task.Port))

	// TCP：把 accept 到的连接交给 sing 的 SS 服务。
	// 它会在返回前跑完整个会话（解密 → 回调我们的 NewConnection → 转发），
	// 所以必须放进 goroutine，否则一条连接就把 accept 循环卡死。
	//
	// ⚠️ 这里不能用 conn.NewTcpListenerAndProcess —— 它内部的 Accept 是**阻塞**的。
	// 只用 TCP 的 socks5 无所谓（Start 本来就跑在 goroutine 里），但这里后面还要起
	// UDP，卡在那儿 UDP 监听永远建不起来：TCP 通、UDP 静默不通，最难查的那种。
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = l
	go conn.Accept(l, func(c net.Conn) {
		if err := s.CheckFlowAndConnNum(s.task.Client); err != nil {
			logs.Warn("client id %d, task id %d, error %s, when shadowsocks connection", s.task.Client.Id, s.task.Id, err.Error())
			c.Close()
			return
		}
		s.task.Client.AddConn()
		go func() {
			err := s.service.NewConnection(context.Background(), c, ssMetadata{Source: M.SocksaddrFromNet(c.RemoteAddr())})
			if err != nil {
				// 握手失败最常见的原因是密钥不对 / 对面在扫端口，降级成 Warn
				logs.Warn("shadowsocks task id %d: %v", s.task.Id, err)
				c.Close()
			}
		}()
	})

	// UDP：跟 TCP 同一个端口号，但是另一个 socket。
	// 失败时把已经起来的 TCP 监听关掉，别留个半死不活的隧道。
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		_ = s.listener.Close()
		s.listener = nil
		return err
	}
	s.packetConn = pc
	go s.serveUDP(pc)

	return nil
}

func (s *ShadowsocksModeServer) Close() error {
	// 建隧道后立即删除时 Start 尚未执行、两个 socket 都是 nil（同 socks5 的 G4）
	if s.packetConn != nil {
		_ = s.packetConn.Close()
	}
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

// serveUDP 把 UDP socket 上的数据报喂给 sing 的 SS 服务。
//
// 每个数据报的"回程写回"由 ssReplyConn 负责：sing 解出一条内层数据报后，
// 会回调我们 ssHandler.NewPacketConnection 里给的那个 PacketConn 写回去。
func (s *ShadowsocksModeServer) serveUDP(pc net.PacketConn) {
	for {
		b := buf.NewPacket()
		_, addr, err := b.ReadPacketFrom(pc)
		if err != nil {
			b.Release()
			// socket 被 Close 掉时这里会一直报错，退出即可
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		// NewPacket 拿走 buffer 的所有权，成功与否都不要再 Release
		if err := s.service.NewPacket(context.Background(),
			&ssReplyConn{pc: pc, source: addr}, b, ssMetadata{Source: M.SocksaddrFromNet(addr)}); err != nil {
			logs.Warn("shadowsocks udp from %s: %v", addr, err)
		}
	}
}

// ssHandler 是 sing-shadowsocks 的回调入口。三个方法里前两个是真正的数据面，
// 第三个只是把解析失败的连接记一笔（密钥不对、扫端口之类）。
type ssHandler struct {
	server *ShadowsocksModeServer
}

var _ shadowsocks.Handler = (*ssHandler)(nil)

// NewError 是 sing 那边的错误出口（握手失败、解密失败、密钥不对、被扫端口……）。
// 面板端口是公开在公网上的，这类噪音一定会有，所以只记 Warn，不要 Error 刷屏。
func (h *ssHandler) NewError(ctx context.Context, err error) {
	logs.Warn("shadowsocks task id %d: %v", h.server.task.Id, err)
}

// NewConnection：conn 已经是**解密后**的流，metadata.Destination 就是客户端请求的目标。
func (h *ssHandler) NewConnection(ctx context.Context, raw net.Conn, metadata ssMetadata) error {
	s := h.server
	target := metadata.Destination.String()
	if target == "" || metadata.Destination.Port == 0 {
		raw.Close()
		return nil
	}
	logs.Trace("New shadowsocks connection, client %d, target %s", s.task.Client.Id, target)
	guard := newSSConnGuard(raw)
	defer guard.Close()
	// 跟 socks5.doConnect 最后那一步完全一样：目标地址交给隧道，客户端去拨。
	s.DealClient(conn.NewConn(guard), s.task.Client, target, nil, common.CONN_TCP, nil,
		s.task.Flow, s.task.Target.LocalProxy, s.task, nil)
	return nil
}

// ssConnGuard 绕开 sing-shadowsocks v0.2.9 的一处数据竞争。
//
// serverConn.Write 第一次被调用时会在自己的锁里给 c.writer 赋值，而
// serverConn.Close **不加锁**地读它。我们这边两个方向各跑一个拷贝 goroutine
// （DealClient → CopyWaitGroup），一边出错就 Close —— 正好可能撞上另一边在做首次
// Write，`go test -race` 必炸。（实际危害不大：Close 无论如何都会关掉底层连接；
// 但 CI 的 -race 门禁会红，而且这确实是竞争。）
//
// 补法：
//   - Write 与 Close 用同一把锁串起来 —— 谁也不能和"正在给 c.writer 赋值"的那次
//     Write 并行。Read 不加锁：它只碰 c.reader，构造之后再不改变。
//   - Close 先**无锁**关掉底层 TCP，好让卡在写里的那次 Write 立刻返回，
//     不会把 Close 拖死（不然就是典型的关闭死锁）。
type ssConnGuard struct {
	net.Conn          // sing 解密后的流（即 serverConn）
	upstream net.Conn // 它下面的裸 TCP，只用来"先关一下"解开阻塞
	mu       sync.Mutex
}

func newSSConnGuard(c net.Conn) *ssConnGuard {
	g := &ssConnGuard{Conn: c}
	// serverConn.Upstream() 返回它包着的那条连接；没有就算了（只是少了"先解开阻塞"这一步）
	if up, ok := c.(interface{ Upstream() any }); ok {
		if raw, ok := up.Upstream().(net.Conn); ok {
			g.upstream = raw
		}
	}
	return g
}

func (c *ssConnGuard) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *ssConnGuard) Close() error {
	if c.upstream != nil {
		_ = c.upstream.Close()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Close()
}

// NewPacketConnection：一条 UDP 会话（同一个源地址的一串数据报）。
//
// 隧道侧照抄 socks5.handleUDP：建一条 "udp5" 链路两边对泵。
//
// ⚠️ udp5 链路两边的报文格式**不对称**（客户端 client/client.go 的 handleUdp 是这么写的，
// 改不了）：
//
//	服务端 → 客户端：[SOCKS5 UDP 头(ATYP+ADDR+PORT)][载荷]，**没有长度前缀**
//	客户端 → 服务端：4 字节小端长度 + [SOCKS5 UDP 头][载荷]
//
// 所以回程必须"先读长度再读那么多字节"，不能直接 ReadUDPDatagram ——
// 标准 UDP 报文的 Rsv 是 0，那条分支会在 io.ReadAll 上一直等缓冲区读满。
func (h *ssHandler) NewPacketConnection(ctx context.Context, pc N.PacketConn, metadata ssMetadata) error {
	s := h.server
	defer pc.Close()

	link := conn.NewLink("udp5", "", s.task.Client.Cnf.Crypt, s.task.Client.Cnf.Compress, metadata.Source.String(), false, "")
	target, err := s.bridge.SendLinkInfo(s.task.Client.Id, link, s.task)
	if err != nil {
		logs.Warn("shadowsocks udp: get connection from client id %d error %s", s.task.Client.Id, err.Error())
		return err
	}
	defer target.Close()

	// 上行：SS 数据报 → 隧道
	go func() {
		defer target.Close()
		for {
			b := buf.NewPacket()
			dest, err := pc.ReadPacket(b)
			if err != nil {
				b.Release()
				return
			}
			d := common.NewUDPDatagram(common.NewUDPHeader(0, 0, socksAddrFromSocksaddr(dest)), b.Bytes())
			b.Release()
			if err := d.Write(target); err != nil {
				logs.Warn("shadowsocks udp: write to client error %s", err.Error())
				return
			}
		}
	}()

	// 下行：隧道 → SS 数据报（格式见函数头的说明：带 4 字节小端长度前缀）。
	// 回程报文头里带的是**实际回包的源地址**，原样交给 SS 层当目标地址写回去。
	for {
		var l int32
		if err := binary.Read(target, binary.LittleEndian, &l); err != nil {
			return nil
		}
		if l <= 0 || l >= common.PoolSizeUdp {
			logs.Warn("shadowsocks udp: bad datagram length %d", l)
			return nil
		}
		raw := make([]byte, l)
		if _, err := io.ReadFull(target, raw); err != nil {
			return nil
		}
		d, err := common.ReadUDPDatagram(bytes.NewReader(raw))
		if err != nil {
			logs.Warn("shadowsocks udp: unpack data error %s", err.Error())
			continue // 单包坏掉就丢，别把整条会话拆了
		}
		if d.Header == nil || d.Header.Addr == nil {
			continue
		}
		// ⚠️ 交出去的 buffer 必须**两头预留**，否则 SS 层会 panic("buffer overflow")：
		//   前面：WritePacket 往载荷前插它自己的头（ExtendHeader，要求 start >= 头长）
		//   后面：AEAD 的 16 字节 tag（Extend，要求还有剩余容量）
		// 用 NewSize(精确长度) 两头都不够。头部长度取决于目标地址——这里是回包的
		// 源地址，客户端固定按 IPv4 编码（common.ToSocksAddr 只产出 ipV4），
		// 128 字节两头都绰绰有余。
		b := buf.NewSize(len(d.Data) + 2*ssUDPReserve)
		b.Extend(ssUDPReserve)
		b.Advance(ssUDPReserve)
		if _, err := b.Write(d.Data); err != nil {
			b.Release()
			return err
		}
		// 所有权交给 SS 层，它负责 Release
		if err := pc.WritePacket(b, socksaddrFromCommonAddr(d.Header.Addr)); err != nil {
			return nil
		}
	}
}

// ssReplyConn 是"把回程原始数据报写回 SS 客户端"的那一端（sing 管它叫 stub packet conn）。
// 它只需要能写：读是 SS 层在做。
type ssReplyConn struct {
	pc     net.PacketConn
	source net.Addr
}

var _ N.PacketConn = (*ssReplyConn)(nil)

func (c *ssReplyConn) ReadPacket(*buf.Buffer) (M.Socksaddr, error) {
	return M.Socksaddr{}, os.ErrInvalid
}

func (c *ssReplyConn) WritePacket(b *buf.Buffer, _ M.Socksaddr) error {
	defer b.Release()
	_, err := c.pc.WriteTo(b.Bytes(), c.source)
	return err
}

func (c *ssReplyConn) Close() error                       { return nil }
func (c *ssReplyConn) LocalAddr() net.Addr                { return c.pc.LocalAddr() }
func (c *ssReplyConn) SetDeadline(t time.Time) error      { return nil }
func (c *ssReplyConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *ssReplyConn) SetWriteDeadline(t time.Time) error { return nil }

// socksAddrFromSocksaddr 把 sing 的地址类型转成隧道报文用的 common.Addr。
// Type 的取值（1/3/4）跟 socks5.go 里那几个常量是同一套 SOCKS5 约定，
// 所以这里直接用包内的 ipV4 / ipV6 / domainName。
func socksAddrFromSocksaddr(a M.Socksaddr) *common.Addr {
	if a.Addr.IsValid() {
		if a.Addr.Is4() {
			return &common.Addr{Type: ipV4, Host: a.Addr.String(), Port: a.Port}
		}
		return &common.Addr{Type: ipV6, Host: a.Addr.String(), Port: a.Port}
	}
	return &common.Addr{Type: domainName, Host: a.Fqdn, Port: a.Port}
}

func socksaddrFromCommonAddr(a *common.Addr) M.Socksaddr {
	if ip, err := netip.ParseAddr(a.Host); err == nil {
		return M.Socksaddr{Addr: ip, Port: a.Port}
	}
	return M.Socksaddr{Fqdn: a.Host, Port: a.Port}
}
