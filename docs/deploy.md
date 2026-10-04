# NatPunch 部署指南

## 1. 快速开始

### 服务端（公网主机）

```shell
sh -c "$(wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/install_server.sh)"
```

进入管理菜单后输入 `1` 安装，**按提示设置面板端口、管理员用户名与密码**（密码强制输入，不允许空回车取默认值）。安装完成自动注册为系统服务并启动。

> 脚本是交互式管理菜单：`1` 安装 / `5` 状态 / `6` 修改面板连接配置 / `8` 卸载。

### 客户端（内网设备）

1. 浏览器打开 `http://<服务器IP>:<web_port>`，用管理员登录。
2. 【客户端】页添加客户端，复制 VKEY，复制一键安装命令。
3. 在设备上执行该命令（OpenWrt 用 `uclient-fetch`，Linux 用 `wget`）。

客户端二进制 `/usr/bin/natpunch-client`，自启服务 `natpunch-client`，与服务端 `natpunch` 完全命名隔离，可同机部署。

## 2. 支持的平台与架构

| 角色 | 架构 | 产物 |
|---|---|---|
| 服务端 / 客户端 | linux amd64 / arm64 / armv7 / mipsle | `linux_<arch>_server.tar.gz` / `linux_<arch>_client.tar.gz` |

OpenWrt 软路由常见架构均已覆盖（armv7 = 多数硬路由/软路由 32 位 ARM，mipsle = 经典 MTK 平台）。安装脚本对未知架构**显式报错退出**（不再静默回退 amd64）。

## 3. 验证安装

```shell
systemctl status natpunch        # 服务端状态
systemctl status natpunch-client # 客户端状态
```

面板「客户端」页应显示客户端在线；添加隧道后公网端口即通。

## 4. 防火墙

- 服务端需放行：桥接端口（默认 `8024` TCP，TLS 桥接 `8025`）、面板端口（`8081`）、隧道端口（面板中添加的监听端口）。
- 客户端需放行：本地服务端口（入站）。

## 5. 升级

### 服务端

菜单 `7`（升级）：下载最新发布包 → **SHA256 强制校验** → 本机有 minisign/openssl 时校验签名 → 替换并重启。

### 客户端

```shell
sh -c "$(wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/uninstall_client.sh)" -s update
```

升级保留 `/etc/natpunch.conf` 配置，仅替换二进制并重启。**升级断连安全**：SSH 通过客户端隧道连接时，升级会先下载校验，再转后台执行（日志 `/tmp/natpunch_update.log`），断开 SSH 不影响升级完成。

## 6. 卸载

```shell
# 服务端（脚本菜单 8）
# 客户端 Linux
wget -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/uninstall_client.sh | sh
# 客户端 OpenWrt
uclient-fetch -qO- https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master/uninstall_client.sh | sh
```

## 7. 生产部署建议

1. **立即修改**面板默认密码（安装脚本已强制）；`web_password` 在首次生成时为随机值。
2. 设置 `max_clients` / `max_tunnels_per_client` 等上限（见 config-reference）。
3. 面板尽量走 HTTPS（`web_open_ssl=true`，配置 `web_cert_file`/`web_key_file` 正式证书），或置于反向代理后。
4. 公网防火墙**只放行必需端口**；`pprof_ip` 不要配成 `0.0.0.0`（即使配了也会被强制绑回环）。
5. 按 [security-hardening.md](security-hardening.md) 完成加固后再对外提供服务。
