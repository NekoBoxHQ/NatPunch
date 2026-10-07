package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
)

func newHandler() *ssHandler { return &ssHandler{server: &ShadowsocksModeServer{}} }

// waitPort 等到端口能连上为止（最多 5 秒）
func waitPort(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("端口 %d 一直没起来: %v", port, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// 面板上「密码留空 → 自动生成密钥」这条路不能出错：生成的密钥必须**正好**是这个
// 加密方式要的形状（16 字节、标准 base64），否则隧道能建起来、客户端却永远连不上，
// 而且从面板上看不出哪里错。
func TestGeneratedPSKIsAcceptedByShadowsocksService(t *testing.T) {
	psk := crypt.NewShadowsocksPSK()

	raw, err := base64.StdEncoding.DecodeString(psk)
	if err != nil {
		t.Fatalf("生成的 PSK 不是合法 base64: %v (%q)", err, psk)
	}
	if len(raw) != SSKeySize {
		t.Fatalf("PSK 解出来 %d 字节，%s 要求 %d 字节", len(raw), SSMethod, SSKeySize)
	}

	svc, err := shadowaead_2022.NewServiceWithPassword(SSMethod, psk, ssUDPTimeout, newHandler(), time.Now)
	if err != nil {
		t.Fatalf("SS 服务不接受面板生成的 PSK: %v", err)
	}
	if svc == nil {
		t.Fatal("SS 服务为 nil")
	}
}

// 两次生成不能撞（撞了等于所有隧道共用一个密钥）
func TestGeneratedPSKIsUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		k := crypt.NewShadowsocksPSK()
		if seen[k] {
			t.Fatalf("生成的 PSK 重复: %q", k)
		}
		seen[k] = true
	}
}

// 密钥坏了必须在**建隧道时**就报错（AddTask → Start 返回 error），
// 而不是等第一个客户端连进来才炸 —— 那样面板上只看到"启动失败"却不知道是密钥的事。
func TestBadPSKRejected(t *testing.T) {
	bad := []string{
		"",              // 空
		"not-base64!!!", // 不是 base64
		base64.StdEncoding.EncodeToString(make([]byte, 8)), // 长度不够
	}
	for _, psk := range bad {
		if _, err := shadowaead_2022.NewServiceWithPassword(SSMethod, psk, ssUDPTimeout, newHandler(), time.Now); err == nil {
			t.Fatalf("PSK %q 应该被拒绝", psk)
		}
	}
}

// 隧道里的 UDP 报文用 common.Addr 编地址，SS 这边用 sing 的 Socksaddr，
// 两者来回转换必须不丢信息（域名尤其重要：内网目标多半是域名）。
func TestSocksaddrRoundTrip(t *testing.T) {
	cases := []M.Socksaddr{
		M.ParseSocksaddr("10.1.50.101:22"),
		M.ParseSocksaddr("8.8.8.8:53"),
		M.ParseSocksaddr("[2400:3200::1]:53"),
		M.ParseSocksaddr("nas.lan:445"),
	}
	for _, want := range cases {
		got := socksaddrFromCommonAddr(socksAddrFromSocksaddr(want))
		if got.String() != want.String() {
			t.Fatalf("往返后地址变了: %s → %s", want, got)
		}
	}
}

// 假 NatPunch 客户端：站在 udp5 链路另一端，**严格照 client/client.go handleUdp
// 的写法**收发 —— 格式一错这条用例就该红。
//
//	读（服务端→客户端）：一次 Read 一个 [SOCKS5 UDP 头][载荷] 报文
//	写（客户端→服务端）：4 字节小端长度 + [SOCKS5 UDP 头][载荷]
//
// replySrc 非 nil 时，回程报文头里报的**源地址**用它替代真实回声地址 ——
// 用来伪造"回包来自 1.1.1.1:53"这种触发 SS2022 填充的形态，不必真去绑 53 端口。
func fakeNatpunchUDPClient(tunnel net.Conn, target string, replySrc *common.Addr) {
	defer tunnel.Close()
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return
	}
	local, err := net.ListenUDP("udp", nil)
	if err != nil {
		return
	}
	defer local.Close()

	// 服务端 → 客户端 → 真实目标
	go func() {
		b := make([]byte, 65535)
		for {
			n, err := tunnel.Read(b)
			if err != nil {
				return
			}
			d, err := common.ReadUDPDatagram(bytes.NewReader(b[:n]))
			if err != nil {
				continue
			}
			if _, err := local.WriteTo(d.Data, raddr); err != nil {
				return
			}
		}
	}()

	// 真实目标 → 客户端 → 服务端
	b := make([]byte, 65535)
	var out bytes.Buffer
	for {
		n, src, err := local.ReadFrom(b)
		if err != nil {
			return
		}
		out.Reset()
		addr := common.ToSocksAddr(src)
		if replySrc != nil {
			addr = replySrc
		}
		_ = common.NewUDPDatagram(common.NewUDPHeader(0, 0, addr), b[:n]).Write(&out)
		head := make([]byte, 4)
		binary.LittleEndian.PutUint32(head, uint32(out.Len()))
		if _, err := tunnel.Write(append(head, out.Bytes()...)); err != nil {
			return
		}
	}
}

