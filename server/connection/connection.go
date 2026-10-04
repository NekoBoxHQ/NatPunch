package connection

import (
	"errors"
	"net"
	"strconv"

	"ehang.io/nps/lib/pmux"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

var pMux *pmux.PortMux
var bridgePort string
var httpsPort string
var httpPort string
var webPort string

func InitConnectionService() {
	bridgePort = beego.AppConfig.String("bridge_port")
	httpsPort = beego.AppConfig.String("https_proxy_port")
	httpPort = beego.AppConfig.String("http_proxy_port")
	webPort = beego.AppConfig.String("web_port")

	if httpPort == bridgePort || httpsPort == bridgePort || webPort == bridgePort {
		port, err := strconv.Atoi(bridgePort)
		if err != nil {
			// 库代码禁止 os.Exit：端口非法时跳过端口复用，交由各监听器正常报错（阶段三 #15）
			logs.Error("bridge_port 解析失败，跳过端口复用: %v", err)
			return
		}
		pMux = pmux.NewPortMux(port, beego.AppConfig.String("web_host"))
	}
}

func GetBridgeListener(tp string) (net.Listener, error) {
	logs.Info("server start, the bridge type is %s, the bridge port is %s", tp, bridgePort)
	var p int
	var err error
	if p, err = strconv.Atoi(bridgePort); err != nil {
		return nil, err
	}
	if pMux != nil {
		l := pMux.GetClientListener()
		if l == nil {
			return nil, errors.New("pmux client listener not ready")
		}
		return l, nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(beego.AppConfig.String("bridge_ip")), Port: p})
}

func GetHttpListener() (net.Listener, error) {
	if pMux != nil && httpPort == bridgePort {
		logs.Info("start http listener, port is", bridgePort)
		l := pMux.GetHttpListener()
		if l == nil {
			return nil, errors.New("pmux http listener not ready")
		}
		return l, nil
	}
	logs.Info("start http listener, port is", httpPort)
	return getTcpListener(beego.AppConfig.String("http_proxy_ip"), httpPort)
}

func GetHttpsListener() (net.Listener, error) {
	if pMux != nil && httpsPort == bridgePort {
		logs.Info("start https listener, port is", bridgePort)
		l := pMux.GetHttpsListener()
		if l == nil {
			return nil, errors.New("pmux https listener not ready")
		}
		return l, nil
	}
	logs.Info("start https listener, port is", httpsPort)
	return getTcpListener(beego.AppConfig.String("http_proxy_ip"), httpsPort)
}

func GetWebManagerListener() (net.Listener, error) {
	if pMux != nil && webPort == bridgePort {
		logs.Info("Web management start, access port is", bridgePort)
		l := pMux.GetManagerListener()
		if l == nil {
			return nil, errors.New("pmux manager listener not ready")
		}
		return l, nil
	}
	logs.Info("web management start, access port is", webPort)
	return getTcpListener(beego.AppConfig.String("web_ip"), webPort)
}

func getTcpListener(ip, p string) (net.Listener, error) {
	port, err := strconv.Atoi(p)
	if err != nil {
		logs.Error("listen port 解析失败: %v", err)
		return nil, err
	}
	if ip == "" {
		ip = "0.0.0.0"
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(ip), Port: port})
}
