#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 仅操作客户端专属命名 natpunch-client，与服务端 natpunch 完全隔离
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
#
# 关键设计：后台执行用的 apply 脚本由本脚本内嵌生成（heredoc），
# 与主脚本同版本同源，绝不从网络拉取——杜绝 CDN 缓存旧版导致的
# "新版主脚本 + 旧版 apply" 混搭（曾导致 done 判定失效、卸载提示失真）。
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
DONE_FILE="/tmp/natpunch_uninstall.done"
UPDATE_LOG="/tmp/natpunch_update.log"
UNINSTALL_LOG="/tmp/natpunch_uninstall.log"
log()  { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
die()  { echo "==> 错误: $*" >&2; exit 1; }
# timeout 不存在时（精简 Linux）直接调用，保证 KillMode 生效不被 command not found 打断
tcmd() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "$@"
    else
        N="$1"; shift; "$@"
    fi
}
# ---------- 下载工具 ----------
HAS_WGET=0; HAS_CURL=0
command -v wget >/dev/null 2>&1 && HAS_WGET=1
command -v curl >/dev/null 2>&1 && HAS_CURL=1
[ "$HAS_WGET" -eq 1 ] || [ "$HAS_CURL" -eq 1 ] || die "需要 wget 或 curl"
fetch_to() {
    # fetch_to <url> <out>
    if [ "$HAS_WGET" -eq 1 ]; then
        wget -q --timeout=15 --tries=2 -O "$2" "$1" 2>/dev/null || return 1
    else
        curl -fsSL --max-time 30 --retry 2 -o "$2" "$1" || return 1
    fi
    [ -s "$2" ] || return 1
    return 0
}
# ---------- 修改 systemd unit 的 KillMode ----------
# 使用 grep + echo 追加，避免 busybox sed 不支持 a 命令
ensure_killmode_process() {
    U="$1"
    [ -f "$U" ] || return 0
    if grep -q '^KillMode=' "$U" 2>/dev/null; then
        # 用 awk 原地替换，busybox 兼容
        awk 'BEGIN{FS=OFS="="} /^KillMode=/{print "KillMode","process"; next} {print}' "$U" > "$U.tmp" \
            && mv "$U.tmp" "$U" || rm -f "$U.tmp"
    else
        # 优先插到 RestartSec 之后，否则追加到末尾
        if grep -q '^RestartSec=' "$U" 2>/dev/null; then
            awk '/^RestartSec=/{print; print "KillMode=process"; next} {print}' "$U" > "$U.tmp" \
                && mv "$U.tmp" "$U" || rm -f "$U.tmp"
        else
            echo "KillMode=process" >> "$U"
        fi
    fi
}
# ---------- 更新模式 ----------
do_update() {
    log "更新 NatPunch 客户端（保留配置）..."
    if [ ! -f "$CLIENT_BIN_1" ] && [ ! -f "$CLIENT_BIN_2" ]; then
        warn "未检测到已安装客户端，请使用 install.sh 全新安装"
        exit 1
    fi
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   PKG="linux_amd64_client.tar.gz" ;;
        aarch64|arm64)  PKG="linux_arm64_client.tar.gz" ;;
        *) die "不支持架构: $ARCH（当前仅支持 x86_64 / arm64）" ;;
    esac
    VER=""
    if [ "$HAS_WGET" -eq 1 ]; then
        VER="$(wget -qO- --timeout=10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
            | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    else
        VER="$(curl -fsSL --max-time 10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
            | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    fi
    log "最新版本: ${VER:-最新发布}"
    URL="https://github.com/$REPO/releases/latest/download/$PKG"
    TMP_DIR="/tmp/natpunch_update.$$"
    rm -rf "$TMP_DIR"
    mkdir -p "$TMP_DIR" || die "无法创建临时目录"
    # 下载/解压阶段失败时自动清理临时目录；
    # 后台 apply 脚本就绪并 setsid 启动后，由 apply 脚本负责清理，这里再解除 trap
    trap 'rm -rf "$TMP_DIR" 2>/dev/null || true' EXIT INT TERM
    fetch_to "$URL" "$TMP_DIR/pkg.tar.gz" || die "下载失败: $URL"
    tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || die "压缩包损坏"
    tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || die "解压失败"
    BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client 2>/dev/null | head -n1)"
    [ -n "${BIN_SRC:-}" ] || die "压缩包内未找到 natpunch-client 二进制"
    # —— 内嵌生成 update apply 脚本（唯一 PID 副本，与主脚本同版本，无外部拉取） ——
    APPLY="/tmp/natpunch_apply.$$"
    cat > "$APPLY" <<'APPLY'
