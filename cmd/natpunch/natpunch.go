package main

import (
	"bufio"
	"github.com/NekoBoxHQ/NatPunch/bridge"
	"github.com/NekoBoxHQ/NatPunch/lib/daemon"
	"github.com/NekoBoxHQ/NatPunch/server"
	"flag"
	"fmt"
	"github.com/fatih/color"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/install"
	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/NekoBoxHQ/NatPunch/server/connection"
	"github.com/NekoBoxHQ/NatPunch/server/proxy"
	"github.com/NekoBoxHQ/NatPunch/server/tool"
	"github.com/NekoBoxHQ/NatPunch/web/routers"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"

	"github.com/kardianos/service"
)

var (
	level      string
	ver        = flag.Bool("version", false, "show current version")
	confPath   = flag.String("conf_path", "", "set current confPath")
	serverCmd  = flag.Bool("server", false, "NatPunch管理脚本")
	natpunchLogPath = flag.String("log_path", "", "natpunch log path")
)

// isServiceCommand 判断是否服务管理命令（这些命令不参与单实例保护）
func isServiceCommand(args []string) bool {
	for _, v := range args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart", "service", "reload", "update":
			return true
		}
	}
	return false
}

func main() {

	debug.SetMaxThreads(1000000)

	flag.Parse()
	// init log
	if *ver {
		common.PrintVersion()
		return
	}
	if *serverCmd {
		_ = logs.SetLogger(logs.AdapterConsole, `{"level":7,"color":true}`)
		printSlogan()
		inputCmd()
		return
	}
	// *confPath why get null value ?
	var logPath string
	for _, v := range os.Args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		if strings.Contains(v, "-conf_path=") {
			common.ConfPath = strings.Replace(v, "-conf_path=", "", -1)
		}

		if strings.Contains(v, "-log_path=") {
			logPath = strings.Replace(v, "-log_path=", "", -1)
		}
	}

	// 单实例保护：直接运行服务（非 install/start/stop/restart/service 管理命令）时，
	// 若已有实例在跑则退出，防止双进程各自持内存互写 clients.json 导致 vkey 丢失。
	// 占用改为 O_CREATE|O_EXCL 原子创建，消除原"先读后写"的 TOCTOU 竞态（复评🟡）；
	// pid 文件保留不删，避免 unlink 与接管之间的竞态（详见 common.OwnPidFile 注释）。
	if !isServiceCommand(os.Args) {
		pidFile := filepath.Join(common.GetRunPath(), "natpunch.pid")
		if err := common.OwnPidFile(pidFile); err != nil {
			fmt.Printf("NatPunch 已在运行或无法占用 pid 文件（%v），本实例退出（单实例保护）\n", err)
			os.Exit(0)
		}
	}

	// auto-generate default config files if not exist
	initConfig(filepath.Join(common.GetRunPath(), "conf"))

	if err := beego.LoadAppConfig("ini", filepath.Join(common.GetRunPath(), "conf", "natpunch.conf")); err != nil {
		log.Fatalln("load config file error", err.Error())
	}

	// 初始化数据层上限（复评🟠6：上限校验下沉 NewClient/NewTask，所有入口统一生效）
	file.MaxClients = beego.AppConfig.DefaultInt("max_clients", 0)
	file.MaxTunnelsPerClient = beego.AppConfig.DefaultInt("max_tunnels_per_client", 0)

	// 写队列上限注入（复评🟠7：默认 16384 包≈64MB，0=不限）
	if v := beego.AppConfig.DefaultInt64("mux_write_queue_max", 16384); v >= 0 {
		natpunch_mux.WriteQueueMax = v
	}

	common.InitPProfFromFile()
	if level = beego.AppConfig.String("log_level"); level == "" {
		level = "7"
	}
	logs.Reset()
	logs.EnableFuncCallDepth(true)
	logs.SetLogFuncCallDepth(3)

	if logPath == "" {
		logPath = beego.AppConfig.String("log_path")
		if logPath == "" {
			logPath = common.GetLogPath()
		}
		if common.IsWindows() {
			logPath = strings.Replace(logPath, "\\", "\\\\", -1)
		}
	}

	// init service
	options := make(service.KeyValue)
	svcConfig := &service.Config{
		Name:        "NatPunch",
		DisplayName: "NatPunch 内网穿透代理服务器",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}

	bridge.ServerTlsEnable = beego.AppConfig.DefaultBool("tls_enable", false)

	for _, v := range os.Args[1:] {
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		svcConfig.Arguments = append(svcConfig.Arguments, v)
	}

	svcConfig.Arguments = append(svcConfig.Arguments, "service")
	if len(os.Args) > 1 && os.Args[1] == "service" {
		_ = logs.SetLogger(logs.AdapterFile, `{"level":`+level+`,"filename":"`+logPath+`","daily":false,"maxlines":100000,"color":true}`)
	} else {
		_ = logs.SetLogger(logs.AdapterConsole, `{"level":`+level+`,"color":true}`)
	}
	if !common.IsWindows() {
		svcConfig.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"}
		svcConfig.Option["SystemdScript"] = install.SystemdScript
		svcConfig.Option["SysvScript"] = install.SysvScript
	}
	prg := &natpunch{}
	prg.exit = make(chan struct{})
	s, err := service.New(prg, svcConfig)
	if err != nil {
		logs.Error(err, "service function disabled")
		run()
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}

	if len(os.Args) > 1 && os.Args[1] != "service" {
		switch os.Args[1] {
		case "reload":
			daemon.InitDaemon("natpunch", common.GetRunPath(), common.GetTmpPath())
			return
		case "install":
			// uninstall before
			_ = service.Control(s, "stop")
			_ = service.Control(s, "uninstall")

			binPath := install.InstallNatpunch()
			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				logs.Error(err)
				return
			}
			err = service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}
			return
		case "start", "restart", "stop":
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				cmd := exec.Command("/etc/init.d/"+svcConfig.Name, os.Args[1])
				err := cmd.Run()
				if err != nil {
					logs.Error(err)
				}
				return
			}
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			return
		case "uninstall":
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				os.Remove("/etc/rc.d/K02" + svcConfig.Name)
			}
			return
		case "update":
			install.UpdateNatpunch()
			return
			//default:
			//	logs.Error("command is not support")
			//	return
		}
	}

	_ = s.Run()
}

