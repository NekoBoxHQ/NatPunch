# Changelog

本项目为 GPLv3 许可的内网穿透项目，派生关系与上游差异见 NOTICE。

## 未发布

### 变更
- **客户端标识全面统一为 `natpunch-client`**：源码标识、构建目录、服务单元名、面板提示、日志与临时文件名里残留的旧客户端标识全部改名，与服务端 `natpunch` 对称。README / LICENSE / NOTICE 的上游归属声明按要求保持不变。
- **修复：面板给出的客户端启动命令与实际二进制名不符**。面板此前显示 `npc.exe` / `./npc`，而发布包里的可执行文件叫 `natpunch-client`，照抄命令必然「找不到文件」；现改为 `natpunch-client.exe` / `./natpunch-client`。
- **修复：菜单里的「更新客户端」必然失败**。`lib/install` 在更新包中查找的是名为 `npc` 的文件，而发布包内是 `natpunch-client`，因此 100% 报「更新包中未找到可执行文件」。改名后与 `os.Executable()` 自替换、SHA256 校验链路一致。
- **修复文档**：`docs/config-reference.md` 里客户端配置路径写成 `conf/npc.conf`，实际是 `conf/natpunch.conf`。
- 构建标签 `npcgui` / `npcsdk` 改名为 `natpunchgui` / `natpunchsdk`，构建目录 `cmd/npc/` 改为 `cmd/natpunch-client/`（入口文件 `main.go`）。

### 兼容
- 环境变量新增 `NATPUNCH_SERVER_ADDR` / `NATPUNCH_SERVER_VKEY`，同时继续识别旧的 `NPC_SERVER_ADDR` / `NPC_SERVER_VKEY` —— 容器 / 编排里既有变量若被静默丢弃，表现是「服务起来了但连不上」，属于最难排查的一类故障。
- vkey 临时文件改名为 `natpunch-client-vkey.txt`，读取时回退旧名 `npc_vkey.txt`，升级后首次启动不会因读不到而直接退出。
- **自行构建 GUI / SDK 的命令需同步改**：`go build -tags natpunchgui ...`、`go build -tags natpunchsdk ...`（CI 与 Makefile 已更新）。
- 安装脚本里对 OpenWrt `firewall.allow-npc-download` / `/opt/npc_download` 的清理**保持不变**：那是上游安装器留下的历史残留，改名会让清理失效。

### 平台与代码清理（只保留 OpenWrt / Linux）
- **删除全部 Windows 专用代码**：`lib/common/pid_windows.go`、`lib/natpunch_mux/sysGetsock_windows.go`、`server/proxy/transport_windows.go` 三个平台垫片，以及 22 处 `IsWindows()` 分支（日志/安装/临时目录路径、`.exe` 后缀、`splitStr` 的 CRLF 分叉、`tasklist` / `taskkill` 等）。其中 `pid_windows.go` 的 `IsProcessAlive` 恒返回 false，等于 Windows 上单实例保护形同虚设。
- **删除二进制自带的「服务自安装」整套**：`install` / `start` / `stop` / `restart` / `uninstall` / `status` / `reload` / `service` 子命令、两个交互式菜单（客户端 `printSlogan`/`inputCmd`/`systemService`/`systemPro`，服务端 `-server` 管理脚本）、`lib/daemon` 整个包，以及 `github.com/kardianos/service` 依赖（连带 `fatih/color`、`go-colorable`、`go-isatty`）。
  开机自启与守护从来都由 `install.sh` / `install_server.sh` 写的 procd / systemd 单元负责，这层自安装是纯冗余；它还带来一个隐患：二进制自装的服务名与脚本装的单元名不同，两者可能各跑一个实例、抢同一个 vkey。
  保留 `-version`（安装器用来比对版本）、`update`（就地替换二进制）、`register` / `nat` / `status`（诊断）。
- **客户端无参数启动不再进交互菜单**：原来找不到配置文件就打印菜单等 stdin 输入，而服务化部署下没有 stdin，表现是「进程起来了但什么都没干」。现在明确报错并以非零码退出。
- **面板展示的客户端命令改为绝对路径** `/usr/bin/natpunch-client`（`install.sh` 的安装位置）。原来的 `./natpunch-client` 只在当前工作目录恰好是二进制所在目录时才有效，而 SSH 进去默认在 `/root`。
- 合计 **−1499 / +381 行**。

