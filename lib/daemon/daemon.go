package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
)

func InitDaemon(f string, runPath string, pidPath string) {
	if len(os.Args) < 2 {
		return
	}
	var args []string
	args = append(args, os.Args[0])
	if len(os.Args) >= 2 {
		args = append(args, os.Args[2:]...)
	}
	args = append(args, "-log=file")
	switch os.Args[1] {
	case "start":
		start(args, f, pidPath, runPath)
		os.Exit(0)
	case "stop":
		stop(f, args[0], pidPath)
		os.Exit(0)
	case "restart":
		stop(f, args[0], pidPath)
		start(args, f, pidPath, runPath)
		os.Exit(0)
	case "status":
		if status(f, pidPath) {
			log.Printf("%s is running", f)
		} else {
			log.Printf("%s is not running", f)
		}
		os.Exit(0)
	case "reload":
		reload(f, pidPath)
		os.Exit(0)
	}
}

// readPidFile 统一读取并校验 pid 文件：TrimSpace → Atoi → 正整数校验。
// 之后的所有 kill/status 均以纯数字参数直传 exec（不经 shell 拼接），杜绝命令注入（阶段三 #14）。
func readPidFile(pidPath, f string) (int, error) {
	b, err := os.ReadFile(filepath.Join(pidPath, f+".pid"))
	if err != nil {
		return 0, fmt.Errorf("pid file does not exist: %w", err)
	}
	s := strings.TrimSpace(string(b))
	pid, err := strconv.Atoi(s)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file content: %q", s)
	}
	return pid, nil
}

// killByPid 以参数数组直传 kill 命令（不经 shell），pid 已由 readPidFile 校验为纯数字。
func killByPid(pid int, sig string) error {
	return exec.Command("kill", sig, strconv.Itoa(pid)).Run()
}

func reload(f string, pidPath string) {
	if f == "natpunch" && !common.IsWindows() && !status(f, pidPath) {
		log.Println("reload fail")
		return
	}
	if common.IsWindows() {
		log.Fatalln("reload is not supported on windows")
	}
	pid, err := readPidFile(pidPath, f)
	if err != nil {
		log.Fatalln("reload error,", err)
	}
	if killByPid(pid, "-30") == nil {
		log.Println("reload success")
	} else {
		log.Println("reload fail")
	}
}

func status(f string, pidPath string) bool {
	if common.IsWindows() {
		b, err := os.ReadFile(filepath.Join(pidPath, f+".pid"))
		if err != nil {
			return false
		}
		out, _ := exec.Command("tasklist").Output()
		if strings.Index(string(out), strings.TrimSpace(string(b))) > -1 {
			return true
		}
		return false
	}
	pid, err := readPidFile(pidPath, f)
	if err != nil {
		return false
	}
	// kill -0 仅探测进程是否存在，不发送信号
	return killByPid(pid, "-0") == nil
}

func start(osArgs []string, f string, pidPath, runPath string) {
	if status(f, pidPath) {
		log.Printf(" %s is running", f)
		return
	}
	cmd := exec.Command(osArgs[0], osArgs[1:]...)
	cmd.Start()
	if cmd.Process.Pid > 0 {
		log.Println("start ok , pid:", cmd.Process.Pid, "config path:", runPath)
		d1 := []byte(strconv.Itoa(cmd.Process.Pid))
		os.WriteFile(filepath.Join(pidPath, f+".pid"), d1, 0600)
	} else {
		log.Println("start error")
	}
}

func stop(f string, p string, pidPath string) {
	if !status(f, pidPath) {
		log.Printf(" %s is not running", f)
		return
	}
	var c *exec.Cmd
	var err error
	if common.IsWindows() {
		p := strings.Split(p, `\`)
		c = exec.Command("taskkill", "/F", "/IM", p[len(p)-1])
	} else {
		pid, err := readPidFile(pidPath, f)
		if err != nil {
			log.Fatalln("stop error,", err)
		}
		err = killByPid(pid, "-9")
		if err != nil {
			log.Println("stop error,", err)
		} else {
			log.Println("stop ok")
		}
		return
	}
	err = c.Run()
	if err != nil {
		log.Println("stop error,", err)
	} else {
		log.Println("stop ok")
	}
}