func printSlogan() {
	green := color.New(color.FgGreen).SprintFunc()
	// 第一次输入，如果输入 1,2,3，4 则需要输入秘钥，否则

	fmt.Printf("%s", green(""))

	fmt.Printf("\033[32;0m欢迎使用 NatPunch 管理脚本，当前版本：v%s\n", version.VERSION)
	fmt.Printf("\033[0m") // 重置颜色

	fmt.Printf("\n")

	fmt.Printf("\u001B[32m输入[1]\u001B[0m - 安装 NatPunch\n")
	fmt.Printf("\u001B[32m输入[2]\u001B[0m - 卸载 NatPunch\n")
	fmt.Printf("\u001B[32m输入[3]\u001B[0m - 更新 NatPunch\n")
	fmt.Printf("---------------------\n")
	fmt.Printf("\u001B[32m输入[4]\u001B[0m - 查看状态\n")
	fmt.Printf("---------------------\n")
	fmt.Printf("\u001B[32m输入[5]\u001B[0m - 启动 NatPunch\n")
	fmt.Printf("\u001B[32m输入[6]\u001B[0m - 停止 NatPunch\n")
	fmt.Printf("\u001B[32m输入[7]\u001B[0m - 重启 NatPunch\n")
	fmt.Printf("---------------------\n")
	fmt.Printf("\u001B[32m输入[0]\u001B[0m - 退出脚本\n")
	fmt.Printf("---------------------\n")
	fmt.Printf("\n")

}