#!/bin/sh
# NatPunch 客户端更新应用脚本（替换+重启）
# 由 uninstall_client.sh 更新流程内嵌生成后脱离会话后台执行。
# 环境变量（由调用方注入）：
#   NP_TMP_DIR / NP_BIN_SRC / NP_CLIENT_BIN_1 / NP_CLIENT_BIN_2
#   NP_CLIENT_INIT / NP_CLIENT_SYSTEMD_1 / NP_CLIENT_SYSTEMD_2
set -u
log()  { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
TMP_DIR="${NP_TMP_DIR:-}"
BIN_SRC="${NP_BIN_SRC:-}"
CLIENT_BIN_1="${NP_CLIENT_BIN_1:-/usr/bin/natpunch-client}"
CLIENT_BIN_2="${NP_CLIENT_BIN_2:-/usr/local/bin/natpunch-client}"
CLIENT_INIT="${NP_CLIENT_INIT:-/etc/init.d/natpunch-client}"
CLIENT_SYSTEMD_1="${NP_CLIENT_SYSTEMD_1:-/etc/systemd/system/natpunch-client.service}"
CLIENT_SYSTEMD_2="${NP_CLIENT_SYSTEMD_2:-/lib/systemd/system/natpunch-client.service}"
[ -n "$BIN_SRC" ] && [ -f "$BIN_SRC" ] || { warn "NP_BIN_SRC 无效: $BIN_SRC"; exit 1; }
[ -n "$TMP_DIR" ] || { warn "NP_TMP_DIR 为空"; exit 1; }
log "开始应用更新"
log "源文件: $BIN_SRC"
log "目标:   $CLIENT_BIN_1"
# ---------- 工具 ----------
tcmd() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "$@"
    else
        N="$1"; shift; "$@"
    fi
}
ensure_killmode_process() {
    U="$1"
    [ -f "$U" ] || return 0
    if grep -q '^KillMode=' "$U" 2>/dev/null; then
        awk 'BEGIN{FS=OFS="="} /^KillMode=/{print "KillMode","process"; next} {print}' "$U" > "$U.tmp" \
            && mv "$U.tmp" "$U" || rm -f "$U.tmp"
    else
        if grep -q '^RestartSec=' "$U" 2>/dev/null; then
            awk '/^RestartSec=/{print; print "KillMode=process"; next} {print}' "$U" > "$U.tmp" \
                && mv "$U.tmp" "$U" || rm -f "$U.tmp"
        else
            echo "KillMode=process" >> "$U"
        fi
    fi
}
client_running() {
    SELF_PID=$$
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$SELF_PID" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)") return 0 ;;
            esac
        done
        return 1
    fi
    ps w 2>/dev/null | grep -v grep | grep -q 'natpunch-client'
}
kill_client_pids() {
    SIGNAL="$1"
    SELF_PID=$$
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$SELF_PID" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)")
                    kill $SIGNAL "$p" 2>/dev/null || true
                    ;;
            esac
        done
    else
        ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}' | while read -r p; do
            [ "$p" = "$SELF_PID" ] && continue
            kill $SIGNAL "$p" 2>/dev/null || true
        done
    fi
}
# ---------- 1. 替换二进制（先备份） ----------
[ -f "$CLIENT_BIN_1" ] && cp -f "$CLIENT_BIN_1" "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
if ! cp -f "$BIN_SRC" "$CLIENT_BIN_1"; then
    warn "写入 $CLIENT_BIN_1 失败，尝试回滚"
    [ -f "${CLIENT_BIN_1}.update_bak" ] && cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_1"
    exit 1