### 安全修复（全面审计后）
- **跨租户越权（严重）**：`server.GetTunnel` 的 `type=tcp+udp` 分支完全没有 `clientId` 归属过滤，非管理员只要把 `type` 传成 `tcp+udp` 就能列出**全部客户端**的 tcp/udp 隧道，而返回体每一行都内嵌完整 `Client`（含明文 `VerifyKey` 与 `WebPassword` 哈希）。已补齐过滤；同时 `IndexController.GetTunnel` 在非管理员拿不到会话 `clientId` 时改为拒绝，不再退化成 0（0 在 `GetTunnel` 里表示「不限客户端」）。
- **跨租户越权（中）**：`tunnelBelongsToMe` 用 Tasks / Hosts 两套彼此独立的自增 id 做「或」判定，两者都从 1 起各自增长、必然重号 —— 自己名下有 `Host #N` 就能操作别人的 `Task #N`。`web/` 里没有任何地方操作 Host，该分支没有正当用途，已改为只查 Tasks。
- **远程 DoS（严重）**：`lib/conn/conn.go` 的 `GetHostInfo` / `GetConfigInfo` / `GetTaskInfo` 在底层读取失败时仍解引用指针（此时为 nil），任何通过 vkey 校验的客户端发一个畸形帧即可打崩服务端进程（调用点在无 `recover` 的 goroutine 里）。已改为检测错误与 nil 并返回，并上报此前被吞掉的 `json.Unmarshal` 错误（畸形配置原被当成「零值客户端」照单全收）。
- **远程 DoS（高）**：客户端对服务端下发的 `RemoteAddr` 直接 `strings.Split(...,":")[1]`，不含冒号时越界 panic —— 该 goroutine 无 `recover`，整个以 root 运行的客户端会退出。改用 `net.SplitHostPort`，解析失败时回退到真实远端地址。
- **存储型 XSS（高）**：客户端列表的 `Version` / `LocalAddr` 由客户端上报、绕过了入库时的 HTML 转义，而 bootstrap-table 默认不转义单元格 → 管理员打开列表页即执行脚本。两列已加显式转义 formatter。
- **验证码可绕过（中）**：登录校验验证码失败时只调用了 `ServeJSON`（它不终止执行）而缺 `return`，凭据正确时仍会建立会话，`open_captcha` 等于没开。
- **服务端 OOM / 任意文件读（中）**：HTTPS 监听按客户端注册 host 时上报的 `CertFilePath` / `KeyFilePath` 做无界读取（指向 `/dev/zero`、`/proc/kcore` 或大文件即可）。新增 `common.ReadCertFile`：只认普通文件、上限 1MB。
- **资源泄漏（中）**：客户端 `handleUdpMonitor` 是永不退出的 `for-select`，而每次重连都会重新 `StartLocalServer` → 每重连一次泄漏一个 goroutine + ticker，新旧实例还会一起抢 `udpConn` / `udpConnStatus`。改为由停止通道收口，`CloseLocalServer`（每次重连前都会调用）统一结束。
- **数据竞争**：SOCKS5 UDP 的 `clientAddr`（改 `atomic.Value`，用户端首包到达前丢弃下行包而非传 nil）；`UdpModeServer.removeSession` 对 `sess.target` 的无同步读（改为与 sweeper 同一口径，经 `ready` 判定）；客户端 `s.signal` / `s.tunnel`（改 `atomic.Pointer`，`handleMain` 持本地引用，消除关闭时的 nil 解引用）；`Client.IpWhiteList` 的并发读写（读侧 `RLock` 取快照，写侧 copy-on-write）；`common.in()` 不再就地排序调用方切片（改线性扫描）。
- **凭据处理**：`vkey`、SOCKS5 账密、配置模式重连 vkey 的比较改用常量时间（新增 `common.ConstantTimeStrEq`）；`IpWhiteAuth` 日志里的完整 vkey 只保留前 4 位；`GetClient` 对非管理员同时抹掉 `IpWhitePass`（此前只抹了 `VerifyKey`）；客户端 vkey 临时文件改用 `O_NOFOLLOW` 打开（`/tmp` 若可被非特权用户写入，预置同名符号链接即可让 root 客户端覆盖任意文件）。
- **更新 / 文件路径加固**：更新路径 4 处 `http.Get` 改用带 60s 超时的 client（原来走 `DefaultClient`，卡住的连接会让更新永久挂起）；`chMod` / `CopyDir` 不再吞掉 `os.Chmod` 与拷贝错误；新建文件先以 0600 落地，目录用 0755 而非 0777。

