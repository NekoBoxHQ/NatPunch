package common

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"golang.org/x/crypto/bcrypt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
)

// Check if the Request request is validated
func CheckAuth(r *http.Request, user, passwd string) bool {
	s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(s) != 2 {
		s = strings.SplitN(r.Header.Get("Proxy-Authorization"), " ", 2)
		if len(s) != 2 {
			return false
		}
	}

	b, err := base64.StdEncoding.DecodeString(s[1])
	if err != nil {
		return false
	}

	pair := strings.SplitN(string(b), ":", 2)
	if len(pair) != 2 {
		return false
	}
	// 常量时间比较，避免用户名/密码时序侧信道（F2-1 / G3）
	u, p := []byte(pair[0]), []byte(pair[1])
	us, ps := []byte(user), []byte(passwd)
	if len(u) != len(us) || len(p) != len(ps) {
		return false
	}
	return subtle.ConstantTimeCompare(u, us) == 1 && subtle.ConstantTimeCompare(p, ps) == 1
}

// get bool by str
func GetBoolByStr(s string) bool {
	switch s {
	case "1", "true":
		return true
	}
	return false
}

// HashPassword bcrypt 哈希面板密码（F2-3）
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// VerifyPassword 兼容 bcrypt 与存量明文的面板密码校验（F2-3）：
//   - 存值为 $2 开头（bcrypt）→ bcrypt.CompareHashAndPassword（自带常量时间）
//   - 存值为明文（存量）→ subtle.ConstantTimeCompare
//
// 任何错误一律返回 false。
func VerifyPassword(stored, plain string) bool {
	if stored == "" {
		return plain == ""
	}
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(plain)) == nil
	}
	u, p := []byte(stored), []byte(plain)
	if len(u) != len(p) {
		return false
	}
	return subtle.ConstantTimeCompare(u, p) == 1
}

// int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// Get verify value
func Getverifyval(vkey string) string {
	return crypt.Md5(vkey)
}

// Change headers and host of request
func ChangeHostAndHeader(r *http.Request, host string, header string, addr string) {
	if host != "" {
		r.Host = host
	}
	if header != "" {
		h := strings.Split(header, "\n")
		for _, v := range h {
			hd := strings.Split(v, ":")
			if len(hd) == 2 {
				r.Header.Set(hd[0], hd[1])
			}
		}
	}
	addr = strings.Split(addr, ":")[0]
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		addr = strings.Join(prior, ", ") + ", " + addr
	}

	addOrigin, _ := beego.AppConfig.Bool("http_add_origin_header")

	if addOrigin {
		logs.Debug("set X-Forwarded-For X-Real-IP = " + addr)
		r.Header.Set("X-Forwarded-For", addr)
		r.Header.Set("X-Real-IP", addr)
	}
}

// Read file content by file path
func ReadAllFromFile(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// FileExists reports whether the named file or directory exists.
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// Judge whether the TCP port can open normally
func TestTcpPort(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			l.Close()
		}
	}()
	if err != nil {
		return false
	}
	return true
}

// Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			l.Close()
		}
	}()
	if err != nil {
		return false
	}
	return true
}

// Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	binary.Write(raw, binary.LittleEndian, int32(len(b)))
	binary.Write(raw, binary.LittleEndian, b)
}

// get seq str
func GetWriteStr(v ...string) []byte {
	buffer := new(bytes.Buffer)
	var l int32
	for _, v := range v {
		l += int32(len([]byte(v))) + int32(len([]byte(CONN_DATA_SEQ)))
		binary.Write(buffer, binary.LittleEndian, []byte(v))
		binary.Write(buffer, binary.LittleEndian, []byte(CONN_DATA_SEQ))
	}
	return buffer.Bytes()
}

