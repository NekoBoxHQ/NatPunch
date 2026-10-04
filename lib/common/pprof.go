package common

import (
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"net"
	"net/http"
	_ "net/http/pprof"
)

func InitPProfFromFile() {
	ip := beego.AppConfig.String("pprof_ip")
	p := beego.AppConfig.String("pprof_port")
	if len(ip) > 0 && len(p) > 0 && IsPort(p) {
		runPProf(ip + ":" + p)
	}
}

func InitPProfFromArg(arg string) {
	if len(arg) > 0 {
		runPProf(arg)
	}
}

func runPProf(ipPort string) {
	host, port, err := net.SplitHostPort(ipPort)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		// 安全默认：绝不把 pprof 暴露到非回环地址（阶段三 #11）
		host = "127.0.0.1"
	}
	if port == "" {
		port = "6060"
	}
	addr := net.JoinHostPort(host, port)
	go func() {
		_ = http.ListenAndServe(addr, nil)
	}()
	logs.Info("PProf debug listen on", addr)
}
