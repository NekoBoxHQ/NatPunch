package common

import (
	"os"
	"path/filepath"
)

var ConfPath string

// GetRunPath 返回运行期使用的配置目录。
// 优先级：显式 -conf_path（ConfPath）> 约定的安装目录 > 可执行文件所在目录。
func GetRunPath() string {
	if ConfPath != "" {
		return ConfPath
	}

	if len(os.Args) == 1 {
		// 无额外参数（服务端由 procd/systemd 直接 exec）→ 用可执行文件所在目录
		dir, _ := filepath.Abs(filepath.Dir(os.Args[0]))
		return dir + "/"
	}
	if path := GetInstallPath(); !FileExists(path) {
		return GetAppPath()
	} else {
		return path
	}
}

// GetInstallPath 返回约定的安装目录。
// 服务端由 install_server.sh 装在 /opt/natpunch，客户端由 install.sh 装在 /usr/bin；
// 这里保留 /etc/natpunch 作为「配置集中存放」的约定位置。
func GetInstallPath() string {
	if ConfPath != "" {
		return ConfPath
	}
	return "/etc/natpunch"
}

// GetAppPath 返回当前可执行文件所在目录的绝对路径
func GetAppPath() string {
	if path, err := filepath.Abs(filepath.Dir(os.Args[0])); err == nil {
		return path
	}
	return os.Args[0]
}

// GetLogPath 服务端日志路径
func GetLogPath() string {
	return "/var/log/natpunch.log"
}

// GetClientLogPath 客户端日志路径
func GetClientLogPath() string {
	return "/var/log/natpunch-client.log"
}

// GetTmpPath 临时目录。OpenWrt 上 /tmp 是 tmpfs，重启即清空。
func GetTmpPath() string {
	return "/tmp"
}

// GetConfigPath 无 -server / -vkey 时回退查找的配置文件（相对当前工作目录）。
// 注意：服务化部署不会走到这里 —— install.sh 写的单元用的是显式参数。
func GetConfigPath() string {
	return "conf/natpunch.conf"
}
