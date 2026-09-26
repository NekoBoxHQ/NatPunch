#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新，不影响同机服务端）
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
set -u
REPO="NekoBoxHQ/NatPunch"
FALLBACK_VER="v26.9.4"
ACTION="${1:-uninstall}"
SERVER_DIR="/opt/natpunch"
SERVER_BIN="$SERVER_DIR/natpunch"
SERVER_CONF="$SERVER_DIR/conf/natpunch.conf"
CLIENT_BIN_1="/usr/bin/natpunch"
CLIENT_BIN_2="/usr/local/bin/natpunch"
CLIENT_CONF_1="/etc/natpunch.conf"
CLIENT_CONF_2="/etc/natpunch/natpunch.conf"
CLIENT_CONF_3="/usr/local/etc/natpunch.conf"
CLIENT_INIT="/etc/init.d/natpunch"
CLIENT_SYSTEMD_1="/etc/systemd/system/natpunch.service"
CLIENT_SYSTEMD_2="/lib/systemd/system/natpunch.service"
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }

# ---------- 自启文件归属判定 ----------
# 服务端(install_server.sh)与客户端(install.sh)的 init.d / systemd 文件名
# 都是 natpunch，必须按内容区分，避免卸载/更新客户端时误伤同机服务端。
# 返回: 0=客户端  1=服务端  2=不存在/无法判定
init_owner() {
    f="$1"
    [ -f "$f" ] || return 2
    if grep -q '/opt/natpunch' "$f" 2>/dev/null; then return 1; fi
    if grep -q '/usr/bin/natpunch\|/etc/natpunch.conf' "$f" 2>/dev/null; then return 0; fi
    return 2
}
unit_owner() {
    f="$1"
    [ -f "$f" ] || return 2
    if grep -q 'Description=NatPunch Server\|ExecStart=/opt/natpunch' "$f" 2>/dev/null; then return 1; fi
    if grep -q 'Description=NatPunch Client\|ExecStart=/usr/bin/natpunch' "$f" 2>/dev/null; then return 0; fi
    return 2
}

# ---------- 同机服务端检测 ----------
SERVER_PRESENT=0
[ -f "$SERVER_BIN" ] && SERVER_PRESENT=1
[ -f "$SERVER_CONF" ] && SERVER_PRESENT=1
init_owner "$CLIENT_INIT"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
unit_owner "$CLIENT_SYSTEMD_1"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
unit_owner "$CLIENT_SYSTEMD_2"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        [ "$exe" = "$SERVER_BIN" ] && { SERVER_PRESENT=1; break; }
    done
fi

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
    [ -n "$VER" ] || VER="$FALLBACK_VER"
    log "最新版本: $VER"
    URL="https://github.com/$REPO/releases/download/$VER/$PKG"
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
    BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch | head -n1)"
    [ -n "$BIN_SRC" ] || { warn "压缩包内未找到 natpunch 二进制"; exit 1; }
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
    # 停止客户端服务与残留进程（仅操作属于客户端的自启，不影响同机服务端）
    IO=2
    if [ -f "$CLIENT_INIT" ]; then
        init_owner "$CLIENT_INIT"; IO=$?
    fi
    if [ "$IO" = "0" ]; then
        "$CLIENT_INIT" stop 2>/dev/null || true
    elif [ "$IO" = "1" ]; then
        warn "$CLIENT_INIT 属于服务端，跳过停止"
    fi
    UO=2
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        unit_owner "$U"; UO=$?
        break
    done
    if command -v systemctl >/dev/null 2>&1 && [ "$UO" = "0" ]; then
        systemctl stop natpunch 2>/dev/null || true
    fi
    command -v pkill >/dev/null 2>&1 && pkill -f 'natpunch .*-vkey=' 2>/dev/null || true
    sleep 1
    # 替换二进制（覆盖两处常见安装路径）
    cp -f "$BIN_SRC" "$CLIENT_BIN_1" || { warn "写入 $CLIENT_BIN_1 失败"; exit 1; }
    chmod 755 "$CLIENT_BIN_1"
    [ -f "$CLIENT_BIN_2" ] && { cp -f "$BIN_SRC" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
    # 重新启动（仅当自启归属客户端；服务端 unit 存在时不做任何启停）
    if [ -f /etc/openwrt_release ]; then
        IO=2
        if [ -f "$CLIENT_INIT" ]; then
            init_owner "$CLIENT_INIT"; IO=$?
        fi
        [ "$IO" = "0" ] && "$CLIENT_INIT" start 2>/dev/null || true
    elif command -v systemctl >/dev/null 2>&1; then
        UO=2
        for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
            [ -f "$U" ] || continue
            unit_owner "$U"; UO=$?
            break
        done
        [ "$UO" = "0" ] && { systemctl restart natpunch 2>/dev/null || systemctl start natpunch 2>/dev/null || true; }
    fi
    sleep 3
    VER_OUT="$("$CLIENT_BIN_1" -version 2>/dev/null | head -n1)"
    log "更新完成 ${VER_OUT:-$VER}"
    rm -rf "$TMP_DIR"
    trap - EXIT INT TERM
    exit 0
fi
if [ "$ACTION" != "uninstall" ]; then
    echo "用法: sh uninstall_client.sh [update|uninstall]" >&2
    exit 1
fi

# 1. 精确识别并结束客户端进程（仅匹配带 -vkey= 的客户端，不影响同机服务端）
CLIENT_PIDS=""
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch|*/natpunch\ \(deleted\)) ;;
            *) continue ;;
        esac
        cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
        case "$cmd" in
            *" -vkey="*) CLIENT_PIDS="$CLIENT_PIDS $p" ;;
        esac
    done
