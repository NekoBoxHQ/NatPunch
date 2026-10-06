package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NekoBoxHQ/NatPunch/client"
	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/config"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/install"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/astaxie/beego/logs"
	"github.com/ccding/go-stun/stun"
)

var (
	serverAddr     = flag.String("server", "", "Server addr (ip:port)")
	configPath     = flag.String("config", "", "Configuration file path")
	verifyKey      = flag.String("vkey", "", "Authentication key")
	logType        = flag.String("log", "stdout", "Log output mode（stdout|file）")
	connType       = flag.String("type", "tcp", "Connection type with the server（kcp|tcp）")
	proxyUrl       = flag.String("proxy", "", "proxy socks5 url(eg:socks5://111:222@127.0.0.1:9007)")
	logLevel       = flag.String("log_level", "7", "log level 0~7")
	registerTime   = flag.Int("time", 2, "register time long /h")
	localPort      = flag.Int("local_port", 2000, "p2p local port")
	password       = flag.String("password", "", "p2p password flag")
	target         = flag.String("target", "", "p2p target")
	localType      = flag.String("local_type", "p2p", "p2p target")
	logPath        = flag.String("log_path", "", "natpunch-client log path")
	debug          = flag.Bool("debug", true, "natpunch-client debug")
	pprofAddr      = flag.String("pprof", "", "PProf debug addr (ip:port)")
	stunAddr       = flag.String("stun_addr", "stun.stunprotocol.org:3478", "stun server address (eg:stun.stunprotocol.org:3478)")
	ver            = flag.Bool("version", false, "show current version")
	disconnectTime = flag.Int("disconnect_timeout", 60, "not receiving check packet times, until timeout will disconnect the client")
	tlsEnable      = flag.Bool("tls_enable", false, "enable tls")
	tlsFingerprint = flag.String("tls_fingerprint", "", "server bridge cert SHA-256 fingerprint (F2-2); empty = legacy behavior with warning")
	tlsStrict      = flag.Bool("tls_strict", false, "require tls_fingerprint when tls_enable, refuse to start otherwise (F2-2)")
)

// 客户端是「被安装器拉起的常驻进程」，不做任何自安装 / 服务管理：
// 开机自启与进程守护由 install.sh 写的 procd / systemd 单元负责，
// 升级由 uninstall_client.sh 负责。这样二进制里只有一条启动路径，
// 不会出现「二进制自装的服务」和「脚本装的服务」并存、互抢同一个 vkey 的情况。
func main() {
	flag.Parse()
	logs.Reset()
	logs.EnableFuncCallDepth(true)
	logs.SetLogFuncCallDepth(3)
	if *ver {
		common.PrintVersion()
		return
	}
	if *logPath == "" {
		*logPath = common.GetClientLogPath()
	}
	if *debug {
		logs.SetLogger(logs.AdapterConsole, `{"level":`+*logLevel+`,"color":true}`)
	} else {
		logs.SetLogger(logs.AdapterFile, `{"level":`+*logLevel+`,"filename":"`+*logPath+`","daily":false,"maxlines":100000,"color":true}`)
	}

	// 辅助子命令：都不建立隧道，执行完即退出
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "status":
			if len(os.Args) > 2 {
				path := strings.Replace(os.Args[2], "-config=", "", -1)
				client.GetTaskStatus(path)
			}
			return
		case "register":
			flag.CommandLine.Parse(os.Args[2:])
			client.RegisterLocalIp(*serverAddr, *verifyKey, *connType, *proxyUrl, *registerTime)
			return
		case "update":
			install.UpdateClient()
			return
		case "nat":
			c := stun.NewClient()
			flag.CommandLine.Parse(os.Args[2:])
			c.SetServerAddr(*stunAddr)
			nat, host, err := c.Discover()
			if err != nil || host == nil {
				logs.Error("get nat type error", err)
				return
			}
			fmt.Printf("nat type: %s \npublic address: %s\n", nat.String(), host.String())
			return
		}
	}

	run()
	// run() 只负责把连接 goroutine 拉起来就返回；主 goroutine 必须驻留，
	// 否则进程会立刻退出（原来靠 kardianos 的 s.Run() 阻塞，现在显式阻塞）。
	select {}
}

