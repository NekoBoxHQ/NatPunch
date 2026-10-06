package client

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/config"
	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/astaxie/beego/logs"
	"github.com/xtaci/kcp-go"
	"golang.org/x/net/proxy"
)

var tlsEnable1 = false

func SetTlsEnable(tlsEnable11 bool) {
	tlsEnable1 = tlsEnable11
}

func GetTlsEnable() bool {
	return tlsEnable1
}

// SetTlsFingerprint 透传期望的服务端桥接证书指纹（F2-2，三态见 lib/crypt/tls.go）
func SetTlsFingerprint(fp string) {
	crypt.SetTlsFingerprint(fp)
}

func GetTlsFingerprint() string {
	return crypt.GetTlsFingerprint()
}

func GetTaskStatus(path string) {
	cnf, err := config.NewConfig(path)
	if err != nil {
		log.Fatalln(err)
	}
	c, err := NewConn(cnf.CommonConfig.Tp, cnf.CommonConfig.VKey, cnf.CommonConfig.Server, common.WORK_CONFIG, cnf.CommonConfig.ProxyUrl)
	if err != nil {
		log.Fatalln(err)
	}
	if _, err := c.Write([]byte(common.WORK_STATUS)); err != nil {
		log.Fatalln(err)
	}
	//read now vKey and write to server
	vkeyPath := filepath.Join(common.GetTmpPath(), "natpunch-client-vkey.txt")
	f, err := common.ReadAllFromFile(vkeyPath)
	if err != nil {
		// 兼容旧命名：早期版本存在 npc_vkey.txt，直接改名会让升级后首次启动在这里 Fatalln
		f, err = common.ReadAllFromFile(filepath.Join(common.GetTmpPath(), "npc_vkey.txt"))
	}
	if err != nil {
		log.Fatalln(err)
	} else if _, err := c.Write([]byte(crypt.Md5(string(f)))); err != nil {
		log.Fatalln(err)
	}
	var isPub bool
	binary.Read(c, binary.LittleEndian, &isPub)
	if l, err := c.GetLen(); err != nil {
		log.Fatalln(err)
	} else if l < 0 || l > 4<<20 {
		// 长度字段来自服务端，GetShortContent 会直接 make([]byte, l)：
		// 不给上界的话一个 2^31 的返回值就能让本进程申请 2GB（OOM）。
		log.Fatalln("响应长度非法:", l)
	} else if b, err := c.GetShortContent(l); err != nil {
		log.Fatalln(err)
	} else {
		arr := strings.Split(string(b), common.CONN_DATA_SEQ)
		for _, v := range cnf.Hosts {
			if common.InStrArr(arr, v.Remark) {
				log.Println(v.Remark, "ok")
			} else {
				log.Println(v.Remark, "not running")
			}
		}
		for _, v := range cnf.Tasks {
			ports := common.GetPorts(v.Ports)
			if v.Mode == "secret" {
				ports = append(ports, 0)
			}
			for _, vv := range ports {
				var remark string
				if len(ports) > 1 {
					remark = v.Remark + "_" + strconv.Itoa(vv)
				} else {
					remark = v.Remark
				}
				if common.InStrArr(arr, remark) {
					log.Println(remark, "ok")
				} else {
					log.Println(remark, "not running")
				}
			}
		}
	}
	os.Exit(0)
}

var errAdd = errors.New("The server returned an error, which port or host may have been occupied or not allowed to open.")