fi
chmod 755 "$CLIENT_BIN_1"
if [ -f "$CLIENT_BIN_2" ]; then
    cp -f "$BIN_SRC" "$CLIENT_BIN_2" && chmod 755 "$CLIENT_BIN_2" || true
fi
# ---------- 2. 修正 KillMode + daemon-reload（超时保护） ----------
if command -v systemctl >/dev/null 2>&1; then
    ensure_killmode_process "$CLIENT_SYSTEMD_1"
    ensure_killmode_process "$CLIENT_SYSTEMD_2"
    tcmd 10 systemctl daemon-reload >/dev/null 2>&1 || true
fi
# ---------- 3. systemd-run 独立单元只做 stop（避开 cgroup 连带杀） ----------
if command -v systemctl >/dev/null 2>&1 && command -v systemd-run >/dev/null 2>&1; then
    if systemd-run --unit="natpunch-apply-stop-$$" --collect --wait --quiet \
            /bin/sh -c "systemctl stop natpunch-client" >/dev/null 2>&1; then
        log "已通过独立单元停止客户端"
        sleep 1
        # stop 后 apply 脚本已脱离客户端 cgroup，可以安全 start
        if tcmd 15 systemctl start natpunch-client >/dev/null 2>&1; then
            sleep 2
            if client_running; then
                log "更新完成（独立单元停止 + 常规启动）"
                rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
                rm -rf "$TMP_DIR"
                exit 0
            fi
            warn "systemctl start 成功但进程未运行，进入常规流程"
        else
            warn "systemctl start 失败，进入常规流程"
        fi
    else
        warn "systemd-run stop 失败，回退常规流程"
    fi
fi
# ---------- 4. 常规流程（OpenWrt init.d / 无 systemd-run） ----------
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
    if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
        tcmd 15 systemctl stop natpunch-client 2>/dev/null || true
    fi