// firstEnv 按顺序返回第一个非空环境变量值。
// 新名优先，旧名（npc 时期）继续识别 —— 容器 / 编排里既有
// NPC_SERVER_ADDR / NPC_SERVER_VKEY 若被静默丢弃，表现是「服务起来了但连不上」，
// 属于最不该靠人肉排查的一类故障。
func firstEnv(env map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := env[k]; v != "" {
			return v
		}
	}
	return ""
}

func run() {
	common.InitPProfFromArg(*pprofAddr)
	//p2p or secret command
	if *password != "" {
		client.SetTlsEnable(*tlsEnable)
		client.SetTlsFingerprint(*tlsFingerprint)
		if *tlsEnable && *tlsStrict && *tlsFingerprint == "" {
			logs.Error("tls_strict=true 但未配置 tls_fingerprint，拒绝启动（F2-2）")
			os.Exit(0)
		}
		logs.Info("the version of client is %s, the core version of client is %s,tls enable is %t", version.VERSION, version.GetVersion(), client.GetTlsEnable())
		commonConfig := new(config.CommonConfig)
		commonConfig.Server = *serverAddr
		commonConfig.VKey = *verifyKey
		commonConfig.Tp = *connType
		localServer := new(config.LocalServer)
		localServer.Type = *localType
		localServer.Password = *password
		localServer.Target = *target
		localServer.Port = *localPort
		commonConfig.Client = new(file.Client)
		commonConfig.Client.Cnf = new(file.Config)
		go client.StartLocalServer(localServer, commonConfig)
		return
	}
	env := common.GetEnvMap()
	if *serverAddr == "" {
		*serverAddr = firstEnv(env, "NATPUNCH_SERVER_ADDR", "NPC_SERVER_ADDR")
	}
	if *verifyKey == "" {
		*verifyKey = firstEnv(env, "NATPUNCH_SERVER_VKEY", "NPC_SERVER_VKEY")
	}
	if *verifyKey != "" && *serverAddr != "" && *configPath == "" {
		client.SetTlsEnable(*tlsEnable)
		client.SetTlsFingerprint(*tlsFingerprint)
		if *tlsEnable && *tlsStrict && *tlsFingerprint == "" {
			logs.Error("tls_strict=true 但未配置 tls_fingerprint，拒绝启动（F2-2）")
			os.Exit(0)
		}
		logs.Info("the version of client is %s, the core version of client is %s,tls enable is %t", version.VERSION, version.GetVersion(), client.GetTlsEnable())

		vkeys := strings.Split(*verifyKey, `,`)
		for _, key := range vkeys {
			key := key
			go func() {
				for {
					logs.Info("start vkey:" + key)
					client.NewRPClient(*serverAddr, key, *connType, *proxyUrl, nil, *disconnectTime).Start()
					logs.Info("Client closed! It will be reconnected in five seconds")
					time.Sleep(time.Second * 5)
				}
			}()
		}

	} else {
		if *configPath == "" {
			*configPath = common.GetConfigPath()
		}

		// 判断路径下是否有配置文件
		if common.FileExists(*configPath) {
			logs.Info("配置文件模式启动")
			go client.StartFromFile(*configPath)
		} else {
			// 原来是 printSlogan()+inputCmd() 的交互式菜单。服务化部署下没有 stdin，
			// 进程会静静地卡在等输入上，表现为「启动了但什么都没干」。
			// 现在明确报错并以非零码退出，让 procd / systemd 的日志里能直接看到原因。
			logs.Error("未提供 -server/-vkey，且找不到配置文件 %s；请用 install.sh 安装，或显式传参启动", *configPath)
			os.Exit(1)
		}
	}
}