func inputCmd() {
	var flag string
	fmt.Printf("请输入[0-7]：")

	stdin := bufio.NewReader(os.Stdin)
	_, err := fmt.Fscanln(stdin, &flag)
	if err != nil {
		fmt.Println("输入有误")
	} else {
		if flag == "0" {
			os.Exit(0)
		}

		// init service

		prg := &natpunch{
			exit: make(chan struct{}),
		}
		options := make(service.KeyValue)
		svcConfig := &service.Config{
			Name:        "NatPunch",
			DisplayName: "NatPunch 内网穿透代理服务器",
			Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
			Option:      options,
		}
		s, _ := service.New(prg, svcConfig)

		switch flag {
		case "1":
			// uninstall before
			_ = service.Control(s, "stop")
			_ = service.Control(s, "uninstall")
			binPath := install.InstallNatpunchToCurrentDir()

			// Ensure conf exists (same as normal startup), then load it for display.
			// Previously LoadAppConfig error was ignored and web_port could be empty,
			// printing "127.0.0.1:" with no port. See #317.
			confDir := filepath.Join(common.GetAppPath(), "conf")
			initConfig(confDir)
			if err := beego.LoadAppConfig("ini", filepath.Join(confDir, "natpunch.conf")); err != nil {
				fmt.Println("加载配置文件失败：", err)
				break
			}

			logPath := filepath.Join(common.GetAppPath(), "natpunch.log")
			if common.IsWindows() {
				logPath = strings.Replace(logPath, "\\", "\\\\", -1)
			}
			svcConfig.Arguments = append(svcConfig.Arguments, "service")
			svcConfig.Arguments = append(svcConfig.Arguments, "-conf_path="+common.GetAppPath())
			svcConfig.Arguments = append(svcConfig.Arguments, "-log_path="+logPath)

			fmt.Println("日志文件路径为：", logPath)

			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				logs.Error("创建服务失败: %v", err)
				return
			}

			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}

			err = service.Control(s, "install")
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			} else {
				fmt.Println("NatPunch服务安装成功")
			}

			err = service.Control(s, "start")
			if err != nil {
				fmt.Println("启动NatPunch服务失败", err)
			} else {
				webPort := beego.AppConfig.String("web_port")
				if webPort == "" {
					fmt.Println("NatPunch服务已启动（未配置 web_port，管理面板已关闭）")
				} else {
					scheme := "http"
					if beego.AppConfig.DefaultBool("web_open_ssl", false) {
						scheme = "https"
					}
					fmt.Println("NatPunch服务已启动，管理面板访问地址：" + scheme + "://127.0.0.1:" + webPort)
				}
			}

			break
		case "2":
			// 卸载系统服务
			err := service.Control(s, "stop")
			if err != nil {
				fmt.Println("NatPunch服务停止失败", err)
			} else {
				fmt.Println("NatPunch服务已停止")
			}

			err = service.Control(s, "uninstall")
			if err != nil {
				logs.Error("NatPunch服务卸载失败")
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				os.Remove("/etc/rc.d/K02" + svcConfig.Name)
			}

			if err == nil {
				fmt.Println("NatPunch服务已卸载成功")
			}
			break
		case "3":
			install.UpdateNatpunchNew()
			break
		case "4":
			// 查看状态
			var statusMsg = ""
			status, err := s.Status()
			if err != nil {
				statusMsg = "\u001B[31m未运行\u001B[0m"
			} else {
				if status == 1 {
					statusMsg = "\u001B[32m运行中\u001B[0m"
				} else {
					statusMsg = "\u001B[31m未运行\u001B[0m"
				}
			}
			fmt.Println("NatPunch服务状态：" + statusMsg)
			break
		case "5":
			// 启动 NatPunch
			err := service.Control(s, "start")
			if err != nil {
				fmt.Println("NatPunch服务启动失败", err)
			} else {
				fmt.Println("NatPunch服务启动成功")
			}

			break
		case "6":
			// 停止 NatPunch
			err := service.Control(s, "stop")
			if err != nil {
				fmt.Println("NatPunch服务停止失败", err)
			} else {
				fmt.Println("NatPunch服务停止成功")
			}

			break
		case "7":
			// 重启 NatPunch
			err := service.Control(s, "restart")
			if err != nil {
				fmt.Println("NatPunch服务重启失败", err)
			} else {
				fmt.Println("NatPunch服务重启成功")
			}

			break
		}
	}

	inputCmd()
}

type natpunch struct {
	exit chan struct{}
}

func (p *natpunch) Start(s service.Service) error {
	_, _ = s.Status()
	go p.run()
	return nil
}
func (p *natpunch) Stop(s service.Service) error {
	_, _ = s.Status()
	close(p.exit)
	if service.Interactive() {
		os.Exit(0)
	}
	return nil
}