### 签名链与并发收紧（第二轮）
- **签名校验改为 fail-closed**。原来「拿不到 `SHA256SUMS.minisig` 就告警放行」是整条签名链的降级口子：SHA256 比对的 `SHA256SUMS` 与包来自同一渠道，属于自洽校验 —— 攻击者控制发布渠道时只要不提供签名文件，就能让签名这层形同虚设。现在 `lib/install` 与三个 shell 安装器（`install.sh` / `install_server.sh` / `uninstall_client.sh`）在拿不到可信签名时一律中止。
- **内置校验器哈希不符也改为中止**（原为警告后放行）。校验器与包同源下载，把请求换成乱码即可逼出「跳过签名校验」的降级路径。
- **`uninstall_client.sh` 补上内置静态校验器**。此前它只有系统 `minisign` 一条路，而 OpenWrt 上没有 minisign 包 —— 也就是说**客户端更新路径一直只做了 SHA256**，等于把「用网上下载的二进制覆盖本地 root 二进制」完全托付给 HTTPS。现在与 `install.sh` 同级：校验器哈希必须与脚本内嵌信任锚一致才执行。CI 的信任锚比对同步纳入该脚本，避免以后发版时锚点过期反而把升级卡死。
- **无签名环境留有显式逃生开关** `NATPUNCH_ALLOW_UNSIGNED=1`（默认不启用）。自建发布链等场景可显式放行，日志会明确标注「不防发布渠道被控」。
- **`server/proxy/http.go` keep-alive 换 host 的跨代复用**：转发 goroutine 原来闭包引用外层的 `connClient` / `host` / `isReset`，而这三者在换 host 时都会被重新赋值 —— 旧 goroutine 退出时的 `defer connClient.Close()` 关掉的其实是**新**连接；共享的 `isReset` 也可能被新一代替成 false，导致旧 goroutine 反过来关掉仍在使用的客户端连接。改为每一代把句柄、host、重置标记显式传进 goroutine，重置标记用独立的 `atomic.Bool`。
- 新增回归断言：`test/upgrade_logic_test.sh` 现在会检查三个脚本都走统一的签名失败收口、不存在「跳过签名校验」的放行分支、校验器哈希不符是硬失败、以及逃生开关存在。

### 已知未修（需单独决策）
- TLS 未配置 `tls_fingerprint` 时仍以 `InsecureSkipVerify` 放行（只防被动窃听，不防中间人），需 `tls_strict=true` 才拒绝启动。**这次没有改默认值**：现网客户端是以 `-tls_enable=true` 起的，把默认改成 fail-closed 会让它们升级后直接拒绝启动 —— 正是「不可以无缘无故不启动」要避免的那类故障。要收紧请在客户端显式加 `-tls_strict=true -tls_fingerprint=<服务端桥接证书指纹>`。

### 界面冗余清理（以面板实际入口为准）
逐项核对「面板上真有入口的才算功能」，删掉全部不可达的页面与接口：

- **隧道列表的 8 个无入口筛选页**：`/index/tcp`、`/index/udp`、`/index/file`、`/index/secret`、`/index/p2p`、`/index/host`、`/index/help`，以及无任何前端调用的 `/index/getonetunnel` 与 `/index/reorder`。菜单本来就只挂了 `tcpudp` / `http` / `socks5` 三个；`/index/all` 由客户端列表页的「隧道」按钮进入，保留。
  （注：表头里的 `sortable` 是 bootstrap-table 的列排序，与 reorder 接口无关，全仓库没有任何地方调用它。`server.ReorderTasks` 与 `CheckUserAuth` 里针对 reorder 的守卫分支随之删除。）
- **全局配置页** `GlobalController` + `views/global/index.html`：没有任何入口（`CheckUserAuth` 里非管理员本来就被 deny），连同路由注册一并删除。
- **`AuthController.GetTime`**：没有任何前端引用。
- **`index/list.html` 里 `row.Mode == "p2p"` / `"secret"` 的快令区块**：该模板只被 tcpudp（过滤 tcp+udp/tcp/udp）、http（httpProxy）、socks5 三个页面使用，这两个条件**恒为假**。
- **`static/js/echarts.min.js`**：面板没有任何图表用它（仪表盘是数字条 + CSS），却每个页面都在加载，从内嵌资源里去掉。
- **`conf/natpunch_client.conf`**：无人引用的上游样例配置（里面还留着原作者的本机路径与 IP），且它示范的 `file`/`secret`/`p2p` 模式已不在界面上。

