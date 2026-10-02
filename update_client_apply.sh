#!/bin/sh
# NatPunch 客户端更新应用脚本（替换+重启）
# 由 uninstall_client.sh 更新流程在断连保护阶段拉取本文件后，脱离会话后台执行。
# 环境变量（由调用方注入）：
#   NP_TMP_DIR / NP_BIN_SRC / NP_CLIENT_BIN_1 / NP_CLIENT_BIN_2
#   NP_CLIENT_INIT / NP_CLIENT_SYSTEMD_1 / NP_CLIENT_SYSTEMD_2
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
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
# 验证客户端已恢复运行
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
    log "更新完成"
else
    warn "更新完成但客户端未运行，请检查日志: /tmp/natpunch_update.log"
fi
rm -rf "$TMP_DIR"
exit 0
