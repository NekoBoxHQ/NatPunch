# NatPunch 架构说明

本文说明组件、数据流与核心机制（对应安全修复的版本基线，阶段四）。派生关系与上游差异见 NOTICE。

## 1. 总体拓扑

```
                        公网                                    内网
┌───────────────────┐         ┌─────────────────────────┐         ┌───────────────────┐
│  访问方            │         │ NatPunch 服务端 (natpunch)│         │ 被管理设备          │
│  SSH / RDP / 浏览器│ ──────► │  Web 管理面板 (beego)   │ 桥接    │  natpunch-client    │
│  HTTP / SOCKS5    │         │  TCP/UDP/HTTP 隧道监听   │◄──────►│  (注册/心跳/隧道)   │
└───────────────────┘         │  bridge 桥接服务 (8024)  │         │  本地 shell / 服务  │
                              └─────────────────────────┘         └───────────────────┘
```

- **服务端 `natpunch`**：常驻公网，提供 Web 管理面板 + 各类隧道监听 + 桥接服务。
- **客户端 `natpunch-client`**：部署于内网设备（OpenWrt / Linux），主动向服务端注册（TCP/KCP），建立长连接（mux 复用）。
- 访问方连**服务端暴露的端口**，服务端经**桥接连接**把流量转发到对应客户端的本地服务。

## 2. 核心组件

| 组件 | 位置 | 职责 |
|---|---|---|
| Web 面板 | `web/` | beego 控制器 + 视图；客户端/隧道/流量/系统信息管理，SSH 终端（WebSocket） |
| 桥接服务 bridge | `bridge/` | 客户端注册、鉴权（vkey）、版本握手、隧道建连（SendLinkInfo）、心跳 |
| 隧道代理 | `server/proxy/` | tcp/udp/http(s)/socks5/p2p 协议代理；连接数/流量/带宽限制 |
| 多路复用 mux | `lib/natpunch_mux/` | 单条桥接连接上复用多条逻辑连接（connStatusOkCh/FailCh 事件、优先队列） |
| 客户端 | `client/` | 注册、重连、本地监听（socks5/p2p/secret）、健康检查 |
| 密码学 | `lib/crypt/` | 隧道 TLS（三态指纹）、证书持久化、随机密钥（crypto/rand） |
| 安装/守护 | `lib/install/` `lib/daemon/` | 服务注册、升级（SHA256 强制校验 + minisign 分级）、kill 安全封装 |

## 3. 连接与数据流

### 3.1 客户端注册（桥接）
1. 客户端连接 `bridge_ip:bridge_port`（TCP 或 KCP，`bridge_type` 指定）。
2. 发送 `REGISTER` 帧（含 vkey / 版本号 / 主机信息）。
3. 服务端校验 vkey 与版本（`crypt.Md5` 认证值 + `compareVersion` 按段比较，阶段四 G7 已修）。
4. 成功后注册 mux 会话：服务端与客户端之间所有隧道数据都在这一条复用连接上。

### 3.2 隧道数据流（以 TCP 隧道为例）
```
访问方 ──TCP──► 服务端 tcp 监听端口
                  │  GetConn() 原子配额校验（阶段三 #4：连接数 CAS + max_global_conn）
                  ▼
              server/proxy/tcp.go  ──SendLinkInfo──► bridge ──mux──► client
                  │                                                      │
                  ▼                                                      ▼
              流量记账(Flow/Rate)                                  本地目标端口
```

### 3.3 UDP / P2P
- UDP 隧道：服务端 UDP 监听，每源地址一个工作池（阶段一 F1-7：ants 池 + TTL 淘汰 + 4096 源上限，满即丢包），报文长度字段带硬上界（F1-3）。
- P2P：服务端做打洞协调（`p2p_ip:p2p_port`），map 读写有锁（F1-4），provider 条目带 TTL。

## 4. 加密与认证（阶段二之后）

| 层 | 机制 | 说明 |
|---|---|---|
| 面板登录 | bcrypt 密码 + 会话（SameSite=Strict / HTTPS Secure / 登录后 RegenerateID） | 存量明文登录时在线迁移 |
| 隧道传输 | 可选 TLS（`tls_enable=true`），ECDSA P-256 自签证书持久化于 `conf/bridge.pem|key` | 与面板证书 `server.pem|key` 隔离 |
| 防中间人 | 客户端 `tls_fingerprint`（SHA-256 指纹严格比对）/ `tls_strict`（强制） | 空值 = 沿用旧行为 + 启动告警 |
| 客户端鉴权 | vkey（128bit，crypto/rand） | 升级不变更；存量 40bit 手动重建客户端处理 |
| 管理 API | 无 query 参数认证（F1-1 已删除）；变更一律 POST + 会话鉴权 | 机器 API 预留 `/api/v1/*`（HMAC，未实施） |

## 5. 资源与限流

| 项 | 配置 | 默认 |
|---|---|---|
| 客户端数上限 | `max_clients` | 0（不限） |
| 每客户端隧道数 | `max_tunnels_per_client` | 0（不限） |
| 全局连接数 | `max_global_conn` | 0（不限） |
| UDP 源工作池 | 内置 | 单池 16 / 4096 源 / TTL 5min |
| 流量限制 | `allow_flow_limit` + 客户端 FlowLimit | 数据面复查（阶段三 #10） |
| 带宽限制 | `allow_rate_limit` + Rate | 按 burst 分块（阶段三 #10） |
| pprof | `pprof_ip/pprof_port` | 强制回环 127.0.0.1:6060 |

## 6. 关键安全边界（代码评审阶段一~三已修复）

- 认证：面板纯会话鉴权（fail-closed 三路分支），无 `auth_key` 旁路。
- 崩溃面：SOCKS5 长度字段 nil 解引用、UDP 长度越界、p2p/connMap 并发 map、nil task 均已封堵。
- 终端：仅管理员/本人客户端可开 shell；WebSocket Origin 白名单；操作留审计日志（TERMINAL AUDIT）。
- 日志/接口：密码不落日志、DTO 脱敏（不下发 WebPassword/VerifyKey）。
- 升级：发布物 SHA256SUMS（强制）+ minisign（分级），解包防路径逃逸。

详见 [security-hardening.md](security-hardening.md)。
