# NatPunch

> 自托管的**内网穿透 / 设备运维平台**：一台公网服务器 + 若干内网设备，就能把家里的路由器、机房里的机器、公司内网的 Web 服务搬到公网上来，并且在一个面板里统一管理。

> 基于 [ehang-io/nps](https://github.com/ehang-io/nps)（GPLv3）重构而来，与原版的差异见 [NOTICE](./NOTICE)。

---

## 它能解决什么问题

- 家里的路由器 / NAS / 树莓派没有公网 IP，想在外面 SSH 或打开管理页；
- 公司内网的 Web 服务要临时给外部同事看，又不想开 VPN；
- 手上有十几台设备，想在一个面板里看到**谁在线、跑了多少流量、隧道通不通**，随手开关；
- 需要一条**不依赖第三方**的运维通道：面板、隧道、终端全部自建，数据只经过自己的服务器。

---

## 功能总览

### 隧道与代理（4 种模式）

| 模式 | 用途 | 关键能力 |
|---|---|---|
| `tcp+udp` | SSH、远程桌面、数据库、任意端口 | 同一端口同时监听 TCP 与 UDP；支持 HTTPS（服务端借面板证书终止 TLS 后转发）、自定义访问路径、健康检查、多目标轮询 |
| `httpProxy` | 浏览器走 HTTP 代理出网 | 独立账号密码、按隧道限速限流量 |
| `socks5` | 通用 SOCKS5 代理 | 独立账号密码、限速限流量 |
| `shadowsocks` | 加密代理 | 独立密码与加密方式 |

每条隧道可以单独设置：端口、目标地址、是否 HTTPS、是否启用、备注、流量上限、限速、健康检查。

### 客户端（被穿透的内网设备）

- VKEY 认证，一条 `natpunch-client` 常驻进程，开机自启（OpenWrt 用 procd，Linux 用 systemd）；
- 面板实时显示：在线状态、版本、出口地址、内网地址、上传/下载流量、实时带宽、连接数；
- 支持：启停、删除、编辑备注、限速（KB/s）、流量上限、最大连接数、**IP 白名单**（含授权密码）、**到期时间**、黑名单、允许/禁止配置文件连接。

### 管理面板

- **仪表盘**：负载 / CPU / 内存 / 连接数 / 实时带宽 / 流量统计 / 连接类型，共 7 张图；
- **客户端列表**：状态、连接、隧道、选项、部署命令一键复制；
- **隧道列表**：隧道管理 / HTTP 代理 / SOCKS5 代理 / Shadowsocks 四类分开管理；
- 表格支持搜索、排序、批量操作；状态用固定尺寸色块表示（在线绿、离线灰）；
- **切源开关**：一键在「GitHub」与「面板自建源」之间切换客户端安装命令的来源。

### 面板 SSH 终端

- 左侧**主机栏**列出所有可操作客户端，点一台即在右侧打开终端；
- 多主机切换、字号调整、清屏、`Ctrl+Shift+C` 复制、右键粘贴、底部整行输入条（手机友好）；
- **快捷命令**：保存在服务端（所有设备通用），一键发送到当前终端；
- **权限隔离**：管理员可操作任意客户端，普通账号只能操作自己的；
- **审计日志**：每次开会话都记录操作人、目标客户端、时间、来源 IP；
- 断线自动重连（WebSocket ping/pong 保活，90 秒无响应判定失效）。

### 部署与升级

- 服务端：交互式管理菜单（安装 / 启动 / 停止 / 重启 / 状态 / 改面板配置 / 升级 / 卸载）；
- 客户端：面板一键生成命令，OpenWrt 用 `uclient-fetch`、Linux 用 `wget`，粘贴即装；
- **断连安全升级**：升级先下载并校验，再把替换/重启转入后台执行，SSH 断开不影响升级完成；
- **自建源**：客户端安装 / 升级可以完全不访问 GitHub（见下文）；
- 并发保护：安装脚本带锁，重复执行会提示「已有操作进行中」，避免两个升级互相踩踏。

### 安全

| 层 | 机制 |
|---|---|
| 面板登录 | 密码 bcrypt 存储、会话 SameSite=Strict、登录后重置 SessionID、可选验证码 / GitHub OAuth / IP 白名单 |
| 隧道传输 | 可选 TLS 桥接（`tls_enable`），支持**证书指纹固定**（`tls_fingerprint`，SHA-256）与强制模式（`tls_strict`，没配指纹就拒绝启动） |
| 安装包 | SHA256 强制校验 + minisign 签名校验（系统有 `minisign` 用它，否则用内置 `minisign-check`），两者都不通过就中止升级 |
| 终端 | 归属校验 + 审计日志 + 输入长度/窗口尺寸上限 |

---

## 架构

```
                    公网                                     内网
┌──────────────────┐        ┌────────────────────────────┐        ┌──────────────────┐
│ 访问方            │        │ NatPunch 服务端 (natpunch)  │        │ 被管理设备        │
│ SSH / RDP / 浏览器│ ──────▶│  Web 面板（beego）          │        │ natpunch-client  │
│ HTTP / SOCKS5     │        │  隧道监听（各模式独立端口） │        │  本地 shell / 服务│
└──────────────────┘        │  桥接服务 8024 / TLS 8025   │◀──────▶│  注册 / 心跳 / 隧道│
                            └────────────────────────────┘        └──────────────────┘
```

- **服务端 `natpunch`**：常驻公网，提供面板 + 隧道监听 + 桥接服务；
- **客户端 `natpunch-client`**：部署在内网设备，主动连接服务端并保持长连接；
- 所有隧道数据都跑在**一条复用连接**上（mux），访问方每来一个请求就开一条逻辑流，不会为每个连接重新握手。

更细的组件说明、数据流、加密与鉴权细节见 [docs/architecture.md](docs/architecture.md)、[docs/config-reference.md](docs/config-reference.md)、[docs/security-hardening.md](docs/security-hardening.md)。

---

## 快速开始

### 1. 服务端

在公网服务器上（推荐全新系统，root 执行）：

```shell
sh -c "$(wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/install_server.sh)"
```

进入管理菜单后按 `1` 安装，按提示设置面板端口、账号密码。安装完成会自动注册为系统服务并启动。

菜单项：`1` 安装 · `2` 启动 · `3` 停止 · `4` 重启 · `5` 状态 · `6` 改面板配置 · `7` 升级 · `8` 卸载 · `0` 退出。

默认端口：

| 用途 | 默认 |
|---|---|
| 面板 | `8081`（可在安装时改，建议配合 `web_open_ssl` 开 HTTPS） |
| 桥接（客户端连接） | `8024` |
| TLS 桥接 | `8025`（`tls_enable=true` 时生效） |

### 2. 客户端

登录面板 →【客户端】页 → 新增客户端并复制 VKEY → 在客户端列表点【部署】复制对应命令，到设备上粘贴执行。

生成的命令形如：

```shell
# OpenWrt
uclient-fetch -qO- https://<面板地址>/static/dl/install.sh | sh -s -- '<VKEY>' <面板域名> <端口> '<tls>'

# Linux
wget -qO- https://<面板地址>/static/dl/install.sh | sh -s -- '<VKEY>' <面板域名> <端口> '<tls>'
```

四个参数分别是：VKEY、面板域名、桥接端口、是否启用 TLS。

> 客户端二进制名为 **`natpunch-client`**，服务端为 **`natpunch`**，进程名 / 服务名 / 安装目录互不冲突，同一台机器可以同时装服务端和客户端。

### 3. 查看状态 / 改面板端口 / 卸载（服务端）

```shell
wget -O /tmp/np-install.sh https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/install_server.sh
sh /tmp/np-install.sh status     # 状态
sh /tmp/np-install.sh passwd     # 改面板端口 / 账号 / 密码
sh /tmp/np-install.sh uninstall  # 卸载
```

---

## 升级与卸载

### 服务端

```shell
wget -O /tmp/np-install.sh https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/install_server.sh
sh /tmp/np-install.sh upgrade
```

升级会自动：备份当前二进制与配置 → 下载新版并校验 → 替换 → 重启服务 → 刷新面板自建源。回滚点在同目录的 `natpunch.bak.<时间戳>`。

### 客户端

`uninstall_client.sh` 同时负责**卸载**和**更新**：

```shell
# 更新（保留 /etc/natpunch.conf 配置，只换二进制并重启）
wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/uninstall_client.sh | sh -s update

# 卸载
wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/uninstall_client.sh | sh
```

三个脚本都会自动识别系统：OpenWrt 用 `uclient-fetch`，其它 Linux 用 `wget`/`curl`。

> **升级不会因为 SSH 断开而中断**：脚本先下载校验完毕，再把「替换 + 重启」交给后台进程执行（日志 `/tmp/natpunch_update.log`），隧道恢复后重连即可。在面板 SSH 终端里升级同样安全。

---

## 自建源：让客户端完全不依赖 GitHub

面板每次升级都会把一整套安装包（6 个平台包 + 4 个校验器 + 校验文件 + 三个脚本，约 55MB）下载到服务端 `web/static/dl/` 下，客户端只从面板取包。

```
/opt/natpunch/web/static/dl/
├── install.sh  uninstall_client.sh  install_server.sh   # 稳定入口
└── v26.10.81/                                            # 当前版本
    ├── linux_{amd64,arm64,arm,mipsle}_client.tar.gz
    ├── linux_{amd64,arm64,arm,mipsle}_server.tar.gz
    ├── minisign-check-linux-{amd64,arm64,arm,mipsle}
    ├── SHA256SUMS  SHA256SUMS.minisig
    └── install.sh  uninstall_client.sh  install_server.sh
```

### 面板上的「切源」开关

客户端列表工具栏有 **`切源：GitHub` / `切源：面板`** 按钮：

- **GitHub**（默认）：生成的命令从 GitHub 取脚本和安装包，适合设备本身能访问 GitHub；
- **面板**：生成的命令改为从面板取，命令里会带上 `NATPUNCH_BASE_URL=<面板>/static/dl/<版本>`，**客户端完全不需要访问 GitHub**。

开关状态记在浏览器本地，按设备能力各选各的即可。

### 为什么命令里只写 `.../static/dl`

面板每次升级会把整个 `dl/` 目录换掉，只保留**当前版本**的子目录。如果命令里写死了
`.../dl/v26.10.76`，下次升级到 77 之后那个目录就没了，客户端更新会 404。

所以发布时会额外写一个 `dl/version.txt`（内容就是当前版本号），安装/升级脚本读到它就
自动拼出 `<根>/<版本>/`。**命令只写 `.../static/dl` 永远有效**，换版本不用改命令。

```shell
# 只写根目录即可（脚本自己找当前版本）
wget -qO- https://panel.example.com/static/dl/uninstall_client.sh \
  | NATPUNCH_BASE_URL=https://panel.example.com/static/dl sh -s update
```

### 环境变量（三个安装脚本通用）

| 变量 | 作用 | 示例 |
|---|---|---|
| `NATPUNCH_BASE_URL` | 直接指定发布目录（含版本），完全不访问 GitHub | `https://panel.example.com/static/dl/v26.10.81` |
| `NATPUNCH_VERSION` | 只覆盖版本号，仍按官方地址拼 | `v26.10.81` |
| `NATPUNCH_MIRROR` | 镜像前缀，拼在官方地址前面 | `https://ghfast.top` |

例子：

```shell
# 从面板升级客户端（当前版本目录要写全）
uclient-fetch -qO- https://panel.example.com/static/dl/uninstall_client.sh \
  | NATPUNCH_BASE_URL=https://panel.example.com/static/dl/v26.10.81 sh -s update
```

> 面板 `dl/` 目录下的旧版本会在下次升级时被替换，只保留当前版本；如果需要回退到旧版，从 GitHub Release 取对应版本包。

---

## 面板使用要点

### 客户端列表

列：`备注 | 版本 | 出口地址 | 内网地址 | 上传流量 | 下载流量 | 网速 | 状态 | 连接 | 隧道 | 选项 | 部署`

- **状态**：开放 / 关闭（是否允许连接）；**连接**：在线 / 离线（客户端是否在位）；
- **隧道**：跳到该客户端的隧道列表；**选项**：启停、删除、编辑；
- **部署**：复制该客户端的安装命令（OpenWrt / Linux），受「切源」开关影响。

### 隧道列表（隧道管理 / HTTP 代理 / SOCKS5 / Shadowsocks）

列：`客户端 | 备注 | 目标 | 端口 | 状态 | 运行 | 客户端 | 选项`，支持健康检查与 HTTPS 端口。

### SSH 终端

- 打开方式：左侧菜单「SSH终端」，进去后从主机栏选设备；
- 左侧主机栏只显示名称与在线状态，搜索框按名称/地址过滤；
- 未选主机时右侧是空态；选中后终端区可用，可随时点另一台切换；
- 快捷命令保存在服务端，所有设备通用；新建/编辑/删除仅管理员可用。

---

## 安全建议

1. **改掉默认面板密码**（默认 `admin/123`），并在首次登录后立即修改；
2. 面板尽量开 HTTPS：`web_open_ssl=true` + 证书路径，或用反向代理终止 TLS；
3. 桥接链路建议 `tls_enable=true`，并给客户端配 `tls_fingerprint`（配合 `tls_strict=true` 更严格）；
4. 隧道端口暴露在公网，代理类隧道务必设置账号密码；
5. 不必要的客户端/隧道及时关闭或删除；
6. 面板 SSH 终端等于目标机的 root shell，只给可信账号管理员权限。

---

## 配置参考（服务端 `conf/natpunch.conf`）

| 键 | 说明 |
|---|---|
| `bridge_port` / `bridge_type` | 桥接端口与协议（`tcp` / `kcp`） |
| `tls_enable` / `tls_bridge_port` | 是否启用 TLS 桥接，默认 `8025` |
| `web_port` / `web_ip` | 面板监听端口与地址 |
| `web_username` / `web_password` | 面板账号密码 |
| `web_open_ssl` / `web_cert_file` / `web_key_file` | 面板 HTTPS |
| `web_base_url` | 面板挂在子路径时使用（如 `/natpunch`） |
| `install_base_url` | 客户端安装源，`auto` = 按当前访问域名自动拼，留空 = 只用 GitHub |
| `disconnect_timeout` | 客户端心跳超时（秒），默认 60 |
| `max_tunnels_per_client` | 每个客户端最多隧道数，0 = 不限 |
| `allow_user_login` / `allow_user_register` | 是否允许普通用户登录 / 注册 |
| `allow_flow_limit` / `allow_rate_limit` / `allow_tunnel_num_limit` / `allow_connection_num_limit` | 各类限制开关 |
| `system_info_display` | 面板是否显示系统信息 |
| `log_level` / `log_path` | 日志级别与路径 |

完整清单见 [docs/config-reference.md](docs/config-reference.md)。

客户端常用启动参数：

```shell
# TCP 桥接可启用 TLS
natpunch-client -server=<面板域名> -vkey=<VKEY> -type=tcp \
                -tls_enable=true -tls_fingerprint=<SHA256> -tls_strict=true

# KCP 桥接不使用 TLS 端口
natpunch-client -server=<面板域名> -vkey=<VKEY> -type=kcp
```

---

## 常见问题

**Q：客户端装不上，提示下载失败？**
设备访问不了 GitHub。在面板把「切源」切到**面板**再复制命令，或手动加 `NATPUNCH_BASE_URL`。

**Q：升级时提示「已有 NatPunch 操作进行中」？**
说明有另一个安装/升级进程正在跑（锁目录 `natpunch.lock.d`）。等它结束；如果确认没有进程在跑，锁超过 10 分钟会被自动接管。

**Q：面板第一次打开有点慢？**
页面公共资源（JS/CSS）已开启 gzip，并做了按需加载（只有仪表盘加载 383KB 的 echarts，只有点主机时才加载 xterm）。如果仍然慢，多半是到服务器的链路带宽问题。

**Q：隧道端口打不开？**
按顺序查：客户端是否在线 → 隧道是否启用/运行 → 目标地址端口是否通 → 服务器防火墙是否放行该端口。

**Q：忘记面板密码？**
`sh install_server.sh passwd` 重设。

---

## 支持平台

| | 架构 |
|---|---|
| 客户端 | `linux_amd64` `linux_arm64` `linux_arm` `linux_mipsle` |
| 服务端 | `linux_amd64` `linux_arm64` `linux_arm` `linux_mipsle` |

系统：OpenWrt（含 iStoreOS 等衍生版，procd 托管）与主流 Linux 发行版（systemd 托管）。

---

## 开发与测试

```shell
go build ./...                        # 编译
go test ./...                         # 单元测试

# 脚本自测（CI 里也在跑这三个）
sh test/upgrade_logic_test.sh         # 安装/升级/回滚流程的纯逻辑分支
sh test/config_keys_test.sh           # 模板与代码引用的配置键是否对得上
sh test/checker_anchors_test.sh       # 内嵌校验器的 SHA256 锚点是否正确
```

CI 在 `.github/workflows/release.yml`：push 到 `master` 跑 vet / 脚本语法检查 / 单测 / `-race` / govulncheck / golangci-lint；打 `v*` tag 时交叉编译四个架构、生成 `SHA256SUMS` 并 minisign 签名，发布 14 个构建产物。

---

## 可接受使用政策（AUP）

NatPunch 面向**合法运维场景**：仅允许用于部署方拥有或经明确授权的设备与网络（自建机房、家庭网络、公司内网运维等）。禁止用于：

- 未经授权访问、控制或穿透他人的设备、服务器与内网；
- 利用「面板 SSH 终端」等特性获取他人系统的 shell；
- 任何违反当地法律或平台服务条款的用途。

部署方对使用行为与数据合规承担全部责任。终端会话会记录审计日志（操作人、时间、目标客户端、来源 IP）。

---

## License

GPL-3.0 License，版权与修改范围见 [LICENSE](LICENSE) 与 [NOTICE](NOTICE)。

## 讨论群组

Telegram：[NatPunch 讨论群](https://t.me/+Kdxyw8yLTz85ODg5)
