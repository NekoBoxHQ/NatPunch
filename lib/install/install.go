package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/version"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jedisct1/go-minisign"
)

// repo 为发布与更新所指向的 GitHub 仓库（组织/仓库名）
const repo = "NekoBoxHQ/NatPunch"

// natpunchMinisignPubKey 发布方 minisign 公钥，须与 install.sh / install_server.sh 内嵌值一致。
const natpunchMinisignPubKey = "RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660"

// Keep it in sync with the template from service_sysv_linux.go file
// Use "ps | grep -v grep | grep $(get_pid)" because "ps PID" may not work on OpenWrt
const SysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{.Description}}
# processname: {{.Path}}
### BEGIN INIT INFO
# Provides:          {{.Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO
cmd="{{.Path}}{{range .Arguments}} {{.|cmd}}{{end}}"
name=$(basename $(readlink -f $0))
pid_file="/var/run/$name.pid"
stdout_log="/var/log/$name.log"
stderr_log="/var/log/$name.err"
[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name
get_pid() {
    cat "$pid_file"
}
is_running() {
    [ -f "$pid_file" ] && ps | grep -v grep | grep $(get_pid) > /dev/null 2>&1
}
case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if .WorkingDirectory}}cd '{{.WorkingDirectory}}'{{end}}
            $cmd >> "$stdout_log" 2>> "$stderr_log" &
            echo $! > "$pid_file"
            if ! is_running; then
                echo "Unable to start, see $stdout_log and $stderr_log"
                exit 1
            fi
        fi
    ;;
    stop)
        if is_running; then
            echo -n "Stopping $name.."
            kill $(get_pid)
            for i in $(seq 1 10)
            do
                if ! is_running; then
                    break
                fi
                echo -n "."
                sleep 1
            done
            echo
            if is_running; then
                echo "Not stopped; may still be shutting down or shutdown may have failed"
                exit 1
            else
                echo "Stopped"
                if [ -f "$pid_file" ]; then
                    rm "$pid_file"
                fi
            fi
        else
            echo "Not running"
        fi
    ;;
    restart)
        $0 stop
        if is_running; then
            echo "Unable to stop, will not attempt to start"
            exit 1
        fi
        $0 start
    ;;
    status)
        if is_running; then
            echo "Running"
        else
            echo "Stopped"
            exit 1
        fi
    ;;
    *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
`

const SystemdScript = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}} 
{{$dep}} {{end}}
[Service]
LimitNOFILE=65536
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:/var/log/{{.Name}}.out
StandardError=file:/var/log/{{.Name}}.err
{{- end}}
Restart=always
RestartSec=120
[Install]
WantedBy=multi-user.target
`

func UpdateNatpunch() {
	destPath, err := downloadLatest("server")
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	//复制文件到对应目录
	if _, err := copyStaticFile(destPath, "natpunch"); err != nil {
		log.Println("替换服务端文件失败：", err)
		return
	}
	fmt.Println("Update completed, please restart")
}

func UpdateNatpunchNew() {
	latest, err := fetchLatestVersion()
	if err != nil {
		log.Println("获取最新版本失败：", err)
		return
	}
	fmt.Println("最新版本为：", latest)
	if compareVersion(version.VERSION, latest) >= 0 {
		fmt.Println("当前已是最新版本，无需更新")
		return
	}
	tempDir := filepath.Join(common.GetAppPath(), "temp")
	destPath, err := downloadLatest2("server", tempDir)
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	//复制文件到对应目录
	if err := copyStaticFileReplaceNatpunch(destPath, common.GetAppPath()); err != nil {
		log.Println("替换服务端文件失败：", err)
		return
	}
	fmt.Println("更新成功，请重启服务")
}

func fetchLatestVersion() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	rl := new(release)
	if err := json.Unmarshal(b, rl); err != nil {
		return "", err
	}
	if rl.TagName == "" {
		return "", errors.New("无法解析最新版本号")
	}
	return rl.TagName, nil
}

