#!/bin/sh
# NatPunch 客户端卸载应用脚本（静默完整卸载）
# 由 uninstall_client.sh 卸载模式拉取后在后台执行（setsid/nohup 脱离当前会话）：
# 面板 SSH 会话由客户端隧道承载，杀客户端即断连；后台执行确保清理完整、无残留。
# 日志: /tmp/natpunch_uninstall.log（仅残留/异常时记录，正常卸载无输出）
set -u
CLIENT_BIN_1="/usr/bin/natpunch-client"
CLIENT_BIN_2="/usr/local/bin/natpunch-client"
CLIENT_CONF_1="/etc/natpunch.conf"
CLIENT_CONF_2="/etc/natpunch/natpunch.conf"
CLIENT_CONF_3="/usr/local/etc/natpunch.conf"
CLIENT_INIT="/etc/init.d/natpunch-client"
CLIENT_SYSTEMD_1="/etc/systemd/system/natpunch-client.service"
CLIENT_SYSTEMD_2="/lib/systemd/system/natpunch-client.service"
CLIENT_PLIST="/Library/LaunchDaemons/com.natpunch.client.plist"
CLIENT_AGENT="${HOME:-}/Library/LaunchAgents/com.natpunch.client.plist"
LOG="/tmp/natpunch_uninstall.log"
log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

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

# ================= 静默完整卸载 =================
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
    fi
fi
if [ -d /usr/local/etc/natpunch ]; then
    if [ ! -f /usr/local/etc/natpunch/natpunch ]; then
        rm -rf /usr/local/etc/natpunch
    fi
fi
# 5. 删除客户端二进制
rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
# 6. 清理客户端日志（仅 natpunch-client 专属，不动服务端 natpunch 的 /tmp/natpunch.log 等）
rm -f /tmp/natpunch-client.log /var/log/natpunch-client.log /tmp/natpunch_update.log /tmp/natpunch_apply.sh /tmp/natpunch_update.* 2>/dev/null || true
# 7. 复查残留（仅写日志，不打扰终端）
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch-client) REMAIN=1; log "残留 PID $p: $(tr '\0' ' ' < "$d/cmdline" 2>/dev/null)" ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    log "仍检测到客户端进程，请手动检查"
else
    log "卸载完成，无残留"
fi
rm -f /tmp/natpunch_uninstall.sh
exit 0
