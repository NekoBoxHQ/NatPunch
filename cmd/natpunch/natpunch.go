package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/NekoBoxHQ/NatPunch/bridge"
	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/crypt"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/install"
	"github.com/NekoBoxHQ/NatPunch/lib/natpunch_mux"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"github.com/NekoBoxHQ/NatPunch/server"
	"github.com/NekoBoxHQ/NatPunch/server/connection"
	"github.com/NekoBoxHQ/NatPunch/server/proxy"
	"github.com/NekoBoxHQ/NatPunch/server/tool"
	"github.com/NekoBoxHQ/NatPunch/web/routers"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

var (
	level           string
	ver             = flag.Bool("version", false, "show current version")
	confPath        = flag.String("conf_path", "", "set current confPath")
	serverCmd       = flag.Bool("server", false, "NatPunch管理脚本")
	natpunchLogPath = flag.String("log_path", "", "natpunch log path")
)

// isUpdateCommand 判断是否为 update 子命令。
// 这类命令要能在服务正在运行时执行，因此不参与单实例保护。
func isUpdateCommand(args []string) bool {
	for _, v := range args[1:] {
		if v == "update" {
			return true
		}
	}
	return false
}

// 服务端是「由安装器拉起的常驻进程」：开机自启与守护由 install_server.sh 写的
// procd / systemd 单元负责，升级由重跑安装脚本负责。二进制自身不做服务管理，
// 只有 -version（安装器用来比对版本）与 update（就地替换二进制）两个辅助入口。
func main() {
	debug.SetMaxThreads(1000000)

	flag.Parse()
	if *ver {
		common.PrintVersion()
		return
	}

	// *confPath 从 flag 包取到的是空值，这里继续按原方式从 argv 里手工提取。
	var logPath string
	for _, v := range os.Args[1:] {
		if strings.Contains(v, "-conf_path=") {
			common.ConfPath = strings.Replace(v, "-conf_path=", "", -1)
		}
		if strings.Contains(v, "-log_path=") {
			logPath = strings.Replace(v, "-log_path=", "", -1)
		}
	}

	// 单实例保护：若已有实例在跑则退出，防止双进程各自持内存互写 clients.json 导致 vkey 丢失。
	// 占用改为 O_CREATE|O_EXCL 原子创建，消除原“先读后写”的 TOCTOU 竞态；
	// pid 文件保留不删，避免 unlink 与接管之间的竞态（详见 common.OwnPidFile 注释）。
	if !isUpdateCommand(os.Args) {
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

	// 初始化数据层上限（上限校验下沉 NewClient/NewTask，所有入口统一生效）
	file.MaxClients = beego.AppConfig.DefaultInt("max_clients", 0)
	file.MaxTunnelsPerClient = beego.AppConfig.DefaultInt("max_tunnels_per_client", 0)

	// 写队列上限注入（默认 16384 包≈64MB，0=不限）
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
	}
	*natpunchLogPath = logPath
	// 常驻进程由 procd / systemd 托管，日志走控制台由服务管理器收集
	// （install_server.sh 的 procd 配置了 stdout/stderr 转发）。
	_ = logs.SetLogger(logs.AdapterConsole, `{"level":`+level+`,"color":true}`)

	// update 子命令：只替换二进制，不进入服务主循环
	if len(os.Args) > 1 && os.Args[1] == "update" {
		install.UpdateNatpunch()
		return
	}

	run()
	// run() 把服务端 goroutine 拉起来就返回，主 goroutine 必须驻留。
	select {}
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
	// 全局连接数上限（0=不限）
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