// compareVersion 按数字段逐段比较语义版本（G7 修复，F2-8 前置）：
// "v1.10.0" > "v2.0.0" 依赖逐段比较，不能"去点拼接 Atoi"（1.10.0→1100 vs 2.0.0→200 会误判）。
func compareVersion(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func UpdateClient() {
	destPath, err := downloadLatest("client")
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	//复制文件到对应目录
	if _, err := copyStaticFile(destPath, "natpunch-client"); err != nil {
		log.Println("替换客户端文件失败：", err)
		return
	}
	fmt.Println("Update completed, please restart")
}

func UpdateClientNew() {
	latest, err := fetchLatestVersion()
	if err != nil {
		log.Println("获取最新版本失败：", err)
		return
	}
	fmt.Println("最新版本为：", latest)
	if compareVersion(version.VERSION, latest) >= 0 {
		fmt.Println("当前已是最新版本，无需更新")
		return
	}
	tempDir := filepath.Join(common.GetAppPath(), "temp")
	destPath, err := downloadLatest2("client", tempDir)
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	if err := copyStaticFileReplaceClient(destPath, common.GetAppPath()); err != nil {
		log.Println("替换客户端文件失败：", err)
		return
	}
	fmt.Println("更新成功，请重启客户端")
}

type release struct {
	TagName string `json:"tag_name"`
}

func downloadLatest(bin string) (string, error) {
	return downloadAndUnpack(bin, "")
}

func downloadLatest2(bin string, path string) (string, error) {
	return downloadAndUnpack(bin, path)
}

// downloadAndUnpack fetches the latest release package for the current OS/arch.
// Releases ship as .tar.gz (see build.assets.sh / release.yml).
// F2-8：强制校验 SHA256SUMS（同一 release 资产），校验失败即中止；解包弃用 unpackit，
// 改用标准库 archive/tar + gzip，并拒绝路径逃逸条目。
func downloadAndUnpack(bin, unpackPath string) (string, error) {
	data, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer data.Body.Close()
	if data.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取版本信息失败: HTTP %d", data.StatusCode)
	}
	b, err := io.ReadAll(data.Body)
	if err != nil {
		return "", err
	}
	rl := new(release)
	if err := json.Unmarshal(b, rl); err != nil {
		return "", err
	}
	if rl.TagName == "" {
		return "", errors.New("无法解析最新版本号")
	}
	ver := rl.TagName
	fmt.Println("the latest version is", ver)
	filename := runtime.GOOS + "_" + runtime.GOARCH + "_" + bin + ".tar.gz"
	downloadUrl := fmt.Sprintf("https://github.com/"+repo+"/releases/download/%s/%s", ver, filename)
	fmt.Println("download package from ", downloadUrl)

	// 强制 SHA256 校验（F2-8）：先取 SHA256SUMS，再下载并比对
	sumsRaw, err := fetchReleaseFile(ver, "SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("获取 SHA256SUMS 失败: %w", err)
	}
	// 签名校验：有签名则强制校验（与 shell 安装器对齐，见 verifyReleaseSignature）
	if err := verifyReleaseSignature(ver, sumsRaw); err != nil {
		return "", err
	}
	sums := parseSha256Sums(sumsRaw)
	want, ok := sums[filename]
	if !ok {
		return "", fmt.Errorf("SHA256SUMS 中未找到 %s 条目", filename)
	}
	resp, err := http.Get(downloadUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: HTTP %d %s", resp.StatusCode, downloadUrl)
	}
	buf := &bytes.Buffer{}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(buf, h), resp.Body); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return "", fmt.Errorf("SHA256 校验失败: 期望 %s 实际 %s", want, got)
	}
	fmt.Println("sha256 verified:", filename)

	destPath, err := extractTarGz(buf, unpackPath)
	if err != nil {
		return "", err
	}
	if bin == "server" {
		destPath = strings.Replace(destPath, "/web", "", -1)
		destPath = strings.Replace(destPath, `\web`, "", -1)
		destPath = strings.Replace(destPath, "/views", "", -1)
		destPath = strings.Replace(destPath, `\views`, "", -1)
	} else {
		destPath = strings.Replace(destPath, `\conf`, "", -1)
		destPath = strings.Replace(destPath, "/conf", "", -1)
	}
	return destPath, nil
}