**保留**（有入口或有配置 / CLI 可达）：终端（客户端列表页的 SSH 按钮进入）、注册页（`allow_user_register=true` 时登录页出现入口）、`/index/all`、`/auth/ipwhiteauth`（客户端 IP 白名单 API，不是界面功能）。

**本次未动**：`file` / `secret` / `p2p` 三种隧道模式在服务端与客户端的实现。它们在界面上确实建不出来，但客户端的配置文件与 `-local_type=` 仍能创建（随包的客户端样例里就写着 `mode=file`），属于配置 / CLI 可达，不是「界面弃用」。

## v26.9.111（已发布）

### 变更
- **CI 增加 `-race` 门禁**：`check` job 新增 `go test -race ./...`。本项目修复集中于并发路径（pidfile 原子化、sourcepool 池淘汰、p2p 清扫、mux 队列、globalconn 计数），无 `-race` 时数据竞争无法被自动发现。
- **Go 自更新增加 minisign 签名校验**：`lib/install` 用 `go-minisign` + 内嵌公钥校验 `SHA256SUMS.minisig`，与 install.sh / install_server.sh 同策略（有签名强制校验、未提供则告警放行）。此前仅比对未签名的 SHA256SUMS，等价于只信任 HTTPS 传输。
- **修复「从不限速改为限速」不生效**：`UpdateClient` 改为比较旧值决定是否重建 Rate，并停掉被替换对象的 ticker（避免 goroutine 泄漏）。
- **客户端 web 密码统一哈希**：修复 bridge pub 模式注册以明文覆盖已哈希值；`NewClient` / `UpdateClient` 集中处理，`$2` 前缀不二次哈希。
- **并发修复**：`UdpModeServer.sweeper` 改用 `ready` 通道判定会话就绪，消除对 `sess.target` 的无同步读取；`sourcePoolSet` 在 `Close` 后不再被 `Submit` 复活无人清扫的池；`HttpsListener.Close` 排空 `acceptConn` 缓冲，避免最多 8 个已建立连接泄漏。
- **pid 文件改用 flock（Unix）**：锁由内核在进程退出时自动释放，消除陈旧文件与 PID 复用误判；不支持文件锁的平台退回 `O_CREATE|O_EXCL` 路径。
- `quick_cmds.json` 权限由 0644 收紧为 0600，与其余写入口一致。

### 工程化 / CI
- master 推送现在会触发 `check`（vet / test / -race / govulncheck / lint）；`build` / `minisign-check` / `release` 仅由 `v*` 标签触发，避免 master 推送误建 release。
- 新增回归测试：pidfile 四项（存活拒绝 / 陈旧接管 / 空文件保守拒绝 / 并发仅一个成功）、sourcepool 两项（Close 后拒绝提交 / 重复 Close 幂等）。

## v26.9.110（已发布）

### 变更
- **单实例 pid 文件原子化**：以 `O_CREATE|O_EXCL` 主张所有权，消除原「读文件判断存活 → 无条件覆盖」的 TOCTOU 竞态（两个进程可同时通过检查、各持内存副本互写 clients.json）。
- `web/controllers/base.go` fail-closed 加固：`isAdmin` 缺失/非法的会话不再放行动作执行。
- `CheckUserAuth` 对 `index/reorder` 一律拒绝：该动作参数为 `ids`（无 `id`），且会重写全部任务的 Sort，无法按归属校验。

## v26.9.109（已发布）
- CI release 上传加固：softprops 偶发 `other side closed` 中断上传，新增 `gh` 兜底步骤——校验资产数、对缺失项重试补传（v26.9.108 实测踩坑）。

## v26.9.108（已发布）
- 校验器信任锚更新为 CI 实测哈希（此前锚点由本地构建填入，与 CI 产物不符）。

## v26.9.107（已发布）
- 校验器产物改用 `-buildvcs=false -trimpath` 构建，使产物跨 VCS 状态可复现（Go 默认会把 git revision/modified 编进二进制，导致本地与 CI 哈希必然不同、锚点永远对不上）。
- release job 补 `actions/checkout`，使信任锚守卫真正生效（此前缺该步骤，守卫会因读不到脚本而静默走 warning 分支）。
- 守卫把本次产物的锚点值写入 job summary，便于直接粘贴。

