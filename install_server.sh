#!/bin/sh
set -u
# ================= 基本配置 =================
DIR="/opt/natpunch"
BIN="$DIR/natpunch"
CONF_DIR="$DIR/conf"
CONF="$CONF_DIR/natpunch.conf"
WEB="$DIR/web"
PID_FILE="$DIR/natpunch.pid"
LOCK_DIR="$DIR/natpunch.lock.d"
LOG="$DIR/natpunch.log"
# 版本号不写死：下载始终走 releases/latest/download（自动指向最新发布），
# 仅需展示版本号时才探测（get_latest_ver），探测失败也不影响安装/升级。
REPO="NekoBoxHQ/NatPunch"
API_URL="https://api.github.com/repos/$REPO/releases/latest"
SELF="$(basename "$0")"
SERVICE_NAME="natpunch"
# ================= 输出样式 =================
C_RESET='\033[0m'
C_BOLD='\033[1m'
C_DIM='\033[2m'
C_GREEN='\033[32m'
C_YELLOW='\033[33m'
C_RED='\033[31m'
C_CYAN='\033[36m'
C_BLUE='\033[34m'
if [ ! -t 1 ]; then
    C_RESET=''; C_BOLD=''; C_DIM=''
    C_GREEN=''; C_YELLOW=''; C_RED=''; C_CYAN=''; C_BLUE=''
