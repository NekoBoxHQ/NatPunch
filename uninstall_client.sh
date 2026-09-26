#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 仅操作客户端专属命名 natpunch-client，与服务端 natpunch 完全隔离（进程名/自启名/二进制名均不同），互不影响
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
set -u
REPO="NekoBoxHQ/NatPunch"
ACTION="${1:-uninstall}"
CLIENT_BIN_1="/usr/bin/natpunch-client"
CLIENT_BIN_2="/usr/local/bin/natpunch-client"
CLIENT_CONF_1="/etc/natpunch.conf"
CLIENT_CONF_2="/etc/natpunch/natpunch.conf"
CLIENT_CONF_3="/usr/local/etc/natpunch.conf"
CLIENT_INIT="/etc/init.d/natpunch-client"
CLIENT_SYSTEMD_1="/etc/systemd/system/natpunch-client.service"
CLIENT_SYSTEMD_2="/lib/systemd/system/natpunch-client.service"
CLIENT_PLIST="/Library/LaunchDaemons/com.natpunch.client.plist"
CLIENT_AGENT="$HOME/Library/LaunchAgents/com.natpunch.client.plist"
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }

# ---------- 客户端进程精确识别（专属进程名 natpunch-client，服务端 natpunch 天然不受影响） ----------
get_client_pids() {
    PIDS=""
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$exe" in
                */natpunch-client|*/natpunch-client\ \(deleted\)) PIDS="$PIDS $p" ;;
            esac
        done
    else
        PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}')
    fi
    echo "$PIDS"
}
kill_client_pids() {
    PIDS=$(get_client_pids)
    [ -z "$PIDS" ] && return 0
    for p in $PIDS; do kill "$p" 2>/dev/null || true; done
    sleep 1
    for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true; done
    sleep 1
}

# ---------- 更新模式：保留配置，仅替换二进制并重启 ----------
if [ "$ACTION" = "update" ]; then
    log "更新 NatPunch 客户端（保留配置）..."
    if [ ! -f "$CLIENT_BIN_1" ] && [ ! -f "$CLIENT_BIN_2" ]; then
        warn "未检测到已安装客户端，请使用 install.sh 全新安装"
        exit 1
    fi
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   PKG="linux_amd64_client.tar.gz" ;;
        aarch64|arm64)  PKG="linux_arm64_client.tar.gz" ;;
        *) warn "不支持架构: $ARCH（当前仅支持 x86_64 / arm64）"; exit 1 ;;
    esac
    VER=""
    if command -v wget >/dev/null 2>&1; then
        VER="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    elif command -v curl >/dev/null 2>&1; then
        VER="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    fi
    log "最新版本: ${VER:-最新发布}"
    # 下载走 releases/latest/download，自动指向最新发布，不依赖写死的版本号
    URL="https://github.com/$REPO/releases/latest/download/$PKG"
    TMP_DIR="/tmp/natpunch_update.$$"
    mkdir -p "$TMP_DIR" || { warn "无法创建临时目录"; exit 1; }
    trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
    if command -v wget >/dev/null 2>&1; then
        wget -q -O "$TMP_DIR/pkg.tar.gz" "$URL" || { warn "下载失败: $URL"; exit 1; }
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$TMP_DIR/pkg.tar.gz" "$URL" || { warn "下载失败: $URL"; exit 1; }
    else
        warn "未找到 wget / curl"; exit 1
    fi
    [ -s "$TMP_DIR/pkg.tar.gz" ] || { warn "下载文件为空"; exit 1; }
    tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || { warn "压缩包损坏"; exit 1; }
    tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || { warn "解压失败"; exit 1; }
    BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client | head -n1)"
    [ -n "$BIN_SRC" ] || { warn "压缩包内未找到 natpunch-client 二进制"; exit 1; }
    # —— 断连保护 ——
    # SSH 通常通过客户端打通的隧道连接：停止客户端 = 隧道断 = SSH 断。
    # 升级文件已下载并校验完成，此处让后续替换/重启流程脱离终端会话，
    # 忽略挂断信号并改道日志，否则脚本随 SSH 挂断被杀 → 客户端停而不启 → 设备失联。
    if [ -z "${NATPUNCH_UPDATE_DETACHED:-}" ]; then
        export NATPUNCH_UPDATE_DETACHED=1
        echo "==> 升级文件已就绪，流程转入后台执行"
        echo "==> SSH 断开后自动完成替换与重启，日志: /tmp/natpunch_update.log"
        echo "==> 完成后客户端自动重启，隧道恢复后请重新连接"
        trap '' HUP
        exec > /tmp/natpunch_update.log 2>&1 < /dev/null
    fi
    # 停止客户端服务与残留进程（natpunch-client 专属名称，不影响同机服务端）
    if [ -f "$CLIENT_INIT" ]; then
        "$CLIENT_INIT" stop 2>/dev/null || true
    fi
    if command -v systemctl >/dev/null 2>&1; then
        if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
            systemctl stop natpunch-client 2>/dev/null || true
        fi
    fi
    kill_client_pids
    # 替换二进制
    cp -f "$BIN_SRC" "$CLIENT_BIN_1" || { warn "写入 $CLIENT_BIN_1 失败"; exit 1; }
    chmod 755 "$CLIENT_BIN_1"
    [ -f "$CLIENT_BIN_2" ] && { cp -f "$BIN_SRC" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
    # 重新启动客户端服务（natpunch-client 专属名称，不触碰服务端 natpunch）
    if [ -f /etc/openwrt_release ]; then
        [ -f "$CLIENT_INIT" ] && "$CLIENT_INIT" start 2>/dev/null || true
    elif command -v systemctl >/dev/null 2>&1; then
        systemctl restart natpunch-client 2>/dev/null || systemctl start natpunch-client 2>/dev/null || true
    fi
    sleep 3
    VER_OUT="$("$CLIENT_BIN_1" -version 2>/dev/null | head -n1)"
    # 验证客户端已恢复运行（断连保护场景下此段写入后台日志）
    RUNNING=0
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$exe" in
                */natpunch-client) RUNNING=1; break ;;
            esac
        done
    else
        ps w 2>/dev/null | grep -v grep | grep -q 'natpunch-client' && RUNNING=1
    fi
    if [ "$RUNNING" = "1" ]; then
        log "更新完成 ${VER_OUT:-最新版}"
    else
        warn "更新完成但客户端未运行，请检查日志: /tmp/natpunch_update.log"
    fi
    rm -rf "$TMP_DIR"
    trap - EXIT INT TERM
    exit 0