// fetchReleaseFile 从指定 release 下载资产并返回内容
func fetchReleaseFile(ver, asset string) (string, error) {
	url := fmt.Sprintf("https://github.com/"+repo+"/releases/download/%s/%s", ver, asset)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// verifyReleaseSignature 校验 SHA256SUMS 的 minisign 签名。
//
// 与 shell 安装器策略保持一致（复评🟡：此前 Go 自更新只比对未签名的 SHA256SUMS，
// 等价于仅信任 HTTPS 传输，弱于 install.sh / install_server.sh 的 minisign 校验）：
//   - 发布方提供了 SHA256SUMS.minisig → 强制校验，失败即中止更新；
//   - 未提供签名文件（CI 未配置 MINISIGN_SECRET_KEY）→ 告警放行，此时仍有 SHA256 强制校验。
func verifyReleaseSignature(ver, sumsRaw string) error {
	sigRaw, err := fetchReleaseFile(ver, "SHA256SUMS.minisig")
	if err != nil {
		log.Printf("未获取到 SHA256SUMS.minisig，跳过签名校验（SHA256 已强制校验）: %v", err)
		return nil
	}
	pk, err := minisign.NewPublicKey(natpunchMinisignPubKey)
	if err != nil {
		return fmt.Errorf("内置 minisign 公钥解析失败: %w", err)
	}
	sig, err := minisign.DecodeSignature(sigRaw)
	if err != nil {
		return fmt.Errorf("SHA256SUMS.minisig 解析失败: %w", err)
	}
	ok, err := pk.Verify([]byte(sumsRaw), sig)
	if err != nil {
		return fmt.Errorf("minisign 签名校验失败: %w", err)
	}
	if !ok {
		return errors.New("minisign 签名与 SHA256SUMS 不匹配，已中止更新")
	}
	log.Println("minisign signature verified: SHA256SUMS")
	return nil
}

// parseSha256Sums 解析 sha256sum 格式："<hash>  <filename>"（忽略空行与 # 注释）
func parseSha256Sums(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 {
			m[filepath.Base(f[len(f)-1])] = strings.ToLower(f[0])
		}
	}
	return m
}

// extractTarGz 用标准库解包 tar.gz，逐条目拒绝绝对路径 / ".." 逃逸 / 符号链接（F2-8）。
// 单文件上限 512MB 防止恶意条目撑爆磁盘。
func extractTarGz(r io.Reader, dest string) (string, error) {
	if dest == "" {
		var err error
		dest, err = os.MkdirTemp("", "natpunch-update-")
		if err != nil {
			return "", err
		}
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	const maxFileSize = 512 << 20
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.Clean(hdr.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("tar 条目路径越界，已拒绝: %s", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if hdr.Size > maxFileSize {
				return "", fmt.Errorf("tar 条目过大: %s (%d bytes)", hdr.Name, hdr.Size)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777|0600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxFileSize+1)); err != nil {
				f.Close()
				return "", err
			}
			if err := f.Close(); err != nil {
				return "", err
			}
		default:
			// 符号链接 / 硬链接 / 设备文件一律拒绝（防路径逃逸与恶意条目）
			return "", fmt.Errorf("tar 含不受支持的条目类型 (%d)，已拒绝: %s", hdr.Typeflag, hdr.Name)
		}
	}
	return dest, nil
}