## v26.9.106（已发布）
- **校验器信任锚内嵌脚本**：不再用同渠道下载的 SHA256SUMS 校验校验器（镜像被控时可同时替换包 / 清单 / 校验器，构成循环信任）。
- **`max_clients` 配额豁免合成条目**：`GetClientCount` 跳过 `NoStore` 客户端，修复 `max_clients=N` 实际只能注册 N-1 个。
- **p2p 条目硬上限**：`p2pMaxEntries=4096`，防「不同 key 高速发包」绕过 TTL 清扫造成内存增长。

## v26.9.105（已发布）
- 复评「发布前必修 3 项」+ 建议项 + 文档对齐：
  - 安装脚本在发布未提供 `.minisig` 时改为告警放行（原实现因内置公钥非空而恒为真，导致无签名发布的安装全部失败）。
  - 文档与代码对齐：integration 门禁、重置 VKEY 入口、安装器权限实际值、`max_global_conn` 覆盖范围等。

## v26.9.104（已发布）
- **程序级单实例保护**：直接运行服务时若已有实例则退出，根治双进程各自持内存互写 clients.json 导致 vkey 丢失。

## v26.9.103（已发布）
- 双进程互覆盖数据库根因修复——systemd 优先单进程管理。

## v26.9.102（已发布）
- **vkey 不可变防呆**（升级掉线保护）：编辑客户端留空即保持原值、手填 <8 位拒绝、查重；移除面板「重置 VKEY」入口（避免误操作导致批量掉线）。存量 40bit vkey 的手动轮换步骤见 `docs/security-hardening.md`。
- 升级前备份 conf 目录；clients.json 缺失/为空时自动从备份恢复。

## v26.9.101（已发布）
- CI integration job 移除（路径过期 + 默认 skip）；安装菜单空输入优化。

## v26.9.100（已发布）
- 版本发现改为按版本号取最高（`releases/latest` 按发布时间排序，跨分支时可能取到较低版本）。

## v26.9.99（已发布）
- **全仓库 nps/NPS 标识清零**，仅 LICENSE / NOTICE 保留法律声明。
- `lib/crypt` 移除旧版前缀快速命令的解析兼容（存量客户端用面板重新生成命令即可）。

## v26.9.98（已发布）

### 变更
- **README 完全品牌化**：README / README_zh 移除顶部与 License 章节的显著派生声明，发布说明模板同步；上游相关仅保留在 LICENSE / NOTICE（法律声明）。
- **OpenWrt 签名校验落地（静态校验器）**：新增 `cmd/minisign-check`（Go 版 go-minisign 库），CI 随发布物静态交叉编译 4 架构（`minisign-check-linux-<arch>`）；`install.sh` / `install_server.sh` 签名校验升级为三级——系统 minisign → 自动下载内置静态校验器 → 降级 SHA256 兜底。**工具可得但校验失败即终止**，仅工具完全不可得才警告跳过（OpenWrt 无 minisign 软件包场景首次获得完整签名校验能力）。
- CI 新增 shell 语法门禁（`sh -n install.sh install_server.sh`）。

## v26.9.97（已发布）

### 变更
- **项目完全 NatPunch 化**：module path 全面改为 `github.com/NekoBoxHQ/NatPunch`（go.mod + 全部 import）；`cmd` 目录改名 `cmd/natpunch`。
- 清理代码 / 配置 / 界面残留上游标识：Windows 服务名、服务安装/卸载/启停菜单、HTTP 代理 404 页、桥接证书 CN、日志路径与文件名、`/etc` 兼容路径、默认配置模板等。
- GPLv3 合规声明保留于 LICENSE / NOTICE（法律义务），`docs/comparison` 保留同类工具对比。

### 工程化 / 依赖 / 合规 / 文档
- CI 门禁：`check`（vet / go test / govulncheck / golangci-lint）→ `build`（linux amd64/arm64/armv7/mipsle × server/client 共 8 组合，产物架构自检）→ `release`（SHA256SUMS + minisign 签名 + 发布说明带 GPL 声明）。go-version 1.26。mux 集成测试（需 Docker+tc）为本地可选项（`NP_MUX_INTEGRATION=1`），不在 CI 门禁内。
- 依赖升级：x/net v0.59.0、x/crypto v0.57.0、x/text v0.42.0、x/sys v0.48.0、x/time v0.16.0、golang/snappy v1.0.0、ants/v2 v2.12.1。go.mod 升 go 1.26.0。
- kcp-go 保持 v5.4.20+incompatible（v5.6+ 需改 module path 为 /v5，列为后续项；无已知漏洞）。
- govulncheck：代码 0 漏洞，import 包 0 漏洞；残余 1 个 module 级（openpgp，未使用、无修复版，不可达）。
- 构建矩阵补 armv7/mipsle；install_server.sh / install.sh 未知架构显式报错。
- 版本号一致：`lib/version.VERSION` 默认 `(dev)`，发布由 -ldflags 注入 tag。
- 文档体系：docs/（architecture、config-reference、deploy、security-hardening、comparison）、CHANGELOG、SECURITY、CONTRIBUTING、Makefile。
- GPLv3 声明式合规：README 顶部声明派生自上游、LICENSE 补版权行、新增 NOTICE。
- README 措辞："静默管理/免凭据 SSH" 调整为"自动化运维管理/授权终端 + 审计日志"，新增 AUP 段落。
- 终端审计日志：`TERMINAL AUDIT`（操作人 / 时间 / 目标客户端 / 来源 IP）。

