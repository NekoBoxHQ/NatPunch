# NatPunch 配置参考

> 覆盖服务端 `conf/natpunch.conf` 全量配置项（阶段四 F4-4）。客户端配置见文末。

## 服务端 `conf/natpunch.conf`

首次启动（配置文件不存在时）会生成随机 `web_password` 与 `public_vkey`；配置文件已存在则**不做任何改写**（升级安全）。

### HTTP(S) 代理

| 配置项 | 默认 | 说明 |
|---|---|---|
| `http_proxy_ip` | `0.0.0.0` | HTTP 代理监听地址；留空则不启动 |
| `http_proxy_port` | `80` | HTTP 代理端口 |
| `https_proxy_port` | `443` | HTTPS 代理端口 |
| `show_http_proxy_port` | `true` | 面板是否展示非 80 端口 |

### 桥接（bridge）

| 配置项 | 默认 | 说明 |
|---|---|---|
| `bridge_type` | `tcp` | 客户端注册通道类型：`tcp` 或 `kcp` |
| `bridge_port` | `8024` | 桥接监听端口（客户端连接此端口注册） |
| `bridge_ip` | `0.0.0.0` | 桥接监听地址 |

### 客户端注册

| 配置项 | 默认 | 说明 |
|---|---|---|
| `public_vkey` | 注释（禁用） | 公共密钥：多个客户端共用同一 vkey 注册；**自用版默认禁用**。启用时建议强随机值（首次生成路径已随机化 16 字符） |

### 上限（阶段一 F1-8 / 阶段三 #4）

| 配置项 | 默认 | 说明 |
|---|---|---|
| `max_clients` | `0` | 客户端注册数量上限；`0` = 不限（存量语义）。推荐 `100` |
| `max_tunnels_per_client` | `0` | 每客户端隧道数量上限；`0` = 不限。推荐 `20` |
| `max_global_conn` | `0` | 全局并发连接上限（TCP/代理等走配额校验的入站；UDP 数据面经独立工作池限流，不纳入本配额）；`0` = 不限 |

### 流量与日志

| 配置项 | 默认 | 说明 |
|---|---|---|
| `flow_store_interval` | `1` | 流量数据持久化间隔（分钟）；留空 = 不持久化 |
| `log_level` | `6` | 日志级别：0 Emergency … 6 Informational … 7 Debug |
| `log_path` | `natpunch.log` | 日志文件路径 |

### IP 限制

| 配置项 | 默认 | 说明 |
|---|---|---|
| `ip_limit` | 注释 | `true/false` 限制注册来源 IP（配合面板 IP 白名单） |

### Web 管理面板

| 配置项 | 默认 | 说明 |
|---|---|---|
| `web_host` | `a.o.com` | 面板 Host 绑定（反代场景） |
| `web_username` | `admin` | 管理员用户名 |
| `web_password` | 首次启动随机 | 管理员密码（bcrypt 存储；存量明文登录时在线迁移） |
| `web_port` | `8081` | 面板端口 |
| `web_ip` | `0.0.0.0` | 面板监听地址 |
| `web_base_url` | 空 | 反代子路径，如 `/natpunch` |
| `web_open_ssl` | `false` | 面板 HTTPS（证书 `web_cert_file`/`web_key_file`） |
| `web_cert_file` / `web_key_file` | `conf/server.pem` / `conf/server.key` | 面板证书（**与桥接证书 `conf/bridge.pem|key` 隔离**，见安全加固） |
| `allow_ports` | 注释 | 允许客户端开放的端口范围，如 `9001-9009,10001,11000-12000` |

### 多用户

| 配置项 | 默认 | 说明 |
|---|---|---|
| `allow_user_login` | `true` | 允许客户端账号登录面板（**安全加固建议：设为 `false`，见 security-hardening.md**） |
| `allow_user_register` | `false` | 允许自助注册 |
| `allow_user_change_username` | `true` | 允许用户改名 |

### 扩展限制

| 配置项 | 默认 | 说明 |
|---|---|---|
| `allow_flow_limit` | `true` | 启用隧道流量上限（客户端设置 FlowLimit 后生效） |
| `allow_rate_limit` | `true` | 启用带宽限制 |
| `allow_tunnel_num_limit` | `true` | 启用客户端隧道数限制 |
| `allow_local_proxy` | `false` | 允许本地代理 |
| `allow_connection_num_limit` | `true` | 启用客户端连接数限制 |
| `allow_multi_ip` | `true` | 每个隧道监听不同服务端端口 |
| `system_info_display` | `true` | 面板展示系统资源信息 |

### HTTP 转发 / 缓存

| 配置项 | 默认 | 说明 |
|---|---|---|
| `http_add_origin_header` | `true` | 代理请求添加来源头（取真实 IP） |
| `http_cache` | `false` | HTTP 缓存开关 |
| `http_cache_length` | `100` | 缓存条目上限 |

### 调试 / 连接

| 配置项 | 默认 | 说明 |
|---|---|---|
| `pprof_ip` / `pprof_port` | 注释 | pprof 调试。**阶段三起强制回环**：配置 `0.0.0.0`/`::`/空 一律绑定 `127.0.0.1:6060`，禁止公网暴露 |
| `disconnect_timeout` | `60` | 客户端断连判定超时（秒） |
| `open_captcha` | `false` | 面板登录验证码（**安全加固建议：设为 `true`**） |

### 隧道 TLS（阶段二 F2-2）

| 配置项 | 默认 | 说明 |
|---|---|---|
| `tls_enable` | `true` | 桥接隧道 TLS 开关 |
| `tls_bridge_port` | `8025` | TLS 桥接端口 |

## 客户端配置

客户端通过 `-conf_path` 指定配置目录（如 `/etc/natpunch-client/conf/natpunch.conf`），常用项：

| 配置项 | 说明 |
|---|---|
| `server` | 服务端地址 `host:port`（TLS 桥接用 `tls_bridge_port`） |
| `vkey` | 客户端密钥（面板生成；128bit） |
| `bridge_type` | `tcp` / `kcp` |
| `tls_enable` | 是否启用 TLS 桥接 |
| `tls_fingerprint` | 期望的服务端桥接证书 SHA-256 指纹（hex）。空 = 沿用旧行为并打印告警；非空 = 严格比对，不匹配即握手失败 |
| `tls_strict` | `true` 时强制要求 `tls_fingerprint` 非空，否则拒绝启动 |
| `auto_reconnection` | 断线自动重连 |
| `proxy_url` | 客户端出口代理（可选） |
| `disconnect_timeout` | 断连判定超时 |

## 新增配置项的兼容性说明

- `max_clients` / `max_tunnels_per_client` / `max_global_conn` 均为 **0 = 不限**，存量部署升级后行为不变，不会打断既有隧道。
- `tls_fingerprint` / `tls_strict` 缺省时行为与旧版完全一致（仅打印一次启动告警），升级不断连。