fi
LINE="----------------------------------------"
# 输出统一用 printf（%b 解释 \033 转义），兼容 busybox 不带 -e 的 echo，避免输出字面 "-e" 前缀
title() {
    printf '\n%b\n%b\n%b\n' "${C_CYAN}${LINE}${C_RESET}" "${C_BOLD}  $*${C_RESET}" "${C_CYAN}${LINE}${C_RESET}"
}
section() {
    printf '\n%b\n%b\n%b\n' "${C_BLUE}${LINE}${C_RESET}" "${C_BOLD}  $*${C_RESET}" "${C_BLUE}${LINE}${C_RESET}"
}
log()  { printf '%b\n' "  ${C_GREEN}[OK]${C_RESET}   $*"; }
info() { printf '%b\n' "  ${C_CYAN}[INFO]${C_RESET} $*"; }
warn() { printf '%b\n' "  ${C_YELLOW}[WARN]${C_RESET} $*" >&2; }
die()  { printf '%b\n' "  ${C_RED}[FAIL]${C_RESET} $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "缺少依赖: $1"; }
kv() {
    printf "  ${C_DIM}%-10s${C_RESET} %s\n" "$1" "$2"
}
# ================= 工具 =================
norm() {
    if command -v realpath >/dev/null 2>&1; then
        realpath "$1" 2>/dev/null && return 0
    fi
    if readlink -f "$1" >/dev/null 2>&1; then
        readlink -f "$1" 2>/dev/null && return 0
    fi
    echo "$1"
}
safe_dir() {
    case "$DIR" in
        /?*) ;;
        *) die "DIR 配置非法: $DIR" ;;
    esac
    [ "$DIR" != "/" ] || die "DIR 不能为根目录"
}
find_first_file() {
    for f in $(find "$1" -type f -name "$2" 2>/dev/null); do
        echo "$f"; return 0
    done
    return 1
}
find_first_dir() {
    for d in $(find "$1" -type d -name "$2" 2>/dev/null); do
        echo "$d"; return 0
    done
    return 1
}
is_openwrt() { [ -f /etc/openwrt_release ]; }
has_systemd() { command -v systemctl >/dev/null 2>&1; }
# ================= 锁 =================
LOCK_MODE=""
acquire_lock() {
    safe_dir
    mkdir -p "$DIR" 2>/dev/null
    if mkdir "$LOCK_DIR" 2>/dev/null; then
        echo $$ > "$LOCK_DIR/pid" 2>/dev/null || true
        LOCK_MODE="held"; return 0
    fi
    if [ -f "$LOCK_DIR/pid" ]; then
        OLD=$(cat "$LOCK_DIR/pid" 2>/dev/null)
        if [ -n "$OLD" ] && kill -0 "$OLD" 2>/dev/null; then
            die "已有 NatPunch 操作进行中 (PID $OLD)，请稍后重试"
        fi
        info "清理陈旧锁 (PID ${OLD:-未知})"
        rm -rf "$LOCK_DIR" 2>/dev/null
        if mkdir "$LOCK_DIR" 2>/dev/null; then
            echo $$ > "$LOCK_DIR/pid" 2>/dev/null || true
            LOCK_MODE="held"; return 0
        fi
    fi
    die "无法获取锁"
}
release_lock() {
    [ "$LOCK_MODE" = "held" ] || return 0
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    LOCK_MODE=""
}
# ================= 进程判断 =================
is_running() {
    [ -f "$PID_FILE" ] || return 1
    PIDV=$(cat "$PID_FILE" 2>/dev/null) || return 1
    [ -n "$PIDV" ] || return 1
    case "$PIDV" in *[!0-9]*) return 1 ;; esac
    [ -d "/proc/$PIDV" ] || return 1
    EXE=$(readlink "/proc/$PIDV/exe" 2>/dev/null)
    if [ -n "$EXE" ]; then
        case "$EXE" in *" (deleted)") return 1 ;; esac
        [ "$(norm "$EXE")" = "$(norm "$BIN")" ] && return 0
        return 1
    fi
    CMD=$(tr '\0' ' ' < "/proc/$PIDV/cmdline" 2>/dev/null)
    case "$CMD" in *"$BIN"*) return 0 ;; esac
    return 1
}
find_all_nps() {
    if [ -f "$PID_FILE" ]; then
        p=$(cat "$PID_FILE" 2>/dev/null)
        case "$p" in ''|*[!0-9]*) ;; *) [ -d "/proc/$p" ] && echo "$p" ;; esac
    fi
    TARGET="$(norm "$BIN")"
    if command -v pgrep >/dev/null 2>&1; then
        for p in $(pgrep -x natpunch 2>/dev/null); do
            [ "$p" = "$$" ] && continue
            exe=$(readlink "/proc/$p/exe" 2>/dev/null)
            [ -n "$exe" ] && [ "$(norm "$exe")" = "$TARGET" ] && echo "$p"
        done
    else
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null)
            [ -n "$exe" ] && [ "$(norm "$exe")" = "$TARGET" ] && echo "$p"
        done
    fi
}
kill_one() {
    p="$1"; [ -n "$p" ] || return 0
    kill "$p" 2>/dev/null || true
    sleep 1
    kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true
}
kill_all() {
    LIST=$(find_all_nps | sort -u)
    [ -n "$LIST" ] || return 0
    for p in $LIST; do
        [ "$p" = "$$" ] && continue
        kill "$p" 2>/dev/null || true
    done
    sleep 1
    for p in $LIST; do
        [ "$p" = "$$" ] && continue
        kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true
    done
    sleep 1
}
# ================= 配置读写 =================
get_kv() {
    key="$1"
    [ -f "$CONF" ] || return 1
    grep -E "^[[:space:]]*$key[[:space:]]*=" "$CONF" 2>/dev/null | head -n1 | sed "s/^[[:space:]]*$key[[:space:]]*=[[:space:]]*//"
}
set_kv() {
    key="$1"; val="$2"
    case "$key" in *[!A-Za-z0-9_]*) die "非法配置键: $key" ;; esac
    case "$val" in
        *'