else
    CLIENT_PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch' | grep ' -vkey=' | awk '{print $1}')
fi
if [ -f /opt/natpunch/natpunch.pid ]; then
    SPID=$(cat /opt/natpunch/natpunch.pid 2>/dev/null)
    case "$SPID" in
        ''|*[!0-9]*) ;;
        *)
            NEW=""
            for p in $CLIENT_PIDS; do
                [ "$p" = "$SPID" ] && continue
                NEW="$NEW $p"
            done
            CLIENT_PIDS="$NEW"
            ;;
    esac
fi
if [ -n "$CLIENT_PIDS" ]; then
    kill $CLIENT_PIDS 2>/dev/null
    sleep 1
    for p in $CLIENT_PIDS; do
        [ -d "/proc/$p" ] && kill -9 "$p" 2>/dev/null
    done
    sleep 1
fi
# 2. 停止/删除客户端自启（仅操作属于客户端的文件，服务端的一律跳过）
IO=2
if [ -f "$CLIENT_INIT" ]; then
    init_owner "$CLIENT_INIT"; IO=$?
fi
if [ "$IO" = "0" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
    rm -f /etc/rc.d/*natpunch 2>/dev/null
    for f in /etc/rc.d/S??natpunch /etc/rc.d/K??natpunch /etc/rc*.d/S??natpunch /etc/rc*.d/K??natpunch; do
        [ -e "$f" ] || continue
        case "$f" in
            *natpunch-server*) continue ;;
            *natpunch-*) continue ;;
        esac
        rm -f "$f"
    done
elif [ "$IO" = "1" ]; then
    warn "$CLIENT_INIT 属于服务端，跳过删除"
else
    warn "$CLIENT_INIT 归属无法判定，跳过删除（如确需删除请手动处理）"
fi
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        unit_owner "$U"; UO=$?
        if [ "$UO" = "0" ]; then
            systemctl stop natpunch 2>/dev/null || true
            systemctl disable natpunch 2>/dev/null || true
            rm -f "$U"
            systemctl daemon-reload 2>/dev/null || true
        elif [ "$UO" = "1" ]; then
            warn "$U 属于服务端，跳过删除"
        else
            warn "$U 归属无法判定，跳过删除（如确需删除请手动处理）"
        fi
    done
fi
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then
    if [ -f /usr/local/etc/rc.d/natpunch ]; then
        service natpunch stop 2>/dev/null || true
        rm -f /usr/local/etc/rc.d/natpunch
        command -v sysrc >/dev/null 2>&1 && sysrc -x natpunch_enable 2>/dev/null || true
    fi
fi
if [ -d /Library/LaunchDaemons ]; then
    PLIST="/Library/LaunchDaemons/com.natpunch.client.plist"
    if [ -f "$PLIST" ]; then
        launchctl bootout system "$PLIST" 2>/dev/null || true
        launchctl unload "$PLIST" 2>/dev/null || true
        rm -f "$PLIST"
    fi
fi
if [ -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist" ]; then
    launchctl unload "$HOME/Library/LaunchAgents/com.natpunch.client.plist" 2>/dev/null || true
    rm -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist"
fi
# 3. 清理 rc.local
if [ -f /etc/rc.local ]; then
    cp -f /etc/rc.local /etc/rc.local.natpunch-client.bak 2>/dev/null || true
    if sed --version >/dev/null 2>&1; then
        sed -i '/natpunch -server=/d; /natpunch.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    else
        sed -i '' '/natpunch -server=/d; /natpunch.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    fi
    if [ "$SERVER_PRESENT" = "0" ]; then
        if sed --version >/dev/null 2>&1; then
            sed -i '/\/usr\/bin\/natpunch/d; /\/usr\/local\/bin\/natpunch/d' /etc/rc.local 2>/dev/null || true
        else
            sed -i '' '/\/usr\/bin\/natpunch/d; /\/usr\/local\/bin\/natpunch/d' /etc/rc.local 2>/dev/null || true
        fi
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
# 5. 删除客户端二进制（同机存在服务端时保留，避免误删）
if [ "$SERVER_PRESENT" = "0" ]; then
    rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
fi
# 6. 清理日志
rm -f /tmp/natpunch.log /var/log/natpunch.log /tmp/natpunch 2>/dev/null || true
# 7. 复查
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch) ;;
            *) continue ;;
        esac
        cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
        case "$cmd" in
            *" -vkey="*) REMAIN=1; echo "残留 PID $p: $cmd" ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    warn "仍检测到客户端进程，请手动检查"
    exit 1
fi
log "客户端已卸载完成"
exit 0