## v26.9.96（已发布）

### 变更
- 客户端列表页移除服务端桥接证书指纹展示条（TLS 一键命令仍自动携带指纹，功能不变）。
- install.sh：TLS_FLAG 写入 /etc/natpunch.conf 时整体加单引号——init.d 用 `. /etc/natpunch.conf` source 配置，值含空格时无引号会被拆成多条命令执行（多参数 TLS_FLAG 安装崩溃修复）。
- minisign 签名启用：CI 签名改 apt C 版 minisign（原 go-minisign `@v0.1.0` 子目录版本不存在，是未验证的死代码路径，密钥一配必炸）；发布方公钥内置 `install.sh` / `install_server.sh`（`MINISIGN_PUBKEY` 环境变量可覆盖），目标机有 minisign 工具即强制校验签名。

## 阶段三（commit 5aa65a0）稳定性
- mux 死锁修复（connStatusOkCh/FailCh 有缓冲 + select 超时）；写队列容量上限（默认 4096 chunk，超限断连并打独立日志）。
- 连接数原子 CAS + 全局连接上限 `max_global_conn`（默认 0 = 不限）。
- 客户端重连前显式关闭旧连接；包级全局状态收敛为实例字段。
- 健康检查 map 重建加锁；LRU 缓存加锁重写（Clear 复位）。
- 限速按 burst 分块；UDP/SOCKS5 数据面复查流量上限。
- pprof 强制回环；安装权限收敛（0700/0600/0640）；JSON 库文件 0600。
- daemon kill 弃用 shell 拼接（pid 解析校验 + exec 纯参数直传；Windows 保留 taskkill）。
- 库代码 os.Exit(0) 清除（返回 error；net.ListenTCP nil 陷阱规避）。
- 外部 IP 获取 HTTPS + 缓存；HTTP keep-alive 按建立时 host 记账。
- 读写返回值补齐；日志/接口 DTO 脱敏；listener 未就绪 Close 守卫等补入项。

## 阶段二（commit 9261ad5）安全基线
- 删除零调用方 AES-CBC/PKCS5 死代码；密钥生成改 crypto/rand；VKEY 提升 128bit；基础认证常量时间比较。
- 隧道 TLS 三态指纹（tls_fingerprint / tls_strict）；桥接证书持久化 `conf/bridge.pem|key` 并与面板证书隔离。
- 面板密码 bcrypt 化 + 存量明文在线迁移。
- 会话加固（SameSite=Strict / HTTPS Secure / SessionRegenerateID）。
- 终端权限收紧（管理员/本人客户端）+ WebSocket Origin 白名单 + 终端页存储型 XSS 修复。
- 升级完整性：compareVersion 按段比较；SHA256 强制校验 + minisign 分级签名；弃用 unpackit 改标准库安全解包（防路径逃逸、512MB 上限）。
- IDOR 显式 RBAC；GET=渲染 / POST=变更。

## 阶段一（commit 95bf47d）止血
- 删除 auth_key query 参数认证链路（未授权接管漏洞）；面板认证回归纯会话；isAdmin fail-closed 三路分支。
- 修复 SOCKS5 长度字段 nil 解引用远程崩溃；UDP 报文长度字段上界校验（防越界/OOM）。
- p2p map / connMap.Close 并发加锁；流量限制 nil task 防御。
- UDP/p2p goroutine 池化（按源池 + TTL 淘汰 + 4096 源上限）。
- 注册上限 max_clients / max_tunnels_per_client（默认 0 = 不限）；public_vkey 随机化；UpdateClient Rate goroutine 泄漏修复。
- 安装脚本密码强制输入；发布物 SHA256SUMS。

## 基线（commit 24cb16c）
- 上游全量导入（硬分叉基线）。