func copyStaticFile(srcPath, bin string) (string, error) {
	// natpunch web UI is embedded in the binary; no web/ files to copy.
	srcBin := filepath.Join(srcPath, bin)
	if common.IsWindows() {
		srcBin += ".exe"
	}
	if _, err := os.Stat(srcBin); err != nil {
		return "", fmt.Errorf("更新包中未找到可执行文件 %s: %w", srcBin, err)
	}
	var binPath string
	if !common.IsWindows() {
		if _, err := copyFile(srcBin, "/usr/bin/"+bin); err != nil {
			if _, err := copyFile(srcBin, "/usr/local/bin/"+bin); err != nil {
				return "", err
			}
			binPath = "/usr/local/bin/" + bin
		} else {
			binPath = "/usr/bin/" + bin
		}
	} else {
		destBin := filepath.Join(common.GetAppPath(), bin+".exe")
		if err := replaceExecutable(srcBin, destBin); err != nil {
			return "", err
		}
		binPath = destBin
	}
	chMod(binPath, 0755)
	return binPath, nil
}

func copyStaticFileReplaceNatpunch(srcPath, descPath string) error {
	// Web UI is embedded in the binary; only replace the executable.
	return replaceBinFromPackage(srcPath, descPath, "natpunch")
}

func copyStaticFileReplaceClient(srcPath, descPath string) error {
	return replaceBinFromPackage(srcPath, descPath, "natpunch-client")
}

func replaceBinFromPackage(srcPath, descPath, bin string) error {
	srcBin := filepath.Join(srcPath, bin)
	destBin := filepath.Join(descPath, bin)
	if common.IsWindows() {
		srcBin += ".exe"
		destBin += ".exe"
	}
	// Prefer replacing the actually running binary when its basename matches.
	if exe, err := os.Executable(); err == nil {
		if filepath.Base(exe) == filepath.Base(destBin) {
			destBin = exe
		}
	}
	if _, err := os.Stat(srcBin); err != nil {
		// unpackit may return a nested root dir; search one level if needed
		if found, findErr := findBinInDir(srcPath, filepath.Base(srcBin)); findErr == nil {
			srcBin = found
		} else {
			return fmt.Errorf("更新包中未找到可执行文件 %s: %w", srcBin, err)
		}
	}
	if err := replaceExecutable(srcBin, destBin); err != nil {
		return err
	}
	chMod(destBin, 0755)
	// Clean temp package; keep parent temp dir if still in use
	_ = os.RemoveAll(srcPath)
	return nil
}

func findBinInDir(root, name string) (string, error) {
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return err
		}
		if info.Name() == name {
			found = path
			return errors.New("found")
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	if err != nil {
		return "", err
	}
	return "", os.ErrNotExist
}