// inArray str interface
func InStrArr(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// inArray int interface
func InIntArr(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// format ports str to a int array
func GetPorts(p string) []int {
	var ps []int
	arr := strings.Split(p, ",")
	for _, v := range arr {
		fw := strings.Split(v, "-")
		if len(fw) == 2 {
			if IsPort(fw[0]) && IsPort(fw[1]) {
				start, _ := strconv.Atoi(fw[0])
				end, _ := strconv.Atoi(fw[1])
				for i := start; i <= end; i++ {
					ps = append(ps, i)
				}
			} else {
				continue
			}
		} else if IsPort(v) {
			p, _ := strconv.Atoi(v)
			ps = append(ps, p)
		}
	}
	return ps
}

// is the string a port
func IsPort(p string) bool {
	pi, err := strconv.Atoi(p)
	if err != nil {
		return false
	}
	if pi > 65536 || pi < 1 {
		return false
	}
	return true
}

// if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return "127.0.0.1:" + s
}

// get address from the complete address
func GetIpByAddr(addr string) string {
	// 如果是 IPv6 地址，可能的格式为[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:8080或[2001:0db8:85a3:0000:0000:8a2e:0370:7334]
	// IPv4 地址，可能的格式为192.168.1.1:80或192.168.1.1
	// 截取地址
	if strings.Contains(addr, "[") {
		addr = strings.Split(addr, "[")[1]
		addr = strings.Split(addr, "]")[0]
	} else {
		addr = strings.Split(addr, ":")[0]
	}
	return addr
}

// in 判断 target 是否在 str_array 中。
// 用线性扫描，而不是 sort + SearchStrings：原实现会就地排序**调用方传入的切片**，
// 而该切片（Client.BlackIpList / IpWhiteList）会被多个转发 goroutine 并发读取，
// 就地排序等于在共享数据上做无同步写（-race 可复现）。IP 名单通常只有个位数项，
// 线性扫描的成本可以忽略。
func in(target string, str_array []string) bool {
	for _, s := range str_array {
		if s == target {
			return true
		}
	}
	return false
}

// ConstantTimeStrEq 常量时间字符串比较，用于 vkey / 口令这类逐字符可试探的凭据。
// （长度不等时 subtle 会立即返回 0，这一点与标准库既有实现一致。）
func ConstantTimeStrEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// maxCertFileSize 证书 / 私钥文件的读取上限。正常 PEM 远小于此。
const maxCertFileSize = 1 << 20

// ReadCertFile 读取证书 / 私钥文件，要求必须是普通文件且不超过 maxCertFileSize。
//
// 证书路径来自客户端注册 host 时上报的配置（file.Host.CertFilePath / KeyFilePath），
// 服务端不能对任意路径做无界读取：把路径指向 /dev/zero、/proc/kcore 或大文件即可
// 让服务端一直读到 OOM（OpenWrt 内存受限，影响被放大），同时还能当文件存在性探针用。
func ReadCertFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("证书路径不是普通文件: %s", path)
	}
	if fi.Size() > maxCertFileSize {
		return nil, fmt.Errorf("证书文件过大（%d 字节，上限 %d）: %s", fi.Size(), maxCertFileSize, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxCertFileSize))
}

// 判断访问地址是否在黑名单内
func IsBlackIp(ipPort, vkey string, blackIpList []string) bool {
	ip := GetIpByAddr(ipPort)
	if in(ip, blackIpList) {
		logs.Error("IP地址[" + ip + "]在隧道[" + vkey + "]黑名单列表内")
		return true
	}

	return false
}

// 判断访问地址是否在白名单内
func IsAuthIp(ipPort, vkey string, authIpList []string) bool {
	ip := GetIpByAddr(ipPort)

	if in(ip, authIpList) {
		return false
	}
	logs.Error("IP地址[" + ip + "]不在隧道[" + vkey + "]白名单列表内")
	return true
}

func CopyBuffer(dst io.Writer, src io.Reader, label ...string) (written int64, err error) {
	buf := CopyBuff.Get()
	defer CopyBuff.Put(buf)
	for {
		nr, er := src.Read(buf)
		//if len(pr)>0 && pr[0] && nr > 50 {
		//	logs.Warn(string(buf[:50]))
		//}
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}

// send this ip forget to get a local udp port
func ParseStr(str string) (string, error) {
	tmp := template.New("natpunch-client")
	var err error
	w := new(bytes.Buffer)
	if tmp, err = tmp.Parse(str); err != nil {
		return "", err
	}
	if err = tmp.Execute(w, GetEnvMap()); err != nil {
		return "", err
	}
	return w.String(), nil
}

// get env
func GetEnvMap() map[string]string {
	m := make(map[string]string)
	environ := os.Environ()
	for i := range environ {
		tmp := strings.Split(environ[i], "=")
		if len(tmp) == 2 {
			m[tmp[0]] = tmp[1]
		}
	}
	return m
}

// throw the empty element of the string array
func TrimArr(arr []string) []string {
	newArr := make([]string, 0)
	for _, v := range arr {
		if v != "" {
			newArr = append(newArr, v)
		}
	}
	return newArr
}

func IsArrContains(arr []string, val string) bool {
	if arr == nil {
		return false
	}
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// remove value from string array
func RemoveArrVal(arr []string, val string) []string {
	for k, v := range arr {
		if v == val {
			arr = append(arr[:k], arr[k+1:]...)
			return arr
		}
	}
	return arr
}

// convert bytes to num
func BytesToNum(b []byte) int {
	var str string
	for i := 0; i < len(b); i++ {
		str += strconv.Itoa(int(b[i]))
	}
	x, _ := strconv.Atoi(str)
	return int(x)
}

// get the length of the sync map
func GeSynctMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}

var externalIp string
var externalIpOnce sync.Once

// GetExternalIp 获取公网出口 IP：优先 HTTPS 源（阶段三 #16），失败回退 HTTP；
// 结果由 sync.Once 缓存，避免无锁全局读写与重复外呼。
func GetExternalIp() string {
	externalIpOnce.Do(func() {
		client := &http.Client{Timeout: 5 * time.Second}
		for _, u := range []string{"https://api.ipify.org", "https://myexternalip.com/raw", "http://myexternalip.com/raw"} {
			resp, err := client.Get(u)
			if err != nil {
				continue
			}
			content, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			if ip := strings.TrimSpace(string(content)); ip != "" {
				externalIp = ip
				return
			}
		}
	})
	return externalIp
}

func GetIntranetIp() (error, string) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, ""
	}
	for _, address := range addrs {
		// 检查ip地址判断是否回环地址
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return nil, ipnet.IP.To4().String()
			}
		}
	}
	return errors.New("get intranet ip error"), ""
}

