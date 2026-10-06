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