fi
# 兜底杀进程
kill_client_pids TERM
sleep 1
kill_client_pids KILL
sleep 1
# ---------- 5. 启动客户端 ----------
start_client() {
    if [ -f /etc/openwrt_release ]; then
        if [ -f "$CLIENT_INIT" ]; then
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
        fi
    elif command -v systemctl >/dev/null 2>&1; then
        tcmd 15 systemctl restart natpunch-client >/dev/null 2>&1 || true
        sleep 2
        client_running && return 0
        tcmd 15 systemctl start natpunch-client >/dev/null 2>&1 || true
        sleep 2
        client_running && return 0
    fi
    # 兜底：直接后台运行二进制
    if [ -f /etc/natpunch.conf ]; then
        SRV=""; PRT=""; VKY=""; TLS=""
        # 安全解析配置：不 source，只 grep 取值，避免配置内容被 shell 解释
        get_conf() {
            grep -E "^[[:space:]]*$1[[:space:]]*=" /etc/natpunch.conf 2>/dev/null \
                | head -n1 | sed "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*//"
        }
        SRV="$(get_conf SERVER)"; [ -n "$SRV" ] || SRV="$(get_conf server)"
        PRT="$(get_conf PORT)";   [ -n "$PRT" ] || PRT="$(get_conf port)"
        VKY="$(get_conf VKEY)";   [ -n "$VKY" ] || VKY="$(get_conf vkey)"
        TLS="$(get_conf TLS_FLAG)"
        [ -z "$PRT" ] && PRT=8024
        [ -z "$SRV" ] || [ -z "$VKY" ] && { warn "配置缺少 SERVER 或 VKEY"; return 1; }
        # 参数化启动，不用 eval：SERVER/VKEY 仅作为参数传给二进制，
        # 不会被 shell 解释，杜绝配置文件内容注入命令
        TLS_ARG=""
        case "$TLS" in
            *tls_enable=true*|*tls=true*|true|1) TLS_ARG="-tls_enable=true" ;;
        esac
        # shellcheck disable=SC2086（TLS_ARG 为空或固定白名单值）
        nohup "$CLIENT_BIN_1" -server="$SRV:$PRT" -vkey="$VKY" -type=tcp $TLS_ARG >> /tmp/natpunch-client.log 2>&1 &
        sleep 2
        client_running && return 0
    fi
    return 1
}
log "替换完成，启动客户端..."
if ! start_client; then
    if [ -f "${CLIENT_BIN_1}.update_bak" ]; then
        warn "新版本启动失败，自动回滚旧版本"
        cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_1"
        chmod 755 "$CLIENT_BIN_1"
        [ -f "$CLIENT_BIN_2" ] && { cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
        if start_client; then
            log "更新完成（回滚后启动）"
        else
            warn "回滚后仍无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
        fi
    else
        warn "客户端无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
    fi
else
    log "更新完成"
    rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
fi
rm -rf "$TMP_DIR"
# 自删唯一副本
(
    sleep 1
    rm -f "$0"
) &
exit 0
APPLY
    chmod 755 "$APPLY"
    # —— KillMode=process：apply 脚本就绪后再改，失败也不影响 ——
    if command -v systemctl >/dev/null 2>&1; then
        ensure_killmode_process "$CLIENT_SYSTEMD_1"
        ensure_killmode_process "$CLIENT_SYSTEMD_2"
        tcmd 10 systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    echo "==> 升级文件已就绪，流程转入后台执行"
    echo "==> SSH 断开后自动完成替换与重启，日志: $UPDATE_LOG"
    echo "==> 完成后客户端自动重启，隧道恢复后请重新连接"
    # —— setsid 彻底脱离会话 ——
    if command -v setsid >/dev/null 2>&1; then
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" \
        NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" \
        NP_CLIENT_INIT="$CLIENT_INIT" \
        NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" \
        setsid sh "$APPLY" > "$UPDATE_LOG" 2>&1 < /dev/null &
    else
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" \
        NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" \
        NP_CLIENT_INIT="$CLIENT_INIT" \
        NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" \
        nohup sh "$APPLY" > "$UPDATE_LOG" 2>&1 < /dev/null &
    fi
    # 清空 trap，避免 EXIT 删掉 $TMP_DIR（由 apply 脚本清理）
    trap - EXIT INT TERM
    exit 0
}
# ---------- 卸载模式 ----------
do_uninstall() {
    log "卸载 NatPunch 客户端..."
    rm -f "$DONE_FILE"
    # —— 内嵌生成 uninstall apply 脚本（唯一 PID 副本，与主脚本同版本，无外部拉取） ——
    APPLY="/tmp/natpunch_uninstall.$$"
    cat > "$APPLY" <<'APPLY'
#!/bin/sh
# NatPunch 客户端卸载应用脚本（静默完整卸载）
# 由 uninstall_client.sh 卸载模式内嵌生成后在后台执行（setsid/nohup 脱离当前会话）：
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
#      不碰固定路径，避免误删下次新生成的 apply 脚本）
(
    sleep 1
    rm -f "$0"
) &
exit 0
APPLY
    chmod 755 "$APPLY"
    # —— KillMode=process：避免 systemd stop 连带杀掉 apply 脚本 ——
    if command -v systemctl >/dev/null 2>&1; then
        ensure_killmode_process "$CLIENT_SYSTEMD_1"
        ensure_killmode_process "$CLIENT_SYSTEMD_2"
        tcmd 10 systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    # —— setsid 静默后台执行 ——
    # 用 done 文件判定完成，不依赖 $!（setsid 会 fork，$! 不可靠）
    if command -v setsid >/dev/null 2>&1; then
        NP_DONE_FILE="$DONE_FILE" \
        setsid sh "$APPLY" >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
    else
        NP_DONE_FILE="$DONE_FILE" \
        nohup sh "$APPLY" >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
    fi
    # 等待 done 文件（最多 60s）
    i=0
    while [ ! -f "$DONE_FILE" ] && [ "$i" -lt 60 ]; do
        sleep 1
        i=$((i+1))
    done
    if [ -f "$DONE_FILE" ]; then
        log "客户端已卸载完成"
    else
        warn "卸载仍在后台进行（可能正通过隧道卸载自身），请稍后直连确认"
        warn "日志: $UNINSTALL_LOG"
    fi
    exit 0
}
# ---------- 入口 ----------
case "$ACTION" in
    update)    do_update ;;
    uninstall) do_uninstall ;;
    *) echo "用法: sh uninstall_client.sh [update|uninstall]" >&2; exit 1 ;;
esac
