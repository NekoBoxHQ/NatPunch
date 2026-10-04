# NatPunch 安全加固指南

> 对应安全修复的最终姿态（阶段一~四完成后）。**以下每一项都应在对外提供服务前落实。**

## 0. 默认凭证与暴露面（最优先）

- 面板管理员密码：首次启动随机生成；安装脚本**强制输入**（空回车无法安装）。请使用强密码。
- `public_vkey`：默认禁用（注释）；启用时必须是强随机值（首次生成路径已随机化）。**不要使用任何公开示例值。**
- 面板只应暴露给受信网络；公网直接暴露时务必开 HTTPS + 验证码。
- **安全默认值建议**（本期未改默认以保兼容，建议下个发版调整）：
  - `allow_user_login=true` → 建议改为 `false`（关闭客户端账号登录，仅管理员），或至少开启 `open_captcha=true`。
  - `open_captcha=false` → 建议改为 `true`（登录验证码）。

## 1. 隧道 TLS 与指纹固定（防中间人）

桥接隧道默认 `tls_enable=true`，服务端首次启动生成 ECDSA P-256 自签证书，**持久化于 `conf/bridge.pem|key`（0600）**，与面板证书 `conf/server.pem|key` 隔离——更换面板证书不会改变桥接指纹，已配指纹的客户端不会掉线。

### 客户端三态指纹（F2-2）

| `tls_fingerprint` | `tls_strict` | 行为 |
|---|---|---|
| 空 | 任意 | 沿用旧行为（不校验服务端证书），**启动打印醒目告警** |
| `<hex>` | 任意 | `VerifyPeerCertificate` 严格比对 SHA-256 指纹，不匹配即握手失败 |
| 非空 | `true` | 强制要求非空，否则客户端拒绝启动 |

### 指纹迁移步骤（存量部署）

1. 服务端启动后，面板「客户端」页的 **TLS 一键命令已自动携带当前服务端指纹**（`-tls_fingerprint=...`）；存量客户端手工补配时可执行：
   `./natpunch -conf_path=<dir>` 后从日志读取指纹（也可直接 `openssl x509 -in conf/bridge.pem -noout -fingerprint -sha256` 换算）。
2. 客户端配置 `tls_fingerprint=<hex>`，先保持旧版本连接正常（告警仍打印）。
3. 全量客户端补配指纹后，服务端/客户端设置 `tls_strict=true`，此后指纹不符即拒绝连接。
4. 升级不断连：新版本在指纹为空时**保持旧行为 + 告警**，绝不 fail-closed。

> 信任模型：指纹固定保护"传输信道"，防止中间人解密/改写隧道流量（含注入内网的流量）。指纹=通道凭证，与账号密码体系互补。

## 2. 认证与会话

- 面板登录：bcrypt 密码 + 会话 Cookie（`SameSite=Strict`，HTTPS 下 `Secure`），登录后会话 ID 轮换（防固定）。
- 变更操作一律 POST + 会话鉴权（GET 只做渲染，见 D11）。
- 终端 SSH：仅管理员或本人客户端可开 shell；WebSocket Origin 白名单；**每次开 shell 记录审计日志** `TERMINAL AUDIT: user [x] opened shell on client id [y] ...`。
- 客户端 VKEY 为 128bit（crypto/rand）。**存量 40bit vkey**：登录面板 → 客户端 → 重置 VKEY → 更新客户端配置（旧 vkey 立即失效）。

## 3. 资源上限（防滥用/耗尽）

| 配置 | 推荐值 | 说明 |
|---|---|---|
| `max_clients` | `100` | 客户端注册上限（0 = 不限） |
| `max_tunnels_per_client` | `20` | 每客户端隧道上限（0 = 不限） |
| `max_global_conn` | 按容量 | 全局并发连接上限（0 = 不限） |
| `ip_limit` | `true` | 配合面板 IP 白名单限制注册来源 |

UDP/p2p 工作池内置上限（单源 16、4096 源、TTL 5 分钟、满即丢包），无额外配置。

## 4. 升级完整性（信任模型）

发布物附 `SHA256SUMS` 与 `SHA256SUMS.minisig`，安装脚本分级校验：

1. **SHA256 强制校验**：失败即终止。防传输损坏与镜像篡改。
2. **签名校验**：安装脚本已内置发布方公钥（`MINISIGN_PUBKEY` 环境变量可覆盖）。本机存在 minisign 工具时强制校验 minisig、失败即终止；目标机无 minisign（如 OpenWrt 默认）则打印警告并跳过（SHA256 仍强制，不阻塞安装）。

> 两个校验的信任边界不同：SHA256 防"链路层篡改"；签名防"发布方密钥泄露"。公钥内置在 `install.sh` / `install_server.sh`（`RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660`），自建发布链可用环境变量覆盖。解包使用标准库 tar+gzip，拒绝 `..`/绝对路径/符号链接逃逸。

## 5. 依赖与漏洞状态（阶段四 F4-2）

- `govulncheck`：**代码 0 漏洞、import 包 0 漏洞**；残余 1 个 module 级（GO-2026-5932，`golang.org/x/crypto/openpgp` 未维护且本项目未使用，无修复版本，不可达）。
- `kcp-go` 保持 `v5.4.20+incompatible`：无已知漏洞；v5.6+ 因 module path 变更为 `/v5` 需改 import（列为后续项）。
- `beego`（1.12.0，replace 至 NekoBoxHQ 维护分支）历史 CVE 评估见 [SECURITY.md](../SECURITY.md)；升级 beego 2.x 为后续项。
- CI 门禁：`go vet` / `go test` / `govulncheck` / `golangci-lint` / integration（Docker）全绿才允许发布。

## 6. 运维红线

- 日志/接口不下发密码与 vkey（DTO 脱敏，阶段三 #18）；日志出现密码即视为事故。
- `pprof` 被强制绑回环（`127.0.0.1:6060`），不要尝试公网暴露。
- 不要修改已发布包的 conf 权限（安装器已收敛到 0700/0600）；配置内含 vkey 与口令。
- 只用于**合法运维场景**（见 README AUP）；终端会话有审计日志，可追溯谁在何时对哪台设备开了 shell。
