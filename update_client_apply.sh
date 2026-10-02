#!/bin/sh
# NatPunch 客户端更新应用脚本（替换+重启）
# 由 uninstall_client.sh 更新流程在断连保护阶段拉取本文件后，脱离会话后台执行。
# 环境变量（由调用方注入）：
#   NP_TMP_DIR / NP_BIN_SRC / NP_CLIENT_BIN_1 / NP_CLIENT_BIN_2
#   NP_CLIENT_INIT / NP_CLIENT_SYSTEMD_1 / NP_CLIENT_SYSTEMD_2
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
log "开始应用更新"
TMP_DIR="$NP_TMP_DIR"
BIN_SRC="$NP_BIN_SRC"
CLIENT_BIN_1="$NP_CLIENT_BIN_1"
CLIENT_BIN_2="$NP_CLIENT_BIN_2"
CLIENT_INIT="$NP_CLIENT_INIT"
CLIENT_SYSTEMD_1="$NP_CLIENT_SYSTEMD_1"
CLIENT_SYSTEMD_2="$NP_CLIENT_SYSTEMD_2"
# 停止客户端服务与残留进程（natpunch-client 专属名称，不影响同机服务端）
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
    if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
        systemctl stop natpunch-client 2>/dev/null || true
    fi
fi
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch-client|*/natpunch-client\ \(deleted\)) kill "$p" 2>/dev/null || true ;;
        esac
    done
    sleep 1
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch-client|*/natpunch-client\ \(deleted\)) kill -9 "$p" 2>/dev/null || true ;;
        esac
    done
else
    ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}' | while read -r p; do kill "$p" 2>/dev/null || true; done
    sleep 1
    ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}' | while read -r p; do kill -9 "$p" 2>/dev/null || true; done
fi
# 替换二进制（先备份旧版，用于启动失败自动回滚）
[ -f "$CLIENT_BIN_1" ] && cp -f "$CLIENT_BIN_1" "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
cp -f "$BIN_SRC" "$CLIENT_BIN_1" || { warn "写入 $CLIENT_BIN_1 失败"; exit 1; }
chmod 755 "$CLIENT_BIN_1"
[ -f "$CLIENT_BIN_2" ] && { cp -f "$BIN_SRC" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }

# 启动检测：/proc 遍历或 ps，识别 natpunch-client 进程
client_running() {
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$exe" in
                */natpunch-client|*/natpunch-client\ \(deleted\)) return 0 ;;
            esac
        done
        return 1
    fi
    ps w 2>/dev/null | grep -v grep | grep -q 'natpunch-client'
}

# 启动客户端：init.d / systemd restart → start（restart 失败等 2 秒避开 systemd 竞态）
start_client() {
    if [ -f /etc/openwrt_release ]; then
        if [ -f "$CLIENT_INIT" ]; then
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
            "$CLIENT_INIT" start 2>/dev/null || true
        fi
    elif command -v systemctl >/dev/null 2>&1; then
        # systemctl 偶发卡死（systemd 默认超时 ~90s），后台执行 + 8s 超时，卡住立即放弃走下一层
        systemctl restart natpunch-client >/dev/null 2>&1 &
        R=$!
        sleep 8
        client_running && return 0
        kill "$R" 2>/dev/null || true
        systemctl start natpunch-client >/dev/null 2>&1 &
        S=$!
        sleep 8
        client_running && return 0
        kill "$S" 2>/dev/null || true
    fi
    # 兜底：直接后台运行二进制（配置在 /etc/natpunch.conf，字段为小写 server/port/vkey/tls_enable）
    if [ -f /etc/natpunch.conf ]; then
        . /etc/natpunch.conf 2>/dev/null
        SRV="${server:-$SERVER}"
        PRT="${port:-$PORT}"
        VKY="${vkey:-$VKEY}"
        TLS="${tls_enable:-$TLS_ENABLE}"
        [ -z "$PRT" ] && PRT=8024
        CMD="$CLIENT_BIN_1 -server=${SRV:-}:$PRT -vkey=${VKY:-} -type=tcp"
        [ "$TLS" = "true" ] && CMD="$CMD -tls_enable=true"
        nohup $CMD >> /tmp/natpunch-client.log 2>&1 &
        sleep 2
        client_running && return 0
    fi
    return 1
}

# 启动（含失败回滚：新版本起不来 → 恢复旧二进制再启动，保证不失联）
log "替换完成，启动客户端..."
if ! start_client; then
    if [ -f "${CLIENT_BIN_1}.update_bak" ]; then
        warn "新版本启动失败，自动回滚旧版本"
        cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_1"
        chmod 755 "$CLIENT_BIN_1"
        [ -f "$CLIENT_BIN_2" ] && { cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
        start_client && log "更新完成（回滚后启动）" || warn "回滚后仍无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
    else
        warn "客户端无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
    fi
else
    log "更新完成"
fi
rm -rf "$TMP_DIR"
exit 0
