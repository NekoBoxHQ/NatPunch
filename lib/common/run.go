package common

import (
	"os"
	"path/filepath"
	"runtime"
)

var ConfPath string

// Get the currently selected configuration file directory
// For non-Windows systems, select the /etc/natpunch as config directory if exist, or select ./
// windows system, select the C:\Program Files\NatPunch as config directory if exist, or select ./
func GetRunPath() string {
	if ConfPath != "" {
		return ConfPath
	}

	var path string
	if len(os.Args) == 1 {
		if !IsWindows() {
			dir, _ := filepath.Abs(filepath.Dir(os.Args[0])) //返回
			return dir + "/"
		} else {
			return "./"
		}
	} else {
		if path = GetInstallPath(); !FileExists(path) {
			return GetAppPath()
		}
	}
	return path
}

// Different systems get different installation paths
func GetInstallPath() string {
	var path string

	if ConfPath != "" {
		return ConfPath
	}

	if IsWindows() {
		path = `C:\Program Files\NatPunch`
	} else {
		path = "/etc/natpunch"
	}

	return path
}

// Get the absolute path to the running directory
func GetAppPath() string {
	if path, err := filepath.Abs(filepath.Dir(os.Args[0])); err == nil {
		return path
	}
	return os.Args[0]
}

// Determine whether the current system is a Windows system?
func IsWindows() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return false
}

// interface log file path
func GetLogPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "natpunch.log")
	} else {
		path = "/var/log/natpunch.log"
	}
	return path
}

func GetLogPathCurrentPath() string {
	var path string
	path = filepath.Join(GetAppPath(), "natpunch.log")
	return path
}

// interface natpunch-client log file path
func GetClientLogPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "natpunch-client.log")
	} else {
		path = "/var/log/natpunch-client.log"
	}
	return path
}

// interface pid file path
func GetTmpPath() string {
	var path string
	if IsWindows() {
		path = GetAppPath()
	} else {
		path = "/tmp"
	}
	return path
}

// config file path
func GetConfigPath() string {
	var path string
	if IsWindows() {
		path = filepath.Join(GetAppPath(), "conf/natpunch.conf")
	} else {
		path = "conf/natpunch.conf"
	}
	return path
}
