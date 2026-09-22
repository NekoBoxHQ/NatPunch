# NatPunch

![Version](https://img.shields.io/badge/version-v0.26.38-blue)
![License](https://img.shields.io/badge/license-GPL--3.0-green)
![Go](https://img.shields.io/badge/Go-%3E%3D1.22-00ADD8?logo=go)

> 轻量级内网穿透 / 无公网设备管理平台。TLS 全程加密，TCP/UDP 隧道 + HTTP/SOCKS5 代理，静默管理软路由与服务器。

---

## ✨ 特性

- **TLS 全程加密**，数据传输安全可靠
- **TCP / UDP 隧道**：SSH、远程桌面、任意端口，公网直达内网设备
- **HTTP / SOCKS5 代理**：支持账号密码，安全出网
- **静默管理**：实时展示在线状态、隧道数、流量与带宽统计
- **一键部署**：OpenWrt / Linux 一键安装，注册为系统服务静默运行

---

## 🚀 部署

### 服务端

SSH 粘贴执行，自动下载最新版、解压、启动：

```shell
sh -c "$(wget -qO- https://raw.githubusercontent.com/lima-droid/NatPunch/master/install_server.sh)"
```

安装完成后按提示打开 Web 面板，首次启动的默认账号密码见日志。

### 客户端

登录 Web 面板，在【客户端】页添加客户端，复制 VKEY，在待部署设备上 SSH 粘贴执行面板生成的一键安装命令。

### 卸载客户端

```shell
wget -qO- https://raw.githubusercontent.com/lima-droid/NatPunch/master/uninstall_client.sh | sh
```

脚本自动识别客户端进程（按 -vkey= 精确匹配），停止服务、清理自启和配置，**不影响同机服务端**。

---

## 📃 License

GPL-3.0 License
