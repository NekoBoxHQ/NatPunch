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

# 1. 替换二进制（stop 前完成：运行中进程不受磁盘替换影响；先备份旧版用于失败回滚）
[ -f "$CLIENT_BIN_1" ] && cp -f "$CLIENT_BIN_1" "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
cp -f "$BIN_SRC" "$CLIENT_BIN_1" || { warn "写入 $CLIENT_BIN_1 失败"; exit 1; }
chmod 755 "$CLIENT_BIN_1"
[ -f "$CLIENT_BIN_2" ] && { cp -f "$BIN_SRC" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }

# 2. Linux systemd：用 systemd-run 在独立 cgroup 里执行"停止+启动"，
#    彻底避免 systemctl stop 连带杀掉更新进程（面板 SSH 场景本脚本位于客户端服务 cgroup 内）。
if command -v systemctl >/dev/null 2>&1 && command -v systemd-run >/dev/null 2>&1; then
    # 尽力修正 KillMode=process（双保险，不依赖它）
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        if [ -f "$U" ]; then
            if grep -q '^KillMode=' "$U" 2>/dev/null; then
                sed -i 's/^KillMode=.*/KillMode=process/' "$U" 2>/dev/null || true
            else
                sed -i '/^RestartSec=/a KillMode=process' "$U" 2>/dev/null || true
                grep -q '^KillMode=process' "$U" || echo "KillMode=process" >> "$U"
            fi
        fi
    done
    systemctl daemon-reload >/dev/null 2>&1 &
    DR=$!
    sleep 3
    kill "$DR" 2>/dev/null || true
    # systemd-run 独立单元：stop → sleep 1 → start（独立 cgroup，stop 杀不到它；--collect 自动清理）
    systemd-run --unit="natpunch-apply-$$" --collect --no-block /bin/sh -c "systemctl stop natpunch-client; sleep 1; systemctl start natpunch-client" >/dev/null 2>&1 \
        && { log "更新完成（独立单元重启）"; rm -rf "$TMP_DIR"; exit 0; }
    warn "systemd-run 提交失败，回退常规流程"
fi

# 3. 常规流程（OpenWrt init.d / 无 systemd-run 的 Linux）：停止 → 清理残留 → 启动（含兜底与回滚）
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

# 启动客户端：init.d / systemd restart → start（后台执行 + 超时，卡住立即放弃）；最后 nohup 直接跑二进制兜底
start_client() {
    if [ -f /etc/openwrt_release ]; then
        if [ -f "$CLIENT_INIT" ]; then
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
            "$CLIENT_INIT" start 2>/dev/null || true
        fi
    elif command -v systemctl >/dev/null 2>&1; then
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
    # 兜底：直接后台运行二进制（配置在 /etc/natpunch.conf，兼容大小写字段）
    if [ -f /etc/natpunch.conf ]; then
        . /etc/natpunch.conf 2>/dev/null
        SRV="${server:-$SERVER}"
        PRT="${port:-$PORT}"
        VKY="${vkey:-$VKEY}"
        TLS="${tls_enable:-$TLS_ENABLE}"
        # install.sh 写入配置的是 TLS_FLAG 键（值 -tls_enable=true 或空），兼容该形式
        [ -z "$TLS" ] && [ "${TLS_FLAG:-}" = "-tls_enable=true" ] && TLS="true"
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
