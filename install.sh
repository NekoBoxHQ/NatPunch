#!/bin/sh
# NatPunch 客户端一键安装（加固版，客户端命名为 natpunch-client，与服务端 natpunch 完全隔离）
# 用法: sh install.sh --openwrt VKEY SERVER [PORT] [TLS_FLAG]
set -u
REPO="NekoBoxHQ/NatPunch"
FALLBACK_VER="v26.9.5"
VKEY="${2:-}"
SERVER="${3:-}"
PORT="${4:-8025}"
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

# ---------- 获取版本 ----------
log "获取最新版本..."
VER=""
if command -v wget >/dev/null 2>&1; then
    VER="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
elif command -v curl >/dev/null 2>&1; then
    VER="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
fi
[ -n "$VER" ] || VER="$FALLBACK_VER"
echo "    $VER"

# ---------- 下载 ----------
URL="https://github.com/$REPO/releases/download/$VER/$PKG"
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
    echo "==> 安装成功 ✓ ${VER_OUT:-$VER}"
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

# ---------- 清理旧版客户端残留（旧命名 natpunch，迁移到 natpunch-client） ----------
# 旧客户端特征：二进制 /usr/bin/natpunch 或 /usr/local/bin/natpunch，进程命令行含 -vkey=；
# 自启 /etc/init.d/natpunch 或 natpunch.service 内容含 /usr/bin/natpunch 或 /etc/natpunch.conf（客户端版）。
# 服务端特征：/opt/natpunch 路径 —— 内容判定为服务端的自启一律不动。
init_owner() {
    f="$1"
    [ -f "$f" ] || return 2
    if grep -q '/opt/natpunch' "$f" 2>/dev/null; then return 1; fi
    if grep -q '/usr/bin/natpunch' "$f" 2>/dev/null; then return 0; fi
    return 2
}
unit_owner() {
    f="$1"
    [ -f "$f" ] || return 2
    if grep -q 'Description=NatPunch Server\|ExecStart=/opt/natpunch' "$f" 2>/dev/null; then return 1; fi
    if grep -q 'Description=NatPunch Client\|ExecStart=/usr/bin/natpunch' "$f" 2>/dev/null; then return 0; fi
    return 2
}
SERVER_PRESENT=0
[ -f /opt/natpunch/natpunch ] && SERVER_PRESENT=1
[ -f /opt/natpunch/conf/natpunch.conf ] && SERVER_PRESENT=1
LEGACY_BIN_1="/usr/bin/natpunch"
LEGACY_BIN_2="/usr/local/bin/natpunch"
LEGACY_INIT="/etc/init.d/natpunch"
LEGACY_UNIT_1="/etc/systemd/system/natpunch.service"
LEGACY_UNIT_2="/lib/systemd/system/natpunch.service"
HAS_LEGACY=0
[ -f "$LEGACY_BIN_1" ] || [ -f "$LEGACY_BIN_2" ] && HAS_LEGACY=1
[ -f "$LEGACY_INIT" ] && HAS_LEGACY=1
[ -f "$LEGACY_UNIT_1" ] || [ -f "$LEGACY_UNIT_2" ] && HAS_LEGACY=1
if [ "$HAS_LEGACY" = "1" ]; then
    echo "==> 检测到旧版客户端命名残留，正在迁移清理..."
    # 精确结束旧客户端进程（仅匹配 -vkey=，服务端不受影响）
    OLD_PIDS=""
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$exe" in
                */natpunch|*/natpunch\ \(deleted\)) ;;
                *) continue ;;
            esac
            cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
            case "$cmd" in
                *" -vkey="*) OLD_PIDS="$OLD_PIDS $p" ;;
            esac
        done
    else
        OLD_PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch' | grep ' -vkey=' | awk '{print $1}')
    fi
    if [ -f /opt/natpunch/natpunch.pid ]; then
        SPID=$(cat /opt/natpunch/natpunch.pid 2>/dev/null)
        case "$SPID" in
            ''|*[!0-9]*) ;;
            *)
                NEW=""
                for p in $OLD_PIDS; do
                    [ "$p" = "$SPID" ] && continue
                    NEW="$NEW $p"
                done
                OLD_PIDS="$NEW"
                ;;
        esac
    fi
    [ -n "$OLD_PIDS" ] && kill $OLD_PIDS 2>/dev/null
    sleep 1
    [ -n "$OLD_PIDS" ] && for p in $OLD_PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done
    # 清理旧自启（内容判定为客户端版才删，服务端版不动）
    IO=2
    [ -f "$LEGACY_INIT" ] && { init_owner "$LEGACY_INIT"; IO=$?; }
    if [ "$IO" = "0" ]; then
        "$LEGACY_INIT" disable 2>/dev/null || true
        rm -f "$LEGACY_INIT" /etc/rc.d/*natpunch 2>/dev/null
    elif [ "$IO" = "1" ]; then
        echo "==> 警告: $LEGACY_INIT 属于服务端，保留"
    fi
    if command -v systemctl >/dev/null 2>&1; then
        for U in "$LEGACY_UNIT_1" "$LEGACY_UNIT_2"; do
            [ -f "$U" ] || continue
            unit_owner "$U"; UO=$?
            if [ "$UO" = "0" ]; then
                systemctl disable natpunch 2>/dev/null || true
                rm -f "$U"
            elif [ "$UO" = "1" ]; then
                echo "==> 警告: $U 属于服务端，保留"
            fi
        done
        systemctl daemon-reload 2>/dev/null || true
    fi
    # 清理旧二进制（同机存在服务端时保留）
    if [ "$SERVER_PRESENT" = "0" ]; then
        rm -f "$LEGACY_BIN_1" "$LEGACY_BIN_2"
    fi
    echo "==> 旧版客户端残留清理完成"
fi
exit 0
