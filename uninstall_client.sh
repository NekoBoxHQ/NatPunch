#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 仅操作客户端专属命名 natpunch-client，与服务端 natpunch 完全隔离
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
    # —— 拉取 apply 脚本（先就绪，再改 KillMode，避免副作用残留） ——
    APPLY_URL="https://cdn.jsdelivr.net/gh/$REPO@master/update_client_apply.sh"
    if ! fetch_to "$APPLY_URL" /tmp/natpunch_apply.sh || ! head -1 /tmp/natpunch_apply.sh 2>/dev/null | grep -q '^#!'; then
        APPLY_URL="https://raw.githubusercontent.com/$REPO/master/update_client_apply.sh"
        fetch_to "$APPLY_URL" /tmp/natpunch_apply.sh || die "拉取更新脚本失败"
        head -1 /tmp/natpunch_apply.sh 2>/dev/null | grep -q '^#!' || die "更新脚本内容非法"
    fi
    chmod 755 /tmp/natpunch_apply.sh
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
        setsid sh /tmp/natpunch_apply.sh > "$UPDATE_LOG" 2>&1 < /dev/null &
    else
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" \
        NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" \
        NP_CLIENT_INIT="$CLIENT_INIT" \
        NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" \
        nohup sh /tmp/natpunch_apply.sh > "$UPDATE_LOG" 2>&1 < /dev/null &
    fi
    # 清空 trap，避免 EXIT 删掉 $TMP_DIR（由 apply 脚本清理）
    trap - EXIT INT TERM
    exit 0
}
# ---------- 卸载模式 ----------
do_uninstall() {
    log "卸载 NatPunch 客户端..."
    rm -f "$DONE_FILE"
    # —— 拉取 apply 脚本（先就绪，再改 KillMode） ——
    UN_URL="https://cdn.jsdelivr.net/gh/$REPO@master/uninstall_client_apply.sh"
    if ! fetch_to "$UN_URL" /tmp/natpunch_uninstall.sh || ! head -1 /tmp/natpunch_uninstall.sh 2>/dev/null | grep -q '^#!'; then
        UN_URL="https://raw.githubusercontent.com/$REPO/master/uninstall_client_apply.sh"
        fetch_to "$UN_URL" /tmp/natpunch_uninstall.sh || die "拉取卸载脚本失败"
        head -1 /tmp/natpunch_uninstall.sh 2>/dev/null | grep -q '^#!' || die "卸载脚本内容非法"
    fi
    chmod 755 /tmp/natpunch_uninstall.sh
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
        setsid sh /tmp/natpunch_uninstall.sh >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
    else
        NP_DONE_FILE="$DONE_FILE" \
        nohup sh /tmp/natpunch_uninstall.sh >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
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