func StartFromFile(path string) {
	first := true
	cnf, err := config.NewConfig(path)
	if err != nil {
		logs.Error("Config file %s loading error %s", path, err.Error())
		os.Exit(0)
	}
	// G5：err == nil 但缺 [common] 段时不得解引用 err（阶段三）
	if cnf.CommonConfig == nil {
		logs.Error("Config file %s: missing [common] section", path)
		os.Exit(0)
	}
	logs.Info("Loading configuration file %s successfully", path)

	SetTlsEnable(cnf.CommonConfig.TlsEnable)
	SetTlsFingerprint(cnf.CommonConfig.TlsFingerprint)
	if cnf.CommonConfig.TlsEnable && cnf.CommonConfig.TlsStrict && cnf.CommonConfig.TlsFingerprint == "" {
		logs.Error("tls_strict=true 但未配置 tls_fingerprint，拒绝启动（F2-2）")
		os.Exit(0)
	}
	logs.Info("the version of client is %s, the core version of client is %s,tls enable is %t", version.VERSION, version.GetVersion(), GetTlsEnable())
	var c *conn.Conn // 在 re 标签前声明：重连循环各处 goto re 时按需关闭旧连接（阶段三 #5）
re:
	if first || cnf.CommonConfig.AutoReconnection {
		if !first {
			logs.Info("Reconnecting...")
			time.Sleep(time.Second * 5)
		}
	} else {
		return
	}
	first = false
	// 重连前显式关闭可能已建立的旧连接，避免每次失败重试泄漏一条（阶段三 #5）
	if c != nil {
		c.Close()
		c = nil
	}
	c, err = NewConn(cnf.CommonConfig.Tp, cnf.CommonConfig.VKey, cnf.CommonConfig.Server, common.WORK_CONFIG, cnf.CommonConfig.ProxyUrl)
	if err != nil {
		if c != nil {
			c.Close()
		}
		logs.Error(err)
		goto re
	}
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		logs.Error("read isPub: %v", err)
		c.Close()
		c = nil
		goto re
	}

	// get tmp password
	var b []byte
	vkey := cnf.CommonConfig.VKey
	if isPub {
		// send global configuration to server and get status of config setting
		if _, err := c.SendInfo(cnf.CommonConfig.Client, common.NEW_CONF); err != nil {
			logs.Error(err)
			goto re
		}
		if !c.GetAddStatus() {
			logs.Error("the web_user may have been occupied!")
			goto re
		}

		if b, err = c.GetShortContent(16); err != nil {
			logs.Error(err)
			goto re
		}
		vkey = string(b)
	}
	// O_NOFOLLOW：/tmp 若可被非特权用户写入，攻击者可以预置同名符号链接，
	// 让以 root 身份运行的客户端把 vkey 写到任意文件上（os.WriteFile 是跟随链接的）。
	vkeyFile := filepath.Join(common.GetTmpPath(), "natpunch-client-vkey.txt")
	if vf, verr := os.OpenFile(vkeyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0600); verr != nil {
		logs.Error("写入 vkey 文件失败:", verr)
	} else {
		if _, werr := vf.Write([]byte(vkey)); werr != nil {
			logs.Error("写入 vkey 文件失败:", werr)
		}
		vf.Close()
	}

	//send hosts to server
	for _, v := range cnf.Hosts {
		if _, err := c.SendInfo(v, common.NEW_HOST); err != nil {
			logs.Error(err)
			goto re
		}
		if !c.GetAddStatus() {
			logs.Error(errAdd, v.Host)
			goto re
		}
	}

	//send  task to server
	for _, v := range cnf.Tasks {
		if _, err := c.SendInfo(v, common.NEW_TASK); err != nil {
			logs.Error(err)
			goto re
		}
		if !c.GetAddStatus() {
			logs.Error(errAdd, v.Ports, v.Remark)
			goto re
		}
	}

	c.Close()
	if cnf.CommonConfig.Client.WebUserName == "" || cnf.CommonConfig.Client.WebPassword == "" {
		logs.Notice("web access login username:user password:%s", vkey)
	} else {
		logs.Notice("web access login username:%s password:%s", cnf.CommonConfig.Client.WebUserName, cnf.CommonConfig.Client.WebPassword)
	}
	NewRPClient(cnf.CommonConfig.Server, vkey, cnf.CommonConfig.Tp, cnf.CommonConfig.ProxyUrl, cnf, cnf.CommonConfig.DisconnectTime).Start()
	goto re
}

