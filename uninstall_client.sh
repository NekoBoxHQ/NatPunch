#!/bin/sh
# 卸载 NatPunch 客户端（不影响同机服务端）
# 用法: sh uninstall-client.sh
SERVER_BIN="/opt/natpunch/natpunch"
SERVER_INIT="/etc/init.d/natpunch-server"
SERVER_SYSTEMD="/etc/systemd/system/natpunch-server.service"
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
SERVER_PRESENT=0
[ -f "$SERVER_BIN" ] && SERVER_PRESENT=1
[ -f "$SERVER_INIT" ] && SERVER_PRESENT=1
[ -f "$SERVER_SYSTEMD" ] && SERVER_PRESENT=1
if [ "$SERVER_PRESENT" = "1" ]; then
    log "检测到服务端存在，将以'不影响服务端'模式卸载客户端"
else
    log "未检测到服务端，按独立客户端卸载"
fi
# ---------- 1. 精确识别客户端进程 ----------
log "查找客户端进程 ..."
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
            *" -vkey="*)
                CLIENT_PIDS="$CLIENT_PIDS $p"
                ;;
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
    log "结束客户端进程:$CLIENT_PIDS"
    kill $CLIENT_PIDS 2>/dev/null
    sleep 1
    for p in $CLIENT_PIDS; do
        [ -d "/proc/$p" ] && kill -9 "$p" 2>/dev/null
    done
    sleep 1
else
    log "未发现运行中的客户端进程"
fi
# ---------- 2. 停止/删除客户端自启 ----------
if [ -f "$CLIENT_INIT" ]; then
    log "移除客户端 init 脚本: $CLIENT_INIT"
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
fi
rm -f /etc/rc.d/*natpunch 2>/dev/null
for f in /etc/rc.d/S??natpunch /etc/rc.d/K??natpunch /etc/rc*.d/S??natpunch /etc/rc*.d/K??natpunch; do
    [ -e "$f" ] || continue
    case "$f" in
        *natpunch-server*) continue ;;
        *natpunch-*) continue ;;
    esac
    rm -f "$f"
done
if command -v systemctl >/dev/null 2>&1; then
    if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
        log "移除客户端 systemd unit"
        systemctl stop natpunch 2>/dev/null || true
        systemctl disable natpunch 2>/dev/null || true
        rm -f "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"
        systemctl daemon-reload 2>/dev/null || true
    fi
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
# ---------- 3. 清理 rc.local ----------
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
# ---------- 4. 删除客户端配置 ----------
log "删除客户端配置 ..."
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
# ---------- 5. 删除客户端二进制 ----------
if [ "$SERVER_PRESENT" = "1" ]; then
    log "服务端存在，保留 $CLIENT_BIN_1 $CLIENT_BIN_2"
else
    log "未检测到服务端，删除客户端二进制"
    rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
fi
# ---------- 6. 清理日志 ----------
rm -f /tmp/natpunch.log /var/log/natpunch.log /tmp/natpunch 2>/dev/null || true
# ---------- 7. 复查 ----------
log "复查残留客户端进程 ..."
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
            *" -vkey="*) REMAIN=1; echo "  残留 PID $p: $cmd" ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    warn "仍检测到客户端进程，请手动检查"
    exit 1
fi
log "客户端已卸载完成"
[ "$SERVER_PRESENT" = "1" ] && log "服务端未被触碰（$SERVER_BIN 仍在运行）"
exit 0