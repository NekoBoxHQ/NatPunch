#!/bin/sh
# NatPunch 客户端卸载应用脚本（静默完整卸载）
# 由 uninstall_client.sh 卸载模式拉取后在后台执行（setsid/nohup 脱离当前会话）：
# 面板 SSH 会话由客户端隧道承载，杀客户端即断连；后台执行确保清理完整、无残留。
#
# 契约（与 uninstall_client.sh 主脚本约定）：
#   1. 完成后必须 touch "$NP_DONE_FILE"（默认 /tmp/natpunch_uninstall.done）
#   2. 所有输出写 "$LOG"（/tmp/natpunch_uninstall.log），stdout 保持干净
#   3. 不读 stdin
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
LOG="${NP_LOG:-/tmp/natpunch_uninstall.log}"
DONE_FILE="${NP_DONE_FILE:-/tmp/natpunch_uninstall.done}"
log() { echo "$(date '+%F %T') $*" >> "$LOG"; }
# 退出时一定写 done 文件，保证主脚本不超时
finish() {
    touch "$DONE_FILE" 2>/dev/null || true
}
trap 'finish' EXIT INT TERM
# ---------- 客户端进程精确识别 ----------
get_client_pids() {
    PIDS=""
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            # 处理 "(deleted)" 后缀
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)") PIDS="$PIDS $p" ;;
            esac
        done
    else
        PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}')
    fi
    echo "$PIDS"
}
kill_client_pids() {
    PIDS=$(get_client_pids)
    [ -z "${PIDS:-}" ] && return 0
    for p in $PIDS; do kill "$p" 2>/dev/null || true; done
    sleep 1
    for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true; done
    sleep 1
}
# ================= 静默完整卸载 =================
log "开始卸载 natpunch-client"
# 1. 精确结束客户端进程
kill_client_pids
# 2. OpenWrt init.d
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
fi
# 只删精确匹配的 rc.d 软链，不用 *natpunch-client 通配
for f in /etc/rc.d/S??natpunch-client /etc/rc.d/K??natpunch-client \
         /etc/rc*.d/S??natpunch-client /etc/rc*.d/K??natpunch-client; do
    [ -e "$f" ] || continue
    rm -f "$f"
done
# 3. systemd
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        systemctl stop natpunch-client 2>/dev/null || true
        systemctl disable natpunch-client 2>/dev/null || true
        rm -f "$U"
    done
    systemctl daemon-reload 2>/dev/null || true
fi
# 4. FreeBSD rc.d
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then
    if [ -f /usr/local/etc/rc.d/natpunch-client ]; then
        service natpunch-client stop 2>/dev/null || true
        rm -f /usr/local/etc/rc.d/natpunch-client
        command -v sysrc >/dev/null 2>&1 && sysrc -x natpunch_client_enable 2>/dev/null || true
    fi
fi
# 5. macOS launchd
if [ -d /Library/LaunchDaemons ] && [ -f "$CLIENT_PLIST" ]; then
    launchctl bootout system "$CLIENT_PLIST" 2>/dev/null || true
    launchctl unload "$CLIENT_PLIST" 2>/dev/null || true
    rm -f "$CLIENT_PLIST"
fi
if [ -f "$CLIENT_AGENT" ]; then
    launchctl unload "$CLIENT_AGENT" 2>/dev/null || true
    rm -f "$CLIENT_AGENT"
fi
# 6. 清理 rc.local（仅 natpunch-client 行，备份不覆盖）
if [ -f /etc/rc.local ]; then
    BAK="/etc/rc.local.natpunch-client.bak"
    [ -f "$BAK" ] || cp -f /etc/rc.local "$BAK" 2>/dev/null || true
    if sed --version >/dev/null 2>&1; then
        sed -i '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    else
        sed -i '' '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    fi
fi
# 7. 删除客户端配置
rm -f "$CLIENT_CONF_1" "$CLIENT_CONF_2" "$CLIENT_CONF_3"
# 仅当目录内无服务端残留时才删目录
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
# 8. 删除客户端二进制
rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
# 9. 清理客户端专属日志与临时文件
rm -f /tmp/natpunch-client.log /var/log/natpunch-client.log \
      /tmp/natpunch_update.log /tmp/natpunch_apply.sh 2>/dev/null || true
# 注意：不删 /tmp/natpunch_update.* 目录，可能正在被更新流程使用；
# 更新流程自身会清理自己的 TMP_DIR。
# 10. 复查残留（仅写日志）
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        base=$(basename "$exe" 2>/dev/null)
        case "$base" in
            "natpunch-client"|"natpunch-client (deleted)")
                REMAIN=1
                log "残留 PID $p: $(tr '\0' ' ' < "$d/cmdline" 2>/dev/null)"
                ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    log "仍检测到客户端进程，请手动检查"
else
    log "卸载完成，无残留"
fi
# 11. 自删（延迟到 shell 退出后；只删当前运行的唯一副本 $0，
#      不碰固定路径 /tmp/natpunch_uninstall.sh，
#      避免上一次卸载的自删子进程误删本次刚拉取的新脚本）
(
    sleep 1
    rm -f "$0"
) &
exit 0