// Create a new connection with the server and verify it
func NewConn(tp string, vkey string, server string, connType string, proxyUrl string) (*conn.Conn, error) {
	var err error
	var connection net.Conn
	var sess *kcp.UDPSession
	if tp == "tcp" {
		if proxyUrl != "" {
			u, er := url.Parse(proxyUrl)
			if er != nil {
				return nil, er
			}
			switch u.Scheme {
			case "socks5":
				n, er := proxy.FromURL(u, nil)
				if er != nil {
					return nil, er
				}
				connection, err = n.Dial("tcp", server)
			default:
				connection, err = NewHttpProxyConn(u, server)
			}
		} else {
			if GetTlsEnable() {
				// tls 流量加密（F2-2 三态：配置指纹则严格校验，否则沿用旧行为并告警）
				if crypt.GetTlsFingerprint() == "" {
					logs.Warn("TLS 已启用但未配置 tls_fingerprint：仅防被动窃听，不防中间人；建议配置服务端指纹（F2-2）")
				}
				connection, err = tls.Dial("tcp", server, crypt.TlsDialConfig())
			} else {
				connection, err = net.Dial("tcp", server)
			}

			//header := &proxyproto.Header{
			//	Version:           1,
			//	Command:           proxyproto.PROXY,
			//	TransportProtocol: proxyproto.TCPv4,
			//	SourceAddr: &net.TCPAddr{
			//		IP:   net.ParseIP("10.1.1.1"),
			//		Port: 1000,
			//	},
			//	DestinationAddr: &net.TCPAddr{
			//		IP:   net.ParseIP("20.2.2.2"),
			//		Port: 2000,
			//	},
			//}
			//
			//_, err = header.WriteTo(connection)
			//_, err = io.WriteString(connection, "HELO")
		}
	} else {
		sess, err = kcp.DialWithOptions(server, nil, 10, 3)
		if err == nil {
			conn.SetUdpSession(sess)
			connection = sess
		}
	}
	if err != nil {
		return nil, err
	}
	connection.SetDeadline(time.Now().Add(time.Second * 10))
	defer connection.SetDeadline(time.Time{})
	c := conn.NewConn(connection)
	if _, err := c.Write([]byte(common.CONN_TEST)); err != nil {
		return nil, err
	}
	if err := c.WriteLenContent([]byte(version.GetVersion())); err != nil {
		return nil, err
	}
	if err := c.WriteLenContent([]byte(version.VERSION)); err != nil {
		return nil, err
	}
	b, err := c.GetShortContent(32)
	if err != nil {
		logs.Error(err)
		return nil, err
	}
	if crypt.Md5(version.GetVersion()) != string(b) {
		// 版本握手不匹配不阻止连接（上游历史行为），记录日志便于排障
		logs.Notice("客户端核心版本与服务器期望版本不匹配（client %s），继续连接", version.GetVersion())
	}
	if _, err := c.Write([]byte(common.Getverifyval(vkey))); err != nil {
		return nil, err
	}
	if s, err := c.ReadFlag(); err != nil {
		return nil, err
	} else if s == common.VERIFY_EER {
		return nil, errors.New(fmt.Sprintf("Validation key %s incorrect", vkey))
	}
	if _, err := c.Write([]byte(connType)); err != nil {
		return nil, err
	}
	c.SetAlive(tp)

	return c, nil
}

// http proxy connection
func NewHttpProxyConn(url *url.URL, remoteAddr string) (net.Conn, error) {
	req, err := http.NewRequest("CONNECT", "http://"+remoteAddr, nil)
	if err != nil {
		return nil, err
	}
	password, _ := url.User.Password()
	req.Header.Set("Authorization", "Basic "+basicAuth(strings.Trim(url.User.Username(), " "), password))
	// we make a http proxy request
	proxyConn, err := net.Dial("tcp", url.Host)
	if err != nil {
		return nil, err
	}
	if err := req.Write(proxyConn); err != nil {
		return nil, err
	}
	res, err := http.ReadResponse(bufio.NewReader(proxyConn), req)
	if err != nil {
		return nil, err
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("Proxy error " + res.Status)
	}
	return proxyConn, nil
}

// get a basic auth string
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
