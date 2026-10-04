#!/bin/sh
# NatPunch 客户端一键安装（OpenWrt / Linux）
# 客户端命名为 natpunch-client，与服务端 natpunch 完全隔离（进程名/自启名/二进制名均不同），互不影响
#
# 用法:
#   sh install.sh --openwrt VKEY SERVER [PORT] [TLS_FLAG]
#   sh install.sh VKEY SERVER [PORT] [TLS_FLAG]
#
# 示例:
#   sh install.sh --openwrt myvkey 1.2.3.4 8024
#   sh install.sh --openwrt myvkey example.com 8024 "-tls=true"
#
set -u
REPO="NekoBoxHQ/NatPunch"
CONF="/etc/natpunch.conf"
BIN="/usr/bin/natpunch-client"
INIT="/etc/init.d/natpunch-client"
UNIT="/etc/systemd/system/natpunch-client.service"
TMP_DIR="/tmp/natpunch_install.$$"
LOG="/tmp/natpunch-client.log"
START_TIMEOUT=15
log()  { echo "==> $*"; }
warn() { echo "!!  $*" >&2; }
die()  { echo "错误: $*" >&2; exit 1; }
cleanup() {
    [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ] && rm -rf "$TMP_DIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM
# ---------- 参数解析 ----------
# 兼容三种写法：带 -- 分隔、带 --openwrt 前缀、或都不带（推荐）
# --openwrt 仅为历史兼容标记，安装时自动检测 /etc/openwrt_release，带不带行为完全一致
if [ "${1:-}" = "--" ]; then
    shift
fi
if [ "${1:-}" = "--openwrt" ]; then
    shift
fi
VKEY="${1:-}"
SERVER="${2:-}"
PORT="${3:-8024}"
TLS_FLAG="${4:-}"
if [ -z "$VKEY" ] || [ -z "$SERVER" ]; then
    echo "用法: sh install.sh --openwrt VKEY SERVER [PORT] [TLS_FLAG]"
    exit 1
fi
case "$PORT" in
    ''|*[!0-9]*) die "PORT 必须是数字: $PORT";;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    die "PORT 超出范围: $PORT"
fi
case "$VKEY" in
    *[!A-Za-z0-9._-]*) die "VKEY 含非法字符";;
esac
# SERVER 只允许 host / IPv4 / IPv6 字面量，禁止 ':' 以免和 PORT 拼接歧义
case "$SERVER" in
    *[!A-Za-z0-9._-]*) die "SERVER 含非法字符（仅允许字母数字 . _ -）";;
esac
case "$TLS_FLAG" in
    *[!A-Za-z0-9._=-]*) die "TLS_FLAG 含非法字符";;
esac
# ---------- 环境检测 ----------
IS_OPENWRT=0
[ -f /etc/openwrt_release ] && IS_OPENWRT=1
log "检测架构..."
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)   PKG="linux_amd64_client.tar.gz";;
    aarch64|arm64)  PKG="linux_arm64_client.tar.gz";;
    *) die "不支持架构: $ARCH";;
esac
echo "    $ARCH -> $PKG"
# ---------- 下载工具探测 ----------
HAS_WGET=0
HAS_CURL=0
command -v wget >/dev/null 2>&1 && HAS_WGET=1
command -v curl >/dev/null 2>&1 && HAS_CURL=1
[ "$HAS_WGET" -eq 1 ] || [ "$HAS_CURL" -eq 1 ] || die "需要 wget 或 curl"
fetch() {
    # fetch <url> <out>（带超时与重试，避免网络挂起阻塞安装）
    if [ "$HAS_WGET" -eq 1 ]; then
        wget -q --timeout=30 --tries=2 -O "$2" "$1"
    else
        curl -fsSL --max-time 60 --retry 2 -o "$2" "$1"
    fi
}
# ---------- 获取版本（仅用于显示） ----------
log "获取最新版本..."
VER=""
if [ "$HAS_WGET" -eq 1 ]; then
    VER="$(wget -qO- --timeout=10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
        | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
else
    VER="$(curl -fsSL --max-time 10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
        | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
fi
echo "    ${VER:-最新发布}"
# ---------- 下载 ----------
URL="https://github.com/$REPO/releases/latest/download/$PKG"
URL_SHA="https://github.com/$REPO/releases/latest/download/$PKG.sha256"
log "下载 $PKG ..."
mkdir -p "$TMP_DIR" || die "无法创建临时目录 $TMP_DIR"
fetch "$URL" "$TMP_DIR/pkg.tar.gz" || die "下载失败: $URL"
[ -s "$TMP_DIR/pkg.tar.gz" ] || die "下载文件为空: $URL"
# 可选 sha256 校验（release 里若带 .sha256 就校验）
if fetch "$URL_SHA" "$TMP_DIR/pkg.sha256" 2>/dev/null && [ -s "$TMP_DIR/pkg.sha256" ]; then
    log "校验 sha256..."
    EXPECT="$(awk '{print $1}' "$TMP_DIR/pkg.sha256" | head -n1)"
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL="$(sha256sum "$TMP_DIR/pkg.tar.gz" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        ACTUAL="$(shasum -a 256 "$TMP_DIR/pkg.tar.gz" | awk '{print $1}')"
    else
        warn "未找到 sha256sum/shasum，跳过校验"
        EXPECT=""
    fi
    if [ -n "$EXPECT" ] && [ "$EXPECT" != "$ACTUAL" ]; then
        die "sha256 校验失败（期望 $EXPECT，实际 $ACTUAL）"
    fi
