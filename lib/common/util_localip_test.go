package common

import (
	"net"
	"testing"
)

func mustIPv4(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad ip: %q", s)
	}
	return ip.To4()
}

func mkPathIPs(t *testing.T, ips ...string) []net.IP {
	t.Helper()
	out := make([]net.IP, 0, len(ips))
	for _, s := range ips {
		out = append(out, mustIPv4(t, s))
	}
	return out
}

// mkAddrs("name", "ip", "name2", "ip2", ...)
func mkAddrs(t *testing.T, pairs ...string) []ifaceAddr {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("需要成对的 name,ip，收到 %d 个参数", len(pairs))
	}
	out := make([]ifaceAddr, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, ifaceAddr{name: pairs[i], ip: mustIPv4(t, pairs[i+1])})
	}
	return out
}

// 这次回归的主场景：软路由 WAN 用 DHCP（上联光猫），WAN 口拿到的也是私网地址。
// 旧实现"连接本端是私网就直接上报"，于是把 WAN 口当成了内网。
func TestSelectLocalIP(t *testing.T) {
	cases := []struct {
		name  string
		path  []string
		addrs []string
		want  string
	}{
		{
			name:  "软路由 DHCP：WAN 私网 192.168.1.4 + LAN 192.168.100.1 → 报 LAN",
			path:  []string{"192.168.1.4"},
			addrs: []string{"eth0.2", "192.168.1.4", "br-lan", "192.168.100.1"},
			want:  "192.168.100.1",
		},
		{
			name:  "软路由 DHCP，顺序反过来也一样（WAN 不是第一个）",
			path:  []string{"10.0.0.2"},
			addrs: []string{"br-lan", "192.168.100.1", "wan", "10.0.0.2"},
			want:  "192.168.100.1",
		},
		{
			name: "真实软路由（iStoreOS）：客户端连接被代理走 TUN，路径地址不是任何网卡 → 靠 br-lan 判出 LAN",
			path: nil,
			addrs: []string{
				"eth0", "192.168.1.4", // WAN（上联光猫）
				"br-lan", "192.168.100.1", // LAN
				"Meta", "198.18.0.1", // 代理 TUN（非 RFC1918）
				"docker0", "172.17.0.1", // 虚拟网卡
			},
			want: "192.168.100.1",
		},
		{
			name:  "WAN 也是桥（br-wan）时，靠排除路径接口选出 br-lan",
			path:  []string{"192.168.2.1"},
			addrs: []string{"br-wan", "192.168.2.1", "br-lan", "192.168.100.1"},
			want:  "192.168.100.1",
		},
		{
			name:  "PC 直连单网卡：没有独立 LAN → 回落路径地址",
			path:  []string{"192.168.1.5"},
			addrs: []string{"Ethernet", "192.168.1.5"},
			want:  "192.168.1.5",
		},
		{
			name:  "PC + docker0：不能被 docker 地址抢走",
			path:  []string{"192.168.1.5"},
			addrs: []string{"eth0", "192.168.1.5", "docker0", "172.17.0.1"},
			want:  "192.168.1.5",
		},
		{
			name:  "Windows + vEthernet(WSL/Hyper-V)：同样跳过虚拟网卡",
			path:  []string{"192.168.1.5"},
			addrs: []string{"Ethernet", "192.168.1.5", "vEthernet (Default Switch)", "172.28.1.1"},
			want:  "192.168.1.5",
		},
		{
			name:  "软路由 PPPoE：WAN 是公网（path 为空）→ 正常报 LAN",
			path:  nil,
			addrs: []string{"pppoe-wan", "1.2.3.4", "br-lan", "192.168.100.1"},
			want:  "192.168.100.1",
		},
		{
			name:  "多个候选：优先 192.168.x（而不是 10.x / 172.16.x）",
			path:  []string{"192.168.1.4"},
			addrs: []string{"eth0.2", "192.168.1.4", "br-guest", "10.0.0.1", "br-lan", "192.168.100.1"},
			want:  "192.168.100.1",
		},
		{
			name:  "没有 192.168 时候选 10.x 优先于 172.16.x",
			path:  []string{"192.168.1.4"},
			addrs: []string{"eth0.2", "192.168.1.4", "br-iot", "172.16.5.1", "br-lan", "10.1.2.3"},
			want:  "10.1.2.3",
		},
		{
			name:  "兜底：只有虚拟网卡的私网地址时也要报一个",
			path:  nil,
			addrs: []string{"docker0", "172.17.0.1"},
			want:  "172.17.0.1",
		},
		{
			name:  "什么私网地址都没有 → 空",
			path:  nil,
			addrs: []string{"eth0", "203.0.113.7"},
			want:  "",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := selectLocalIP(mkPathIPs(t, c.path...), mkAddrs(t, c.addrs...))
			if c.want == "" {
				if got != nil {
					t.Fatalf("want nil, got %v", got)
				}
				return
			}
			if got == nil || got.String() != c.want {
				t.Fatalf("want %s, got %v", c.want, got)
			}
		})
	}
}

// br-lan 是 OpenWrt 的 LAN 桥，绝不能被当成"虚拟网卡"跳过
func TestIsVirtualIface(t *testing.T) {
	for _, n := range []string{"docker0", "veth1a2b", "vEthernet (Default Switch)", "vmnet8", "VirtualBox Host-Only Network", "tun0", "wg0"} {
		if !isVirtualIface(n) {
			t.Errorf("%q 应判为虚拟网卡", n)
		}
	}
	for _, n := range []string{"br-lan", "br-guest", "eth0", "eth0.2", "wan", "pppoe-wan", "Ethernet", "Wi-Fi", "WLAN"} {
		if isVirtualIface(n) {
			t.Errorf("%q 不应判为虚拟网卡", n)
		}
	}
}
