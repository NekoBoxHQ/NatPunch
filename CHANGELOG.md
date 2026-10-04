# Changelog

本项目为 GPLv3 许可的内网穿透项目，派生关系与上游差异见 NOTICE。

## [未发布]（v26.9.98 候选）

### 变更
- **README 完全品牌化**：README/README_zh 移除顶部与 License 章节的显著派生声明、发布说明模板同步；上游相关仅保留在 LICENSE / NOTICE（法律声明）。对外呈现完全为 NatPunch。
- **OpenWrt 签名校验落地（静态校验器）**：新增 `cmd/minisign-check`（Go 版 go-minisign 库，约 40 行极简校验器），CI 随发布物静态交叉编译 4 架构（`minisign-check-linux-<arch>`）；`install.sh` / `install_server.sh` 签名校验升级为三级——系统 minisign → 自动下载内置静态校验器 → 降级 SHA256 兜底。**工具可得但校验失败即终止**，只有校验工具完全不可得才警告跳过（OpenWrt 无 minisign 软件包场景首次获得完整签名校验能力）。
- CI 新增 shell 语法门禁（`sh -n install.sh install_server.sh`）。

### 工程化 / 依赖 / 合规 / 文档

## v26.9.96（已发布）

### 变更
- 客户端列表页移除服务端桥接证书指纹展示条（TLS 一键命令仍自动携带指纹，功能不变）。
- install.sh：TLS_FLAG 写入 /etc/natpunch.conf 时整体加单引号——init.d 用 `. /etc/natpunch.conf` source 配置，值含空格时无引号会被拆成多条命令执行（多参数 TLS_FLAG 安装崩溃修复）。
- minisign 签名启用：CI 签名改 apt C 版 minisign（原 go-minisign `@v0.1.0` 子目录版本不存在，是未验证的死代码路径，密钥一配必炸）；发布方公钥内置 `install.sh` / `install_server.sh`（`MINISIGN_PUBKEY` 环境变量可覆盖），目标机有 minisign 工具即强制校验签名。

## [未发布]（v26.9.97 候选）

### 变更
- **项目完全 NatPunch 化**：module path 全面改为 `github.com/NekoBoxHQ/NatPunch`（go.mod + 全部 import）；`cmd` 目录改名 `cmd/natpunch`。
- 清理代码/配置/界面残留上游标识：Windows 服务名、服务安装/卸载/启停菜单、HTTP 代理 404 页、桥接证书 CN、日志路径与文件名、`/etc` 兼容路径、默认配置模板等。
- **legacy 移除（v26.9.99）**：`lib/crypt` 删除旧版前缀快速命令的解析兼容（存量客户端以面板重新生成命令即可，无前缀新格式为默认）。
- GPLv3 合规声明保留于 LICENSE / NOTICE（法律义务），docs/comparison 保留同类工具对比。

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
