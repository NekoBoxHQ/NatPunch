#!/bin/sh
# NatPunch 客户端更新应用脚本（替换+重启）
# 由 uninstall_client.sh 更新流程在断连保护阶段拉取本文件后，脱离会话后台执行。
#
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
# timeout 不存在时（精简环境）直接调用，保证更新流程不被 command not found 打断
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
exit 0