func (p *natpunch) run() error {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logs.Warning("natpunch: panic serving %v: %v\n%s", err, string(buf))
		}
	}()
	run()
	select {
	case <-p.exit:
		logs.Warning("stop...")
	}
	return nil
}

func run() {
	routers.Init()
	task := &file.Tunnel{
		Mode: "webServer",
	}
	bridgePort, err := beego.AppConfig.Int("bridge_port")
	if err != nil {
		logs.Error("Getting bridge_port error", err)
		os.Exit(0)
	}

	logs.Info("日志路径：" + *natpunchLogPath)
	logs.Info("the config path is:" + common.GetRunPath())
	logs.Info("the version of server is %s ,allow client core version to be %s,tls enable is %t", version.VERSION, version.GetVersion(), bridge.ServerTlsEnable)
	connection.InitConnectionService()
	// 桥接证书持久化到 conf/bridge.pem|key（0600），与面板 HTTPS 证书（server.pem|key）路径隔离（F2-2）
	if err := crypt.InitTls(filepath.Join(common.GetRunPath(), "conf", "bridge.pem"), filepath.Join(common.GetRunPath(), "conf", "bridge.key")); err != nil {
		logs.Error("init bridge tls cert error: %v", err)
	}
	tool.InitAllowPort()
	tool.StartSystemInfo()
	timeout, err := beego.AppConfig.Int("disconnect_timeout")
	if err != nil {
		timeout = 60
	}
	// 全局连接数上限（阶段三 #4，0=不限）
	if maxConn, err := beego.AppConfig.Int64("max_global_conn"); err == nil {
		proxy.SetMaxGlobalConn(maxConn)
	}
	go server.StartNewServer(bridgePort, task, beego.AppConfig.String("bridge_type"), timeout)
}

func initConfig(confDir string) {
	if !common.FileExists(confDir) {
		os.MkdirAll(confDir, 0755)
	}
	confPath := filepath.Join(confDir, "natpunch.conf")
	if !common.FileExists(confPath) {
		webPassword := crypt.GetRandomString(8)
		publicVkey := crypt.GetRandomString(16) // 公共密钥随机化，避免写死 123（F1-8）
		content := strings.Replace(defaultNatPunchConf, "web_password=123", "web_password="+webPassword, 1)
		content = strings.Replace(content, "public_vkey=123", "public_vkey="+publicVkey, 1)
		f, err := os.Create(confPath)
		if err != nil {
			return
		}
		defer f.Close()
		f.WriteString(content)
		logs.Info("Auto-generated default config file:", confPath)
		logs.Info("Web login username: admin, password:", webPassword)
	}
}

const defaultNatPunchConf = `http_proxy_ip=0.0.0.0
http_proxy_port=0
https_proxy_port=0
show_http_proxy_port=true

bridge_type=tcp
bridge_port=8024
bridge_ip=0.0.0.0

public_vkey=123

# 客户端/隧道数量上限（0=不限，沿用存量语义；推荐 max_clients=100、max_tunnels_per_client=20，见文档）
max_clients=0
max_tunnels_per_client=0

# mux 写队列排队上限（包数，0=不限；默认 16384 约 64MB，千兆级大流量下防批量掉线）
mux_write_queue_max=16384

# 全局并发连接数上限（0=不限，沿用存量语义；推荐 10000，见文档）
max_global_conn=0

flow_store_interval=1

log_level=6
log_path=natpunch.log

web_host=a.o.com
web_username=admin
web_password=123
web_port = 8081
web_ip=0.0.0.0
web_base_url=
web_open_ssl=false
web_cert_file=conf/server.pem
web_key_file=conf/server.key

allow_user_login=true
allow_user_register=false
allow_user_change_username=true

allow_flow_limit=true
allow_rate_limit=true
allow_tunnel_num_limit=true
allow_local_proxy=false
allow_connection_num_limit=true
allow_multi_ip=true
system_info_display=true

http_add_origin_header=true

http_cache=false
http_cache_length=100

disconnect_timeout=60

open_captcha=false

tls_enable=true
tls_bridge_port=8025
`