// replaceExecutable places srcBin at destBin. On Windows a running executable
// cannot be overwritten, but it can be renamed aside first.
func replaceExecutable(srcBin, destBin string) error {
	if _, err := os.Stat(srcBin); err != nil {
		return fmt.Errorf("源文件不存在: %s: %w", srcBin, err)
	}
	if dstFi, err := os.Stat(destBin); err == nil {
		if srcFi, err := os.Stat(srcBin); err == nil {
			if os.SameFile(srcFi, dstFi) {
				return nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(destBin), 0755); err != nil {
		return err
	}

	// Move the current binary out of the way when present (required on Windows
	// while the process is still running).
	if _, err := os.Stat(destBin); err == nil {
		bak := destBin + ".old"
		_ = os.Remove(bak)
		if err := os.Rename(destBin, bak); err != nil {
			return fmt.Errorf("无法备份当前程序 %s: %w", destBin, err)
		}
	}

	// Same filesystem: rename is atomic. Fall back to copy across volumes.
	if err := os.Rename(srcBin, destBin); err != nil {
		if _, copyErr := copyFile(srcBin, destBin); copyErr != nil {
			// Best-effort restore of previous binary
			bak := destBin + ".old"
			if _, statErr := os.Stat(bak); statErr == nil {
				_ = os.Rename(bak, destBin)
			}
			return fmt.Errorf("替换可执行文件失败: %w", copyErr)
		}
		_ = os.Remove(srcBin)
	}
	return nil
}

func InstallClient() {
	path := common.GetInstallPath()
	if !common.FileExists(path) {
		err := os.Mkdir(path, 0755)
		if err != nil {
			log.Fatal(err)
		}
	}
	if _, err := copyStaticFile(common.GetAppPath(), "natpunch-client"); err != nil {
		log.Fatalln(err)
	}
}

func InstallNatpunch() string {
	path := common.GetInstallPath()
	log.Println("install path:" + path)
	if !common.FileExists(path) {
		MkidrDirAll(path, "conf")
		// not copy config if the config file is exist
		if err := CopyDir(filepath.Join(common.GetAppPath(), "conf"), filepath.Join(path, "conf")); err != nil {
			log.Fatalln(err)
		}
		chMod(filepath.Join(path, "conf"), 0755)
	}
	binPath, err := copyStaticFile(common.GetAppPath(), "natpunch")
	if err != nil {
		log.Fatalln(err)
	}
	log.Println("install ok!")
	log.Println("Web UI is embedded in the natpunch binary; no web/ directory is required")
	log.Println("The new configuration file is located in", path, "you can edit them")
	if !common.IsWindows() {
		log.Println(`You can start with:
natpunch start|stop|restart|uninstall|update
anywhere!`)
	} else {
		log.Println(`You can copy executable files to any directory and start working with:
natpunch.exe start|stop|restart|uninstall|update
now!`)
	}
	chMod(common.GetLogPath(), 0640)
	return binPath
}

func InstallNatpunchToCurrentDir() string {
	path := common.GetAppPath()
	log.Println("install path:" + path)
	log.Println("install ok!")
	chMod(filepath.Join(path, "natpunch.log"), 0640)

	if !common.IsWindows() {
		path = filepath.Join(path, "natpunch")
	} else {
		path = filepath.Join(path, "natpunch.exe")
	}
	return path
}

func MkidrDirAll(path string, v ...string) {
	for _, item := range v {
		if err := os.MkdirAll(filepath.Join(path, item), 0755); err != nil {
			log.Fatalf("Failed to create directory %s error:%s", path, err.Error())
		}
	}
}

func CopyDir(srcPath string, destPath string) error {
	//检测目录正确性
	if srcInfo, err := os.Stat(srcPath); err != nil {
		fmt.Println(err.Error())
		return err
	} else {
		if !srcInfo.IsDir() {
			e := errors.New("SrcPath is not the right directory!")
			return e
		}
	}
	if destInfo, err := os.Stat(destPath); err != nil {
		return err
	} else {
		if !destInfo.IsDir() {
			e := errors.New("DestInfo is not the right directory!")
			return e
		}
	}
	err := filepath.Walk(srcPath, func(path string, f os.FileInfo, err error) error {
		if f == nil {
			return err
		}
		if !f.IsDir() {
			destNewPath := strings.Replace(path, srcPath, destPath, -1)
			log.Println("copy file ::" + path + " to " + destNewPath)
			copyFile(path, destNewPath)
			if !common.IsWindows() {
				// 拷贝的配置文件含 vkey/web_password 等敏感项：0640（组可读），不再 0766 全局可写（阶段三 #12）
				chMod(destNewPath, 0640)
			}
		}
		return nil
	})
	return err
}

// 生成目录并拷贝文件
func copyFile(src, dest string) (w int64, err error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return
	}
	defer srcFile.Close()
	//分割path目录
	destSplitPathDirs := strings.Split(dest, string(filepath.Separator))

	//检测时候存在目录
	destSplitPath := ""
	for index, dir := range destSplitPathDirs {
		if index < len(destSplitPathDirs)-1 {
			destSplitPath = destSplitPath + dir + string(filepath.Separator)
			b, _ := pathExists(destSplitPath)
			if b == false {
				log.Println("mkdir:" + destSplitPath)
				//创建目录
				err := os.Mkdir(destSplitPath, os.ModePerm)
				if err != nil {
					log.Fatalln(err)
				}
			}
		}
	}
	dstFile, err := os.Create(dest)
	if err != nil {
		return
	}
	defer dstFile.Close()

	return io.Copy(dstFile, srcFile)
}

// 检测文件夹路径时候存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func chMod(name string, mode os.FileMode) {
	if !common.IsWindows() {
		os.Chmod(name, mode)
	}
}