// isUsefulLocalIPv4 reports whether ip is a usable unicast IPv4 for display:
// excludes loopback, link-local (169.254.0.0/16), unspecified and multicast.
// No RFC1918 requirement, so non-standard private ranges used in practice
// (e.g. 192.169.0.0/16) are kept.
func isUsefulLocalIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return !v4.IsLoopback() && !v4.IsLinkLocalUnicast() && !v4.IsLinkLocalMulticast() && !v4.IsUnspecified() && !v4.IsMulticast()
}

// isUsefulPrivateIPv4 reports whether ip is a usable LAN IPv4 for display:
// RFC1918 private only — excludes loopback, link-local (169.254.0.0/16), public, etc.
func isUsefulPrivateIPv4(ip net.IP) bool {
	if !isUsefulLocalIPv4(ip) {
		return false
	}
	// RFC1918 only (not 169.254, not public)
	return ip.To4().IsPrivate()
}

func ipFromAddr(addr net.Addr) net.IP {
	if addr == nil {
		return nil
	}
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP
	case *net.UDPAddr:
		return a.IP
	default:
		return net.ParseIP(GetIpByAddr(addr.String()))
	}
}

// GetLocalIPs returns IPv4 addresses useful for identifying the natpunch-client host.
// GetLocalIPs 返回最能代表这台设备"内网地址"的一个 IPv4（面板上「内网」那一栏显示的就是它）。
//
// 采集到的事实交给 selectLocalIP 做选择，规则见那里的注释。这里只负责取数据：
//   - 通往 natpunch 服务端的本地地址（连接本端 + 出网本端）——即"WAN 腿"上的地址；
//   - 本机 up 且非 loopback 接口上的全部 IPv4。
//
// 曾经的实现是"连接本端只要不是公网 IP 就直接上报"，在 PC 直连场景下对，
// 但软路由 WAN 用 DHCP/上联光猫时 WAN 口本身就是私网（如 192.168.1.4），
// 于是把 WAN 口当成了内网（v26.10.50 及更早）。现在改为先排除 WAN 腿再找 LAN。
func GetLocalIPs(conn net.Conn) string {
	var pathIPs []net.IP
	if conn != nil {
		if ip := ipFromAddr(conn.LocalAddr()); ip != nil && isUsefulLocalIPv4(ip) && !IsPublicIP(ip) {
			pathIPs = append(pathIPs, ip.To4())
		}
	}
	// 出网接口（向公网地址 UDP 拨号，不会真的发包）——与上面同一个思路
	if tmp, err := net.Dial("udp", "114.114.114.114:53"); err == nil {
		ip := ipFromAddr(tmp.LocalAddr())
		_ = tmp.Close()
		if ip != nil && isUsefulLocalIPv4(ip) && !IsPublicIP(ip) {
			pathIPs = append(pathIPs, ip.To4())
		}
	}

	var addrs []ifaceAddr
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			list, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, address := range list {
				ipnet, ok := address.(*net.IPNet)
				if !ok {
					continue
				}
				if v4 := ipnet.IP.To4(); v4 != nil {
					addrs = append(addrs, ifaceAddr{name: iface.Name, ip: v4})
				}
			}
		}
	}

	if ip := selectLocalIP(pathIPs, addrs); ip != nil {
		return ip.String()
	}
	return ""
}