'*) die "配置值不能包含换行: $key" ;;
    esac
    [ -f "$CONF" ] || die "配置文件不存在: $CONF"
    awk -v k="$key" 'BEGIN { pat = "^[[:space:]]*#?[[:space:]]*" k "[[:space:]]*=" } $0 !~ pat { print }' "$CONF" > "$CONF.clean" || die "处理配置失败"
    mv "$CONF.clean" "$CONF" || die "写回配置失败"
    printf '%s=%s\n' "$key" "$val" >> "$CONF" || die "追加 $key 失败"
    grep -q "^$key=" "$CONF" || die "校验 $key 失败"
}
# ================= 交互输入 =================
prompt_credentials() {
    section "设置 NatPunch 面板登录信息"
    printf "  Web 端口 [默认 8080]: "; read IN_PORT
    [ -n "$IN_PORT" ] || IN_PORT="8080"
    case "$IN_PORT" in *[!0-9]*) die "Web 端口必须是纯数字" ;; esac
    [ "$IN_PORT" -ge 1 ] && [ "$IN_PORT" -le 65535 ] || die "Web 端口范围必须是 1-65535"
    printf "  客户端 TCP 端口 [默认 8024]: "; read IN_BRIDGE
    [ -n "$IN_BRIDGE" ] || IN_BRIDGE="8024"
    case "$IN_BRIDGE" in *[!0-9]*) die "客户端连接端口必须是纯数字" ;; esac
    [ "$IN_BRIDGE" -ge 1 ] && [ "$IN_BRIDGE" -le 65535 ] || die "客户端连接端口范围必须是 1-65535"
    printf "  启用 HTTPS/TLS (y/N): "; read IN_HTTPS
    NEW_HTTPS="false"; NEW_CERT=""; NEW_KEY=""; NEW_DOMAIN=""
    case "$IN_HTTPS" in
        y|Y|yes|YES)
            NEW_HTTPS="true"
            printf "  域名: "; read NEW_DOMAIN
            [ -n "$NEW_DOMAIN" ] || NEW_DOMAIN="$(get_ip)"
            printf "  pem [默认 /opt/natpunch/conf/server.pem]: "; read IN_CERT
            [ -n "$IN_CERT" ] && NEW_CERT="$IN_CERT" || NEW_CERT="/opt/natpunch/conf/server.pem"
            printf "  key [默认 /opt/natpunch/conf/server.key]: "; read IN_KEY
            [ -n "$IN_KEY" ] && NEW_KEY="$IN_KEY" || NEW_KEY="/opt/natpunch/conf/server.key"
            ;;
    esac
    printf "  用户名 [默认 admin]: "; read IN_USER
    [ -n "$IN_USER" ] || IN_USER="admin"
    printf "  密码   [默认 123  ]: "; read IN_PASS
    [ -n "$IN_PASS" ] || IN_PASS="123"
    case "$IN_USER" in *"="*) die "用户名不能包含 =" ;; esac
    case "$IN_PASS" in *"="*) die "密码不能包含 =" ;; esac
    NEW_PORT="$IN_PORT"; NEW_BRIDGE="$IN_BRIDGE"; NEW_USER="$IN_USER"; NEW_PASS="$IN_PASS"
}
apply_credentials() {
    [ -f "$CONF" ] || die "配置文件不存在: $CONF"
    set_kv web_port "$NEW_PORT"
    set_kv bridge_port "$NEW_BRIDGE"
    set_kv web_username "$NEW_USER"
    set_kv web_password "$NEW_PASS"
    set_kv allow_user_change_username true
    info "面板端口: $NEW_PORT"
    info "客户端 TCP 端口: $NEW_BRIDGE (明文)"
    if [ "$NEW_HTTPS" = "true" ]; then
        set_kv tls_enable "true"
        set_kv tls_bridge_port "8025"
        set_kv web_open_ssl "true"
        set_kv web_cert_file "$NEW_CERT"
        set_kv web_key_file "$NEW_KEY"
        set_kv web_domain "$NEW_DOMAIN"
        info "已启用 HTTPS/TLS: $NEW_DOMAIN"
        info "pem: $NEW_CERT"
        info "key: $NEW_KEY"
        info "客户端 TLS 端口: 8025"
    else
        set_kv tls_enable "false"
        set_kv tls_bridge_port "0"
        set_kv web_open_ssl "false"
        info "未启用 HTTPS/TLS"
    fi
    info "面板账号: $NEW_USER"
}
# ================= 下载 =================
choose_downloader() {
    if command -v wget >/dev/null 2>&1; then DL_TYPE="wget"
    elif command -v curl >/dev/null 2>&1; then DL_TYPE="curl"
    else die "缺少 wget/curl"; fi
}
dl() {
    case "$DL_TYPE" in
        wget) wget -q -O "$2" "$1" ;;
        curl) curl -fsSL -o "$2" "$1" ;;
    esac
}
fetch() {
    case "$DL_TYPE" in
        wget) wget -q -O - "$1" 2>/dev/null ;;
        curl) curl -fsSL "$1" 2>/dev/null ;;
    esac
}
get_latest_ver() {
    # 多源探测：优先 GitHub API，失败后回退 GitHub 网页重定向（github.com 通常更稳定）。
    # 仅用于展示版本号；两者都失败返回空，下载仍走 releases/latest/download 自动取最新。
    V=""
    RESP=$(fetch "$API_URL") || RESP=""
    if [ -n "$RESP" ]; then
        V=$(echo "$RESP" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
    fi
    if [ -z "$V" ]; then
        LOC=""
        if command -v curl >/dev/null 2>&1; then
            LOC=$(curl -sI --max-time 10 "https://github.com/$REPO/releases/latest" 2>/dev/null | tr -d '\r' | grep -i '^location:' | head -n1)
        else
            LOC=$(wget -qO- --timeout=10 --server-response "https://github.com/$REPO/releases/latest" 2>&1 | tr -d '\r' | grep -i 'location:' | head -n1)
        fi
        V=$(echo "$LOC" | sed 's/.*tag\///' | tr -d '[:space:]')
    fi
    [ -n "$V" ] && echo "$V" && return 0
    return 1
}
get_current_ver() {
    if [ -x "$BIN" ]; then
        V=$("$BIN" -version 2>/dev/null | head -n1)
        [ -n "$V" ] && echo "$V" && return 0
    fi
    echo "未知"
}
# ================= 安装 =================
install() {
    safe_dir; need tar; choose_downloader
    mkdir -p "$DIR" || die "无法创建 $DIR"
    TMP="$DIR/.install.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) SERVER_PKG="linux_amd64_server.tar.gz";;
        aarch64|arm64) SERVER_PKG="linux_arm64_server.tar.gz";;
        *) SERVER_PKG="linux_amd64_server.tar.gz";;
    esac
    info "下载最新发布 ($SERVER_PKG) ..."
    dl "https://github.com/$REPO/releases/latest/download/$SERVER_PKG" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    info "解压安装包 ..."
    tar -zxf natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "解压失败"; }
    rm -f natpunch.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    CONF_SRC=$(find_first_dir . conf)
    WEB_SRC=$(find_first_dir . web)
    [ -n "$BIN_SRC" ]  || { cd /; rm -rf "$TMP"; die "压缩包中未找到 natpunch 二进制"; }
    [ -n "$CONF_SRC" ] || { cd /; rm -rf "$TMP"; die "压缩包中未找到 conf 目录"; }
    [ -n "$WEB_SRC" ]  || { cd /; rm -rf "$TMP"; die "压缩包中未找到 web 目录"; }
    cp "$BIN_SRC" "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "拷贝二进制失败"; }
    chmod +x "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "赋予执行权限失败"; }
    mv "$DIR/natpunch.new" "$BIN" || { cd /; rm -rf "$TMP"; die "替换二进制失败"; }
    [ -d "$CONF_DIR" ] || cp -r "$CONF_SRC" "$CONF_DIR" || { cd /; rm -rf "$TMP"; die "拷贝 conf 失败"; }
    [ -d "$WEB" ] || cp -r "$WEB_SRC" "$WEB" || { cd /; rm -rf "$TMP"; die "拷贝 web 失败"; }
    cd /; rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    log "安装完成"
}
upgrade() {
    safe_dir; need tar; choose_downloader
    [ -x "$BIN" ] || die "NatPunch 未安装，请先安装"
    TARGET_VER="${1:-}"
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) SERVER_PKG="linux_amd64_server.tar.gz";;
        aarch64|arm64) SERVER_PKG="linux_arm64_server.tar.gz";;
        *) SERVER_PKG="linux_amd64_server.tar.gz";;
    esac
    if [ -n "$TARGET_VER" ]; then
        TARGET_URL="https://github.com/$REPO/releases/download/$TARGET_VER/$SERVER_PKG"
        info "下载 $TARGET_VER ..."
    else
        TARGET_URL="https://github.com/$REPO/releases/latest/download/$SERVER_PKG"
        info "下载最新发布 ..."
    fi
    TMP="$DIR/.upgrade.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    dl "$TARGET_URL" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    info "解压安装包 ..."
    tar -zxf natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "解压失败"; }
    rm -f natpunch.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    WEB_SRC=$(find_first_dir . web)
    [ -n "$BIN_SRC" ] || { cd /; rm -rf "$TMP"; die "压缩包中未找到 natpunch 二进制"; }
    BAK=""
    if [ -f "$BIN" ]; then
        BAK="$BIN.bak.$(date +%Y%m%d%H%M%S)"
        cp "$BIN" "$BAK" 2>/dev/null && info "已备份旧二进制: $BAK"
    fi
    cp "$BIN_SRC" "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "拷贝二进制失败"; }
    chmod +x "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "赋予执行权限失败"; }
    WAS_RUNNING=0; is_running && WAS_RUNNING=1
    stop
    if ! mv "$DIR/natpunch.new" "$BIN"; then
        warn "替换二进制失败，尝试回滚"
        [ -n "$BAK" ] && [ -f "$BAK" ] && cp "$BAK" "$BIN"
        [ "$WAS_RUNNING" = "1" ] && start
        cd /; rm -rf "$TMP"; die "替换二进制失败"
    fi
    if [ -n "$WEB_SRC" ]; then
        rm -rf "$WEB"
        cp -r "$WEB_SRC" "$WEB" || { cd /; rm -rf "$TMP"; die "拷贝 web 失败"; }
        info "已更新 web 目录"
    fi
    cd /; rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    start
    log "升级完成，当前版本: ${TARGET_VER:-最新版}"
}
# ================= 启停 =================
start() {
    if is_running; then
        info "服务已在运行 (PID $(cat "$PID_FILE"))"
        return 0
    fi
    kill_all; rm -f "$PID_FILE"
    cd "$DIR" || die "无法进入 $DIR"
    info "启动服务 ..."
    nohup "$BIN" >"$LOG" 2>&1 &
    echo $! > "$PID_FILE"
    sleep 2
    if is_running; then
        log "启动成功 (PID $(cat "$PID_FILE"))"
    else
        warn "启动失败，日志末尾："
        tail -n 20 "$LOG" 2>/dev/null || true
        rm -f "$PID_FILE"; die "启动失败"
    fi
}
stop() {
    if is_running; then
        info "停止服务 ..."
        kill_one "$(cat "$PID_FILE" 2>/dev/null)"
    fi
    kill_all; rm -f "$PID_FILE"
    log "已停止"
}
restart() { stop; sleep 2; start; }
status() {
    if is_running; then
        log "运行中 (PID $(cat "$PID_FILE"))"
        if command -v ss >/dev/null 2>&1; then ss -tlnp 2>/dev/null | grep -F "natpunch" || true; fi
    else
        info "未运行"
    fi
}
# ================= 开机自启 =================
register_autostart() {
    if is_openwrt; then
        info "注册 OpenWrt init.d 自启 ..."
        cat > /etc/init.d/natpunch <<'EOL'
#!/bin/sh /etc/rc.common
START=99
STOP=10
PID_FILE="/opt/natpunch/natpunch.pid"
DIR="/opt/natpunch"
BIN="/opt/natpunch/natpunch"
LOG="/opt/natpunch/natpunch.log"
start() {
    if [ -f "$PID_FILE" ]; then
        OP=$(cat "$PID_FILE" 2>/dev/null)
        [ -n "$OP" ] && kill -0 "$OP" 2>/dev/null && exit 0
    fi
    cd "$DIR" || exit 1
    "$BIN" >"$LOG" 2>&1 &
    NEWPID=$!
    echo $NEWPID > "$PID_FILE"
    sleep 1
    kill -0 $NEWPID 2>/dev/null || { echo "[NatPunch] 启动失败" >&2; rm -f "$PID_FILE"; exit 1; }
}
stop() {
    if [ -f "$PID_FILE" ]; then
        OP=$(cat "$PID_FILE" 2>/dev/null)
        if [ -n "$OP" ]; then
            kill "$OP" 2>/dev/null; sleep 1
            kill -0 "$OP" 2>/dev/null && kill -9 "$OP" 2>/dev/null
        fi
        rm -f "$PID_FILE"
    fi
}
EOL
        chmod +x /etc/init.d/natpunch || die "无法赋予 init 脚本执行权限"
        /etc/init.d/natpunch enable 2>/dev/null && log "已启用开机自启 (init.d)" || warn "启用开机自启失败"
        return 0
    fi
    if has_systemd; then
        info "注册 systemd 自启 ..."
        cat > /etc/systemd/system/natpunch.service <<EOF
[Unit]
Description=NatPunch Server
After=network.target
[Service]
Type=simple
WorkingDirectory=$DIR
ExecStart=$BIN
Restart=always
RestartSec=3
PIDFile=$PID_FILE
[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload 2>/dev/null
        systemctl enable natpunch >/dev/null 2>&1 && log "已启用开机自启 (systemd)" || warn "systemctl enable 失败"
        return 0
    fi
    warn "未识别的系统，跳过开机自启注册"
}
unregister_autostart() {
    if is_openwrt; then
        /etc/init.d/natpunch stop  >/dev/null 2>&1 || true
        /etc/init.d/natpunch disable >/dev/null 2>&1 || true
        rm -f /etc/init.d/natpunch /etc/rc.d/*natpunch 2>/dev/null
    fi
    if has_systemd; then
        systemctl stop natpunch >/dev/null 2>&1 || true
        systemctl disable natpunch >/dev/null 2>&1 || true
        rm -f /etc/systemd/system/natpunch.service
        systemctl daemon-reload >/dev/null 2>&1 || true
    fi
}
cleanup_download() {
    if ! is_openwrt; then info "非 OpenWrt，跳过下载服务清理"; return 0; fi
    if command -v uci >/dev/null 2>&1; then
        uci show uhttpd.download >/dev/null 2>&1 && { uci delete uhttpd.download 2>/dev/null; uci commit uhttpd 2>/dev/null; info "已删除 uhttpd download"; }
        uci show firewall.allow-npc-download >/dev/null 2>&1 && { uci delete firewall.allow-npc-download 2>/dev/null; uci commit firewall 2>/dev/null; info "已删除 firewall 规则"; }
        /etc/init.d/uhttpd restart >/dev/null 2>&1
        /etc/init.d/firewall restart >/dev/null 2>&1
    fi
    [ -d "/opt/npc_download" ] && rm -rf /opt/npc_download 2>/dev/null && info "已删除旧版下载目录"
}
uninstall() {
    safe_dir; stop; kill_all; unregister_autostart; cleanup_download
    [ -n "$DIR" ] && [ "$DIR" != "/" ] && rm -rf "$DIR"
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    log "已卸载"
}
# ================= 信息获取 =================
get_ip() {
    IP=""
    if command -v wget >/dev/null 2>&1; then
        IP=$(wget -qO- --timeout=3 https://api.ipify.org 2>/dev/null | head -n1)
    elif command -v curl >/dev/null 2>&1; then
        IP=$(curl -fsSL --max-time 3 https://api.ipify.org 2>/dev/null | head -n1)
    fi
    [ -z "$IP" ] && IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1)
    [ -z "$IP" ] && IP=$(ip addr 2>/dev/null | grep 'inet ' | grep -v '127.0.0.1' | awk '{print $2}' | cut -d/ -f1 | head -n1)
    [ -n "$IP" ] || IP="<本机IP>"
    echo "$IP"
}
get_web_port() {
    P=$(get_kv web_port)
    [ -n "$P" ] && echo "$P" && return 0
    echo "8080"
}
# ================= 菜单 =================
show_menu() {
    echo ""
    printf '%b\n' "${C_CYAN}${LINE}${C_RESET}"
    printf '%b\n' "${C_BOLD}         NatPunch 服务端管理脚本${C_RESET}"
    printf '%b\n' "${C_CYAN}${LINE}${C_RESET}"
    printf '%b\n' "   ${C_GREEN}1${C_RESET}  安装 NatPunch"
    printf '%b\n' "   ${C_GREEN}2${C_RESET}  启动 NatPunch"
    printf '%b\n' "   ${C_GREEN}3${C_RESET}  停止 NatPunch"
    printf '%b\n' "   ${C_GREEN}4${C_RESET}  重启 NatPunch"
    printf '%b\n' "   ${C_GREEN}5${C_RESET}  查看状态"
    printf '%b\n' "   ${C_GREEN}6${C_RESET}  修改面板账号密码"
    printf '%b\n' "   ${C_GREEN}7${C_RESET}  升级 NatPunch"
    printf '%b\n' "   ${C_GREEN}8${C_RESET}  卸载 NatPunch"
    printf '%b\n' "   ${C_RED}0${C_RESET}  退出"
    printf '%b\n' "${C_CYAN}${LINE}${C_RESET}"
    printf "  请输入选项 [0-8]: "
}
# ================= 动作 =================
do_install() {
    title "安装 NatPunch"
    acquire_lock; prompt_credentials
    install || { release_lock; return 1; }
    apply_credentials; register_autostart; start
    RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        echo ""
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        printf '%b\n' "${C_GREEN}${C_BOLD}  [OK] 安装完成${C_RESET}"
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        echo ""
        if [ "$NEW_HTTPS" = "true" ]; then
            kv "面板地址" "https://$NEW_DOMAIN:$NEW_PORT"
        else
            kv "面板地址" "http://$(get_ip):$NEW_PORT"
        fi
        kv "用户名" "$NEW_USER"
        kv "密码"   "$NEW_PASS"
        echo ""
    fi
}
do_start() {
    title "启动 NatPunch"
    acquire_lock; start; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        PORT=$(get_web_port)
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "$HOST" ] || HOST="$(get_ip)"
        else SCHEME="http"; HOST="$(get_ip)"; fi
        echo ""; kv "面板地址" "$SCHEME://$HOST:$PORT"; echo ""
    fi
}
do_stop() {
    title "停止 NatPunch"; acquire_lock; stop; release_lock
}
do_restart() {
    title "重启 NatPunch"; acquire_lock; restart; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        PORT=$(get_web_port)
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "$HOST" ] || HOST="$(get_ip)"
        else SCHEME="http"; HOST="$(get_ip)"; fi
        echo ""; kv "面板地址" "$SCHEME://$HOST:$PORT"; echo ""
    fi
}
do_status() { title "运行状态"; status; echo ""; }
do_passwd() {
    if [ ! -f "$CONF" ]; then die "NatPunch 未安装，找不到配置文件 $CONF"; fi
    title "修改面板登录信息"
    acquire_lock
    OLD_USER=$(get_kv web_username); [ -n "$OLD_USER" ] || OLD_USER="admin"
    OLD_PORT=$(get_web_port)
    section "当前信息"
    kv "用户名" "$OLD_USER"; kv "端口" "$OLD_PORT"
    section "输入新信息"
    printf "  新端口 [回车保持 %s]: " "$OLD_PORT"; read IN_PORT
    [ -n "$IN_PORT" ] || IN_PORT="$OLD_PORT"
    case "$IN_PORT" in *[!0-9]*) die "端口必须是纯数字" ;; esac
    [ "$IN_PORT" -ge 1 ] && [ "$IN_PORT" -le 65535 ] || die "端口范围必须是 1-65535"
    printf "  新用户名 [回车保持 %s]: " "$OLD_USER"; read IN_USER
    [ -n "$IN_USER" ] || IN_USER="$OLD_USER"
    printf "  新密码 (不能为空): "; read IN_PASS
    [ -n "$IN_PASS" ] || die "密码不能为空"
    case "$IN_USER" in *"="*) die "用户名不能包含 =" ;; esac
    case "$IN_PASS" in *"="*) die "密码不能包含 =" ;; esac
    cp "$CONF" "$CONF.bak.$(date +%Y%m%d%H%M%S)" 2>/dev/null
    set_kv web_port "$IN_PORT"; set_kv web_username "$IN_USER"; set_kv web_password "$IN_PASS"
    set_kv allow_user_change_username true
    info "已写入配置，正在重启服务 ..."
    restart; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "$HOST" ] || HOST="$(get_ip)"
        else SCHEME="http"; HOST="$(get_ip)"; fi
        echo ""
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        printf '%b\n' "${C_GREEN}${C_BOLD}  [OK] 修改完成${C_RESET}"
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        echo ""
        kv "端口" "$IN_PORT"; kv "用户名" "$IN_USER"; kv "密码" "$IN_PASS"; kv "面板" "$SCHEME://$HOST:$IN_PORT"
        echo ""
    fi
}
do_upgrade() {
    if [ ! -x "$BIN" ]; then die "NatPunch 未安装，请先安装"; fi
    title "升级 NatPunch"
    section "版本信息"
    kv "当前版本" "$(get_current_ver)"
    info "正在获取最新版本 ..."
    LATEST=$(get_latest_ver)
    if [ -n "$LATEST" ]; then
        info "GitHub 最新版本: $LATEST"
    else
        warn "无法获取最新版本号（将直接下载最新发布）"
    fi
    printf "  目标版本 [回车使用最新版]: "; read IN_VER
    case "$IN_VER" in *"/"*|*" "*|*".."*) die "版本号不合法" ;; esac
    acquire_lock; upgrade "$IN_VER"; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        PORT=$(get_web_port)
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "$HOST" ] || HOST="$(get_ip)"
        else SCHEME="http"; HOST="$(get_ip)"; fi
        echo ""
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        printf '%b\n' "${C_GREEN}${C_BOLD}  [OK] 升级完成${C_RESET}"
        printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
        echo ""
        kv "目标版本" "${IN_VER:-最新版}"; kv "面板地址" "$SCHEME://$HOST:$PORT"
        echo ""
    fi
}
do_uninstall() {
    echo ""
    printf '%b\n' "  ${C_YELLOW}[WARN]${C_RESET} 即将卸载 NatPunch"
    printf '%b\n' "  ${C_DIM}       将删除: $DIR、自启服务、全部配置（含证书）${C_RESET}"
    printf "  确认卸载？[y/N]: "; read ans
    case "$ans" in y|Y|yes|YES) ;; *) echo "  已取消"; return 0 ;; esac
    acquire_lock; uninstall; release_lock
}
# ================= 入口 =================
trap 'release_lock' EXIT INT TERM
if [ -n "${1:-}" ]; then
    case "$1" in
        install)   do_install ;;
        start)     do_start ;;
        stop)      do_stop ;;
        restart)   do_restart ;;
        status)    do_status ;;
        passwd)    do_passwd ;;
        upgrade)
            shift
            acquire_lock; upgrade "${1:-}"; RC=$?; release_lock
            [ $RC -eq 0 ] && log "升级完成${1:+: $1}"
            ;;
        uninstall) do_uninstall ;;
        *) echo "用法: $SELF {install|start|stop|restart|status|passwd|upgrade [版本]|uninstall}" ;;
    esac
    exit $?
fi
while :; do
    show_menu
    read CHOICE
    case "$CHOICE" in
        1) do_install ;; 2) do_start ;; 3) do_stop ;; 4) do_restart ;; 5) do_status ;;
        6) do_passwd ;; 7) do_upgrade ;; 8) do_uninstall ;; 0) echo "已退出"; exit 0 ;;
        *) echo "无效选项，请重新输入" ;;
    esac
    echo ""
    printf "按回车键返回菜单..."
    read dummy
done