else
    warn "release 未提供 .sha256，跳过校验"
fi
# ---------- 解压 ----------
log "解压..."
tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || die "压缩包损坏"
tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || die "解压失败"
BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client | head -n1)"
[ -n "$BIN_SRC" ] || die "未找到 natpunch-client 二进制"
# ---------- 安装前清理既有客户端进程/服务 ----------
log "停止既有 natpunch-client（如有）..."
if command -v killall >/dev/null 2>&1; then
    killall natpunch-client 2>/dev/null || true
elif command -v pkill >/dev/null 2>&1; then
    pkill -x natpunch-client 2>/dev/null || true
fi
[ -f "$INIT" ] && "$INIT" stop 2>/dev/null || true
if command -v systemctl >/dev/null 2>&1; then
    systemctl stop natpunch-client 2>/dev/null || true
fi
sleep 1
# ---------- 安装二进制 ----------
install -m 0755 "$BIN_SRC" "$BIN" 2>/dev/null || {
    cp -f "$BIN_SRC" "$BIN" || die "安装到 $BIN 失败"
    chmod 755 "$BIN"
}
# ---------- 写配置 ----------
log "写入配置 $CONF"
umask 077
cat > "$CONF" <<EOF
SERVER=$SERVER
PORT=$PORT
VKEY=$VKEY
TLS_FLAG=$TLS_FLAG
EOF
chmod 600 "$CONF"
umask 022
# ---------- 注册自启 ----------
if [ "$IS_OPENWRT" -eq 1 ]; then
    log "注册 OpenWrt init.d 服务..."
    if grep -q "USE_PROCD" /etc/rc.common 2>/dev/null || [ -f /sbin/procd ]; then
        cat > "$INIT" <<'INIT'
#!/bin/sh /etc/rc.common
START=99
STOP=10
USE_PROCD=1
start_service() {
    [ -f /etc/natpunch.conf ] || return 1
    . /etc/natpunch.conf
    procd_open_instance
    procd_set_param command /usr/bin/natpunch-client \
        -server="${SERVER}:${PORT}" -vkey="${VKEY}" -type=tcp ${TLS_FLAG}
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
stop_service() {
    :
}
INIT
    else
        cat > "$INIT" <<'INIT'
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    [ -f /etc/natpunch.conf ] || exit 1
    . /etc/natpunch.conf
    /usr/bin/natpunch-client -server="${SERVER}:${PORT}" -vkey="${VKEY}" -type=tcp ${TLS_FLAG:-} \
        >>/tmp/natpunch-client.log 2>&1 &
}
stop() {
    killall natpunch-client 2>/dev/null
}
INIT
    fi
    chmod +x "$INIT"
    "$INIT" enable 2>/dev/null || warn "init.d enable 失败（不影响本次启动）"
    "$INIT" start
else
    if ! command -v systemctl >/dev/null 2>&1; then
        die "非 OpenWrt 环境且未找到 systemctl"
    fi
    log "注册 systemd 服务..."
    cat > "$UNIT" <<'SVC'
[Unit]
Description=NatPunch Client
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
EnvironmentFile=/etc/natpunch.conf
# 用 sh -c 包裹，避免 systemd 不支持 ${VAR:-} 默认值语法
ExecStart=/bin/sh -c '/usr/bin/natpunch-client -server="${SERVER}:${PORT}" -vkey="${VKEY}" -type=tcp ${TLS_FLAG}'
Restart=always
RestartSec=3
# KillMode=process：stop 只杀主进程，不连带杀面板 SSH 场景下的更新子脚本
KillMode=process
[Install]
WantedBy=multi-user.target
SVC
    chmod 644 "$UNIT"
    systemctl daemon-reload
    systemctl enable natpunch-client >/dev/null 2>&1
    systemctl restart natpunch-client
fi
# ---------- 验证 ----------
log "等待启动..."
OK=0
i=0
while [ "$i" -lt "$START_TIMEOUT" ]; do
    if [ "$IS_OPENWRT" -eq 1 ]; then
        if command -v pgrep >/dev/null 2>&1; then
            pgrep -f "/usr/bin/natpunch-client" >/dev/null 2>&1 && { OK=1; break; }
        else
            ps w 2>/dev/null | grep -v grep | grep -q "/usr/bin/natpunch-client" && { OK=1; break; }
        fi
    else
        systemctl is-active --quiet natpunch-client && { OK=1; break; }
    fi
    i=$((i + 1))
    sleep 1
done
if [ "$OK" -eq 1 ]; then
    VER_OUT=""
    if command -v timeout >/dev/null 2>&1; then
        VER_OUT="$(timeout 5 "$BIN" -version 2>/dev/null | head -n1)"
    else
        VER_OUT="$("$BIN" -version 2>/dev/null | head -n1)"
    fi
    echo "==> 安装成功 ✓ ${VER_OUT:-${VER:-最新版}}"
else
    echo "==> 启动失败，日志如下："
    if [ "$IS_OPENWRT" -eq 1 ]; then
        tail -n 20 "$LOG" 2>/dev/null
        logread 2>/dev/null | grep natpunch-client | tail -n 20
    else
        journalctl -u natpunch-client -n 20 --no-pager 2>/dev/null
        tail -n 20 "$LOG" 2>/dev/null
    fi
    exit 1
fi
exit 0
