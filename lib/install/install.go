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
	"time"

	"github.com/jedisct1/go-minisign"
)

// httpClient 带超时的 HTTP 客户端。更新路径必须能在网络半死时退出：
// 原来直接用 http.Get（走 DefaultClient，无任何超时），一个卡住的连接会让
// “更新客户端”永久挂起，既没有报错也无法中断。
var httpClient = &http.Client{Timeout: 60 * time.Second}

// repo 为发布与更新所指向的 GitHub 仓库（组织/仓库名）
const repo = "NekoBoxHQ/NatPunch"

// natpunchMinisignPubKey 发布方 minisign 公钥，须与 install.sh / install_server.sh 内嵌值一致。
const natpunchMinisignPubKey = "RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660"

func UpdateNatpunch() {
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
	destPath, err := downloadLatest("server", tempDir)
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	//复制文件到对应目录
	if err := copyStaticFileReplaceNatpunch(destPath, common.GetAppPath()); err != nil {
		log.Println("替换服务端文件失败：", err)
		return
	}
	// 换完只是磁盘上的文件变了，跑着的服务内存里还是旧代码 —— 必须重启才生效。
	// 这一步交给脱离会话的独立进程做，原因见 restart.go。
	restartServiceDetached("natpunch", "natpunch")
}

func fetchLatestVersion() (string, error) {
	resp, err := httpClient.Get("https://api.github.com/repos/" + repo + "/releases/latest")
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
	destPath, err := downloadLatest("client", tempDir)
	if err != nil {
		log.Println("下载更新失败：", err)
		return
	}
	if err := copyStaticFileReplaceClient(destPath, common.GetAppPath()); err != nil {
		log.Println("替换客户端文件失败：", err)
		return
	}
	// 同 UpdateNatpunch：换完必须重启才生效，交给脱离会话的独立进程做。
	restartServiceDetached("natpunch-client", "natpunch-client")
}

type release struct {
	TagName string `json:"tag_name"`
}

func downloadLatest(bin string, path string) (string, error) {
	return downloadAndUnpack(bin, path)
}

// downloadAndUnpack fetches the latest release package for the current OS/arch.
// Releases ship as .tar.gz (see build.assets.sh / release.yml).
// F2-8：强制校验 SHA256SUMS（同一 release 资产），校验失败即中止；解包弃用 unpackit，
// 改用标准库 archive/tar + gzip，并拒绝路径逃逸条目。
func downloadAndUnpack(bin, unpackPath string) (string, error) {
	data, err := httpClient.Get("https://api.github.com/repos/" + repo + "/releases/latest")
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
	resp, err := httpClient.Get(downloadUrl)
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
	resp, err := httpClient.Get(url)
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
//   - 拿不到签名文件 → 同样中止。
//
// 最后一条原来是告警放行，但那是个降级口子：SHA256 是对**同一渠道拿到的**
// SHA256SUMS 做的自洽比对，攻击者控制发布渠道时只要不上传 .minisig，
// 就能让整条签名链失效、转而信任自己提供的清单。发布流水线（release.yml）
// 已强制要求上传 SHA256SUMS.minisig，因此这里改为 fail-closed。
func verifyReleaseSignature(ver, sumsRaw string) error {
	sigRaw, err := fetchReleaseFile(ver, "SHA256SUMS.minisig")
	if err != nil {
		return fmt.Errorf("未获取到 SHA256SUMS.minisig，无法确认包的可信来源，已中止更新: %w", err)
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
	if err := chMod(destBin, 0755); err != nil {
		return err
	}
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
			if _, err := copyFile(path, destNewPath); err != nil {
				return fmt.Errorf("拷贝 %s 失败: %w", path, err)
			}
			// 拷贝的配置文件含 vkey/web_password 等敏感项：0640（组可读），不再 0766 全局可写
			if err := chMod(destNewPath, 0640); err != nil {
				return err
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
				err := os.Mkdir(destSplitPath, 0755)
				if err != nil {
					log.Fatalln(err)
				}
			}
		}
	}
	// 先以 0600 创建，避免拷贝过程中出现“凭据文件短暂全局可读”的窗口；
	// 调用方（copyStaticFile / CopyDir）随后按用途 chmod 到 0755 / 0640。
	dstFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
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

// chMod 收紧文件权限。配置文件里含 vkey / web_password 等凭据，
// chmod 失败必须作为错误上报，不能静默放过 —— 否则敏感文件可能一直保持宽松权限。
func chMod(name string, mode os.FileMode) error {
	return os.Chmod(name, mode)
}
