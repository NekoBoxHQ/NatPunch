#!/bin/sh
# NatPunch 客户端一键安装（OpenWrt / Linux）
# 客户端命名为 natpunch-client，与服务端 natpunch 完全隔离（进程名/自启名/二进制名均不同），互不影响
# 用法: sh install.sh --openwrt VKEY SERVER [PORT] [TLS_FLAG]
set -u
REPO="NekoBoxHQ/NatPunch"
VKEY="${2:-}"
SERVER="${3:-}"
PORT="${4:-8024}"
TLS_FLAG="${5:-}"
CONF="/etc/natpunch.conf"
BIN="/usr/bin/natpunch-client"
INIT="/etc/init.d/natpunch-client"
UNIT="/etc/systemd/system/natpunch-client.service"
TMP_DIR="/tmp/natpunch_install.$$"
LOG="/tmp/natpunch-client.log"
log()  { echo "==> $*"; }
die()  { echo "错误: $*" >&2; cleanup; exit 1; }
cleanup() {
    [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ] && rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

# ---------- 参数校验 ----------
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
case "$SERVER" in
    *[!A-Za-z0-9.:_-]*) die "SERVER 含非法字符";;
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

# ---------- 获取版本（仅用于显示，下载走 releases/latest/download 自动指向最新） ----------
log "获取最新版本..."
VER=""
if command -v wget >/dev/null 2>&1; then
    VER="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
elif command -v curl >/dev/null 2>&1; then
    VER="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
fi
echo "    ${VER:-最新发布}"

# ---------- 下载（自动指向最新发布，不依赖写死的版本号） ----------
URL="https://github.com/$REPO/releases/latest/download/$PKG"
log "下载 $PKG ..."
mkdir -p "$TMP_DIR" || die "无法创建临时目录"
fetch() {
    if command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$2" "$1"
    else
        return 1
    fi
}
fetch "$URL" "$TMP_DIR/pkg.tar.gz" || die "下载失败: $URL"
[ -s "$TMP_DIR/pkg.tar.gz" ] || die "下载文件为空: $URL"

# ---------- 解压 ----------
log "解压..."
tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || die "压缩包损坏"
tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || die "解压失败"
BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client | head -n1)"
[ -n "$BIN_SRC" ] || die "未找到 natpunch-client 二进制"
cp -f "$BIN_SRC" "$BIN" || die "安装到 $BIN 失败"
chmod 755 "$BIN"

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

# ---------- 注册自启（natpunch-client，与服务端 natpunch 完全隔离） ----------
if [ "$IS_OPENWRT" -eq 1 ]; then
    log "注册 OpenWrt init.d 服务..."
    cat > "$INIT" <<'INIT'
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    [ -f /etc/natpunch.conf ] || exit 1
    . /etc/natpunch.conf
    /usr/bin/natpunch-client -server="${SERVER}:${PORT}" -vkey="${VKEY}" -type=tcp ${TLS_FLAG:-} >>/tmp/natpunch-client.log 2>&1 &
}
stop() {
    killall natpunch-client 2>/dev/null
}
INIT
    chmod +x "$INIT"
    "$INIT" enable
    "$INIT" start
else
    if ! command -v systemctl >/dev/null 2>&1; then
        die "非 OpenWrt 环境且未找到 systemctl"
    fi
    log "注册 systemd 服务..."
    cat > "$UNIT" <<'SVC'
[Unit]
Description=NatPunch Client
After=network.target
[Service]
Type=simple
EnvironmentFile=/etc/natpunch.conf
ExecStart=/usr/bin/natpunch-client -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp ${TLS_FLAG}
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
SVC
    systemctl daemon-reload
    systemctl enable natpunch-client >/dev/null 2>&1
    systemctl restart natpunch-client
fi

# ---------- 验证 ----------
log "等待启动..."
sleep 3
OK=0
if [ "$IS_OPENWRT" -eq 1 ]; then
    if command -v pgrep >/dev/null 2>&1; then
        pgrep -f "/usr/bin/natpunch-client" >/dev/null 2>&1 && OK=1
    else
        ps w 2>/dev/null | grep -v grep | grep -q "/usr/bin/natpunch-client" && OK=1
    fi
else
    systemctl is-active --quiet natpunch-client && OK=1
fi
if [ "$OK" -eq 1 ]; then
    VER_OUT="$("$BIN" -version 2>/dev/null | head -n1)"
    echo "==> 安装成功 ✓ ${VER_OUT:-最新版}"
else
    echo "==> 启动失败，日志如下："
    if [ "$IS_OPENWRT" -eq 1 ]; then
        tail -n 20 "$LOG" 2>/dev/null
    else
        journalctl -u natpunch-client -n 20 --no-pager 2>/dev/null
        tail -n 20 "$LOG" 2>/dev/null
    fi
    exit 1
fi
exit 0
