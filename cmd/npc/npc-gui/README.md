# NPS 客户端 GUI (Wails v3 版本)

基于 [Wails v3](https://v3.wails.io/)（beta）构建的 NPS 客户端图形界面。

## 快捷命令格式

快捷命令使用 Base64 编码，解码后的格式为：
```
nps:name|addr|key|tls
```

示例：
```
nps:MyServer|127.0.0.1:8024|mykey123|false
```

编码后的 Base64：
```
bnBzOk15U2VydmVyfDEyNy4wLjAuMTo4MDI0fG15a2V5MTIzfGZhbHNl
```

## 前置要求

- Go 1.25+（wails v3 最低要求）
- Node.js 16+
- Yarn
- Wails v3 CLI（wails3）

安装 Wails CLI：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.12
```

## 启动 / 调试

### 开发模式（热重载）

```bash
cd cmd/npc/npc-gui

# 启动开发模式：自动执行前端依赖安装、bindings 生成、yarn build:dev、
# Go 编译并运行应用（无需手动 yarn build）
# Vite 端口默认为 9245，自定义端口用 -port（如 -port 34115）
wails3 dev
```

### 构建

```bash
cd cmd/npc/npc-gui

# 构建当前平台（前端构建 + bindings 生成 + Go 编译自动完成）
wails3 build --tags npcgui
```

产物输出到 `bin/` 目录（如 Windows 为 `bin/npc-gui.exe`）。

### 跨平台构建

通过 `GOOS`/`GOARCH` 环境变量指定目标平台：

```bash
# Windows amd64（在非 Windows 上需 Docker 镜像 wails-cross 支持 CGO 交叉编译）
GOOS=windows GOARCH=amd64 wails3 build --tags npcgui

# macOS
GOOS=darwin GOARCH=arm64 wails3 build --tags npcgui

# Linux（需要 libgtk-3-dev / libwebkit2gtk-4.1-dev）
GOOS=linux GOARCH=amd64 wails3 build --tags npcgui
```

### 打包安装包

```bash
# Windows NSIS 安装器（需要 makensis）
wails3 package

# 或指定安装范围（user = 免 UAC 按用户安装）
wails3 package INSTALL_SCOPE=user
```

### 手动生成前端绑定

Go 侧绑定方法签名变更后，重新生成前端 bindings：

```bash
cd cmd/npc/npc-gui
wails3 generate bindings
```

生成结果位于 `frontend/bindings/`。

### 常用任务

```bash
wails3 task --list            # 列出所有 Taskfile 任务
wails3 task build             # 等价于 wails3 build
wails3 task run               # 运行已构建的产物
wails3 update build-assets    # 刷新 build/ 目录构建资产
```

## 项目结构（v3）

```
cmd/npc/npc-gui/
├── Taskfile.yml          # 根任务定义（APP_NAME、包管理器、端口）
├── main.go               # 应用入口（application.New + 窗口 + 服务注册）
├── app_bindings.go       # App 服务（前端可调用的绑定方法）
├── tray.go               # 系统托盘（v3 内置 SystemTray）
├── build/
│   ├── config.yml        # 应用信息、版本、dev 模式配置
│   ├── Taskfile.yml      # 公共任务（前端安装/构建、bindings 生成、图标）
│   └── windows|darwin|linux/  # 各平台构建资产与打包模板
└── frontend/
    ├── bindings/         # wails3 generate bindings 生成（已提交）
    ├── src/App.vue       # 主界面（import { App as AppAPI } from '../bindings/npc-gui/index.js'）
    └── dist/             # 前端构建产物（embed 进二进制）
```

## 构建标签

- `npcgui`：GUI 客户端构建必须传入（排除 `server/proxy/tcp.go` 中的服务端隧道实现，见 `server/proxy/tcp_npcgui.go` 占位实现）

## 配置存储

连接配置自动保存在以下位置：
- Windows: `%APPDATA%\npc\npc_data.json`
- Linux: `~/.config/npc/npc_data.json`
- macOS: `~/Library/Application Support/npc/npc_data.json`

客户端日志默认保存在 `<配置目录>/npc/logs/`，可在设置中修改日志目录。