// ifaceAddr：某个接口上的一个 IPv4（只为把选择逻辑做成可单测的纯函数）
type ifaceAddr struct {
	name string
	ip   net.IP
}

// selectLocalIP 从本机地址里挑出要上报的"内网地址"。
//
// 顺序（每一步都有对应单测）：
//  1. 先把"承载 pathIPs 的接口"标成 WAN 腿，它的地址**不作为 LAN 候选** ——
//     软路由 DHCP 模式（WAN 上联光猫/上级路由）下 WAN 自己就是私网，不排除就会把
//     WAN 口当成内网（就是这次要修的现象）。
//  2. 在剩下的接口里挑 RFC1918 地址，跳过 docker/veth/vEthernet 之类虚拟网卡，
//     优先级 192.168.0.0/16 > 10.0.0.0/8 > 172.16.0.0/12（家用主 LAN 最常见 192.168.x）。
//  3. 没有独立 LAN（PC 直连、单网卡）→ 回落到 path 地址本身，保住原有正确行为。
//  4. 最后兜底：任何可用的 RFC1918 地址（含虚拟网卡），保证不至于什么都不报。
func selectLocalIP(pathIPs []net.IP, addrs []ifaceAddr) net.IP {
	wan := make(map[string]struct{})
	for _, a := range addrs {
		for _, p := range pathIPs {
			if p != nil && p.Equal(a.ip) {
				wan[a.name] = struct{}{}
			}
		}
	}

	var best net.IP
	bestRank := 0
	for _, a := range addrs {
		if _, isWan := wan[a.name]; isWan {
			continue
		}
		if !isUsefulPrivateIPv4(a.ip) || isVirtualIface(a.name) {
			continue
		}
		r := lanRank(a.ip)
		if r > 0 && isBridgeIface(a.name) {
			// 软路由上"桥"比物理口更像 LAN（OpenWrt/iStoreOS 的 br-lan）。
			// 这一条是给"排除 WAN 腿"失效时兜底的：例如客户端连接被代理走 TUN
			// （路径地址是 198.18.x 这类非私网地址，被当作公网丢弃）时，
			// eth0(WAN) 与 br-lan(LAN) 同为 192.168.x 会打成平局，靠它才能选中 LAN。
			r += 10
		}
		if r > bestRank {
			best, bestRank = a.ip, r
		}
	}
	if best != nil {
		return best
	}

	for _, p := range pathIPs {
		if p != nil {
			return p
		}
	}
	for _, a := range addrs {
		if isUsefulPrivateIPv4(a.ip) {
			return a.ip
		}
	}
	return nil
}

// lanRank 越大越优先：192.168.x 最像家用主 LAN
func lanRank(ip net.IP) int {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	switch {
	case v4[0] == 192 && v4[1] == 168:
		return 3
	case v4[0] == 10:
		return 2
	default: // 172.16.0.0/12
		return 1
	}
}

// isVirtualIface 判断是不是虚拟网卡：docker0 / veth* / vEthernet / vmnet* 这些上面的
// 私网地址不是这台设备真正的局域网地址，挑 LAN 时要跳过。
//
// 注意**别误伤 OpenWrt 的 LAN 桥 `br-lan`**：这里只匹配具体前缀，不匹配裸 "br"。
func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{
		"docker", "veth", "virbr", "vmnet", "vbox", "vethernet", "hyper-v",
		"wsl", "virtual", "vnic", "tun", "tap", "wg", "zt", "tailscale",
		"utun", "isatap", "teredo", "bluetooth", "hamachi", "radmin",
	} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// isBridgeIface 判断是不是 Linux 网桥（OpenWrt / iStoreOS 的 LAN 常是 `br-lan`）。
//
// 只认 `br-xxx` 与 `brN` 两种写法，避免把 Windows 上以 "br" 开头的物理网卡名
// （如 "Broadcom ..."）误判成桥。
func isBridgeIface(name string) bool {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, "br-") {
		return true
	}
	if len(n) >= 3 && n[0] == 'b' && n[1] == 'r' && n[2] >= '0' && n[2] <= '9' {
		return true
	}
	return false
}

func IsPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}
	if ip4 := IP.To4(); ip4 != nil {
		switch true {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}
	return false
}

func GetServerIpByClientIp(clientIp net.IP) string {
	if IsPublicIP(clientIp) {
		return GetExternalIp()
	}
	_, ip := GetIntranetIp()
	return ip
}

func PrintVersion() {
	fmt.Printf("Version: %s\nCore version: %s\nSame core version of client and server can connect each other\n", version.VERSION, version.GetVersion())
}