// 端到端（UDP）：真的 SS 客户端 → 真的 SS 服务端 → 假 NatPunch 客户端 → 真的 UDP 回声。
//
// 这条重点盯的是 **udp5 链路的非对称报文格式**：回程如果按对称写法直接
// ReadUDPDatagram，会卡在 io.ReadAll 上永远等不到数据（Rsv=0 那条分支）。
func TestShadowsocksEndToEndUDP(t *testing.T) {
	runShadowsocksUDPRoundTrip(t, nil)
}

// 回包源地址是 **53 端口** 的那条路：SS2022 的 serverPacketWriter 对目的端口 53 的
// 报文会加随机填充（最长 MaxPaddingLength=900），回程 buffer 的头部预留要按最大算。
//
// 这条是线上事故的回归钉：v26.10.35 上线后一次 DNS 查询就把服务端打崩了 ——
// 当时只留了 128 字节，ExtendHeader(648) 直接 panic，进程退出、systemd 重启。
// 上面那条用例的回声端口是随机高端口，**碰不到填充**，所以它当初没报出来。
func TestShadowsocksEndToEndUDPPort53Padding(t *testing.T) {
	runShadowsocksUDPRoundTrip(t, &common.Addr{Type: ipV4, Host: "1.1.1.1", Port: 53})
}