fi
if [ "$ACTION" != "uninstall" ]; then
    echo "用法: sh uninstall_client.sh [update|uninstall]" >&2
    exit 1
fi

# ================= 卸载模式 =================
# 1. 精确结束客户端进程（专属进程名 natpunch-client，不影响同机服务端）
kill_client_pids
# 2. 停止/删除客户端自启（natpunch-client 专属名称）
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
fi
rm -f /etc/rc.d/*natpunch-client 2>/dev/null
for f in /etc/rc.d/S??natpunch-client /etc/rc.d/K??natpunch-client /etc/rc*.d/S??natpunch-client /etc/rc*.d/K??natpunch-client; do
    [ -e "$f" ] || continue
    rm -f "$f"
done
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        systemctl stop natpunch-client 2>/dev/null || true
        systemctl disable natpunch-client 2>/dev/null || true
        rm -f "$U"
    done
    systemctl daemon-reload 2>/dev/null || true
fi
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then
    if [ -f /usr/local/etc/rc.d/natpunch-client ]; then
        service natpunch-client stop 2>/dev/null || true
        rm -f /usr/local/etc/rc.d/natpunch-client
        command -v sysrc >/dev/null 2>&1 && sysrc -x natpunch_client_enable 2>/dev/null || true
    fi
fi
if [ -d /Library/LaunchDaemons ]; then
    if [ -f "$CLIENT_PLIST" ]; then
        launchctl bootout system "$CLIENT_PLIST" 2>/dev/null || true
        launchctl unload "$CLIENT_PLIST" 2>/dev/null || true
        rm -f "$CLIENT_PLIST"
    fi
fi
if [ -f "$CLIENT_AGENT" ]; then
    launchctl unload "$CLIENT_AGENT" 2>/dev/null || true
    rm -f "$CLIENT_AGENT"
fi
# 3. 清理 rc.local（仅 natpunch-client 行）
if [ -f /etc/rc.local ]; then
    cp -f /etc/rc.local /etc/rc.local.natpunch-client.bak 2>/dev/null || true
    if sed --version >/dev/null 2>&1; then
        sed -i '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    else
        sed -i '' '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    fi
fi
# 4. 删除客户端配置
rm -f "$CLIENT_CONF_1" "$CLIENT_CONF_2" "$CLIENT_CONF_3"
if [ -d /etc/natpunch ]; then
    if [ ! -f /etc/natpunch/natpunch ] && [ ! -f /etc/natpunch/conf/natpunch.conf ]; then
        rm -rf /etc/natpunch
    else
        warn "/etc/natpunch 目录含服务端文件，跳过删除"
    fi
fi
if [ -d /usr/local/etc/natpunch ]; then
    if [ ! -f /usr/local/etc/natpunch/natpunch ]; then
        rm -rf /usr/local/etc/natpunch
    else
        warn "/usr/local/etc/natpunch 目录含服务端文件，跳过删除"
    fi
fi
# 5. 删除客户端二进制
rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
# 6. 清理日志
rm -f /tmp/natpunch.log /var/log/natpunch.log /tmp/natpunch-client.log /var/log/natpunch-client.log /tmp/natpunch 2>/dev/null || true
# 7. 复查
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch-client) REMAIN=1; echo "残留 PID $p: $(tr '\0' ' ' < "$d/cmdline" 2>/dev/null)" ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    warn "仍检测到客户端进程，请手动检查"
    exit 1
fi
log "客户端已卸载完成"
exit 0