func runShadowsocksUDPRoundTrip(t *testing.T, replySrc *common.Addr) {
	echoPC, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoPC.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, err := echoPC.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = echoPC.WriteTo(b[:n], a)
		}
	}()

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	confPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confPath, "conf"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"clients.json", "tasks.json", "hosts.json"} {
		if err := os.WriteFile(filepath.Join(confPath, "conf", n), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldConfPath := common.ConfPath
	common.ConfPath = confPath
	t.Cleanup(func() { common.ConfPath = oldConfPath })

	psk := crypt.NewShadowsocksPSK()
	client := file.NewClient("test", true, true)
	client.Id = 1
	task := &file.Tunnel{
		Id: 1, Port: port, ServerIp: "127.0.0.1", Mode: "shadowsocks",
		Password: psk, Client: client, Target: &file.Target{}, Flow: &file.Flow{},
	}

	serverSide, clientSide := net.Pipe()
	go fakeNatpunchUDPClient(clientSide, echoPC.LocalAddr().String(), replySrc)

	srv := NewShadowsocksModeServer(&tunnelTestBridge{target: serverSide}, task)
	if err := srv.Start(); err != nil {
		t.Fatalf("SS 服务端起不来: %v", err)
	}
	defer srv.Close()
	waitPort(t, port)

	method, err := shadowaead_2022.NewWithPassword(SSMethod, psk, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	pc := method.DialPacketConn(raw)
	_ = pc.SetDeadline(time.Now().Add(10 * time.Second))

	// SS 层的 WritePacket 会在载荷**前面**插进自己的头部（ExtendHeader），
	// 所以 buffer 必须预留出头部空间：start 得先推出去一段，否则
	// ExtendHeader 直接 panic("buffer overflow")。这是调用方的责任。
	send := buf.NewPacket()
	send.Extend(64)
	send.Advance(64)
	if _, err := send.Write([]byte("udp-hello")); err != nil {
		t.Fatal(err)
	}
	if err := pc.WritePacket(send, M.ParseSocksaddr(echoPC.LocalAddr().String())); err != nil {
		t.Fatalf("SS UDP 写入失败: %v", err)
	}

	recv := buf.NewPacket()
	defer recv.Release()
	if _, err := pc.ReadPacket(recv); err != nil {
		t.Fatalf("SS UDP 读回失败（回程不通）: %v", err)
	}
	if got := string(recv.Bytes()); got != "udp-hello" {
		t.Fatalf("UDP 回程数据不对: %q", got)
	}

	// UDP 这一半原来**完全没记账**，客户端列表自然也是 0（见 assertClientFlowCounted）
	assertClientFlowCounted(t, client)
}

// 域名要落成 SOCKS5 的 domainName(3)，不能被当成 IP 硬塞进 4 字节。
func TestDomainKeptAsDomain(t *testing.T) {
	a := socksAddrFromSocksaddr(M.ParseSocksaddr("nas.lan:445"))
	if a.Type != domainName {
		t.Fatalf("域名地址的 Type = %d，应为 %d", a.Type, domainName)
	}
	if a.Host != "nas.lan" || a.Port != 445 {
		t.Fatalf("域名/端口丢信息: %+v", a)
	}
}

// 端到端（TCP）：起一个真的 Shadowsocks 服务端，用**真的** SS 客户端连上去，
// 字节要原样穿过"隧道"。
//
// 「隧道」由 tcp_test.go 里现成的 tunnelTestBridge 代替 —— 它把转发目标直接换成
// 一个本地回声服务，所以这条用例覆盖的正是我写的那部分：accept → sing 解密 →
// 目标地址解析 → DealClient 转发，以及反向的回程。
// （PSK 生成、地址转换那些在上面的用例里已经单独钉过了。）
func TestShadowsocksEndToEndTCP(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	// 借一个空闲端口号给 SS 监听（先占后退，够用）
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// DealClient 会读全局设置/黑名单，走一遍 DB 初始化
	confPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confPath, "conf"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"clients.json", "tasks.json", "hosts.json"} {
		if err := os.WriteFile(filepath.Join(confPath, "conf", n), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldConfPath := common.ConfPath
	common.ConfPath = confPath
	t.Cleanup(func() { common.ConfPath = oldConfPath })

	psk := crypt.NewShadowsocksPSK()
	client := file.NewClient("test", true, true)
	client.Id = 1
	task := &file.Tunnel{
		Id:       1,
		Port:     port,
		ServerIp: "127.0.0.1",
		Mode:     "shadowsocks",
		Password: psk,
		Client:   client,
		Target:   &file.Target{},
		Flow:     &file.Flow{},
	}

	tunnelSide, err := net.Dial("tcp", echoLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	srv := NewShadowsocksModeServer(&tunnelTestBridge{target: tunnelSide}, task)
	if err := srv.Start(); err != nil {
		t.Fatalf("SS 服务端起不来: %v", err)
	}
	defer srv.Close()
	// Start 现在是「建好两个监听就返回」，但保险起见探一下端口
	waitPort(t, port)

	method, err := shadowaead_2022.NewWithPassword(SSMethod, psk, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	c, err := method.DialConn(raw, M.ParseSocksaddr(echoLn.Addr().String()))
	if err != nil {
		t.Fatalf("SS 客户端握手失败: %v", err)
	}
	defer c.Close()

	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte("hello-natpunch")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	got := make([]byte, len("hello-natpunch"))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("读回失败（回程不通）: %v", err)
	}
	if string(got) != "hello-natpunch" {
		t.Fatalf("回程数据不对: %q", got)
	}

	// 记账：客户端那份也必须有数（面板客户端列表的流量列 + 客户端流量上限都看它）。
	// 这曾经是个真 bug：DealClient 的记账对象由调用方传，ss / socks5 / transport
	// 传的是 task.Flow，于是这些模式在客户端列表里流量**永远显示 0**。
	assertClientFlowCounted(t, client)
}

// assertClientFlowCounted 断言客户端那份流量真的被记上了（两个方向都要有）。
// 收尾可能有几百毫秒的拷贝 goroutine 尾巴，所以给它一点时间再判。
func assertClientFlowCounted(t *testing.T, client *file.Client) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		client.Flow.RLock()
		in, out := client.Flow.InletFlow, client.Flow.ExportFlow
		client.Flow.RUnlock()
		if in > 0 && out > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("客户端流量没记上: inlet=%d export=%d（应都 > 0）", in, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
