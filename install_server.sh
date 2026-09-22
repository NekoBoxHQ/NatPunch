#!/bin/sh
# ================= 基本配置 =================
DIR="/opt/natpunch"
BIN="$DIR/natpunch"
CONF_DIR="$DIR/conf"
CONF="$CONF_DIR/natpunch.conf"
WEB="$DIR/web"
PID_FILE="$DIR/natpunch.pid"
LOCK_DIR="$DIR/natpunch.lock.d"
LOG="$DIR/natpunch.log"
VER="v26.9.3"
REPO="lima-droid/NatPunch"
API_URL="https://api.github.com/repos/$REPO/releases/latest"
SELF="$(basename "$0")"
SERVICE_NAME="natpunch"
# ================= 工具 =================
log()  { echo "[NatPunch] $*"; }
die()  { echo "[NatPunch] 错误: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "缺少依赖: $1"; }
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
        log "清理陈旧锁 (PID ${OLD:-未知})"
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
    grep -E "^[[:space:]]*$key[[:space:]]*=" "$CONF" 2>/dev/null \
        | head -n1 | sed "s/^[[:space:]]*$key[[:space:]]*=[[:space:]]*//"
}
set_kv() {
    key="$1"; val="$2"
    case "$key" in *[!A-Za-z0-9_]*) die "非法配置键: $key" ;; esac
    case "$val" in
        *'
'*) die "配置值不能包含换行: $key" ;;
    esac
    [ -f "$CONF" ] || die "配置文件不存在: $CONF"
    awk -v k="$key" '
        BEGIN { pat = "^[[:space:]]*#?[[:space:]]*" k "[[:space:]]*=" }
        $0 !~ pat { print }
    ' "$CONF" > "$CONF.clean" || die "处理配置失败"
    mv "$CONF.clean" "$CONF" || die "写回配置失败"
    printf '%s=%s\n' "$key" "$val" >> "$CONF" || die "追加 $key 失败"
    grep -q "^$key=" "$CONF" || die "校验 $key 失败"
}
# ================= 交互输入端口/账号密码 =================
prompt_credentials() {
    echo ""
    echo "--------------------------------------------------"
    echo "  设置 NatPunch 面板登录信息"
    echo "--------------------------------------------------"
    printf "  Web 端口 [默认 8080]: "; read IN_PORT
    [ -n "$IN_PORT" ] || IN_PORT="8080"
    case "$IN_PORT" in *[!0-9]*) die "Web 端口必须是纯数字" ;; esac
    [ "$IN_PORT" -ge 1 ] && [ "$IN_PORT" -le 65535 ] || die "Web 端口范围必须是 1-65535"
    printf "  客户端TCP端口 [默认 8024]: "; read IN_BRIDGE
    [ -n "$IN_BRIDGE" ] || IN_BRIDGE="8024"
    case "$IN_BRIDGE" in *[!0-9]*) die "客户端连接端口必须是纯数字" ;; esac
    [ "$IN_BRIDGE" -ge 1 ] && [ "$IN_BRIDGE" -le 65535 ] || die "客户端连接端口范围必须是 1-65535"
    printf "  启用 HTTPS/TLS (y/N): "; read IN_HTTPS
    NEW_HTTPS="false"
    NEW_CERT=""
    NEW_KEY=""
    NEW_DOMAIN=""
    case "$IN_HTTPS" in
        y|Y|yes|YES)
            NEW_HTTPS="true"
            printf "  域名: "; read NEW_DOMAIN
            printf "  证书文件路径 [默认 /opt/natpunch/conf/server.pem]: "; read IN_CERT
            [ -n "$IN_CERT" ] && NEW_CERT="$IN_CERT" || NEW_CERT="/opt/natpunch/conf/server.pem"
            printf "  私钥文件路径 [默认 /opt/natpunch/conf/server.key]: "; read IN_KEY
            [ -n "$IN_KEY" ] && NEW_KEY="$IN_KEY" || NEW_KEY="/opt/natpunch/conf/server.key"
            ;;
    esac
    printf "  用户名   [默认 admin]: "; read IN_USER
    [ -n "$IN_USER" ] || IN_USER="admin"
    printf "  密码     [默认 123  ]: "; read IN_PASS
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
    log "已设置面板端口: $NEW_PORT"
    log "已设置客户端TCP端口: $NEW_BRIDGE (明文)"
    if [ "$NEW_HTTPS" = "true" ]; then
        set_kv tls_enable "true"
        set_kv tls_bridge_port "8025"
        set_kv web_open_ssl "true"
        set_kv web_cert_file "$NEW_CERT"
        set_kv web_key_file "$NEW_KEY"
        log "已启用 HTTPS/TLS: $NEW_DOMAIN"
        log "  证书: $NEW_CERT"
        log "  私钥: $NEW_KEY"
        log "  客户端TLS端口: 8025"
    else
        set_kv tls_enable "false"
        set_kv tls_bridge_port "0"
        set_kv web_open_ssl "false"
        log "未启用 HTTPS/TLS"
    fi
    log "已设置面板账号: $NEW_USER"
}
# ================= 下载 =================
choose_downloader() {
    if command -v wget >/dev/null 2>&1; then
        DL_TYPE="wget"
    elif command -v curl >/dev/null 2>&1; then
        DL_TYPE="curl"
    else
        die "缺少 wget/curl"
    fi
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
    RESP=$(fetch "$API_URL") || return 1
    [ -n "$RESP" ] || return 1
    VER_RAW=$(echo "$RESP" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
    [ -n "$VER_RAW" ] || return 1
    echo "$VER_RAW"
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
    safe_dir
    need tar; choose_downloader
    mkdir -p "$DIR" || die "无法创建 $DIR"
    TMP="$DIR/.install.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    LATEST=$(get_latest_ver)
    [ -n "$LATEST" ] || LATEST="$VER"
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) SERVER_PKG="linux_amd64_server.tar.gz";;
        aarch64|arm64) SERVER_PKG="linux_arm64_server.tar.gz";;
        *) SERVER_PKG="linux_amd64_server.tar.gz";;
    esac
    log "下载 $LATEST ($SERVER_PKG) ..."
    DL_URL="https://github.com/$REPO/releases/download/$LATEST/$SERVER_PKG"
    dl "$DL_URL" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    log "解压 ..."
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
    safe_dir
    need tar; choose_downloader
    [ -x "$BIN" ] || die "NatPunch 未安装，请先安装"
    TARGET_VER="${1:-$VER}"
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) SERVER_PKG="linux_amd64_server.tar.gz";;
        aarch64|arm64) SERVER_PKG="linux_arm64_server.tar.gz";;
        *) SERVER_PKG="linux_amd64_server.tar.gz";;
    esac
    TARGET_URL="https://github.com/$REPO/releases/download/$TARGET_VER/$SERVER_PKG"
    TMP="$DIR/.upgrade.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    log "下载 $TARGET_VER ..."
    dl "$TARGET_URL" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    log "解压 ..."
    tar -zxf natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "解压失败"; }
    rm -f natpunch.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    WEB_SRC=$(find_first_dir . web)
    [ -n "$BIN_SRC" ] || { cd /; rm -rf "$TMP"; die "压缩包中未找到 natpunch 二进制"; }
    BAK=""
    if [ -f "$BIN" ]; then
        BAK="$BIN.bak.$(date +%Y%m%d%H%M%S)"
        cp "$BIN" "$BAK" 2>/dev/null && log "已备份旧二进制: $BAK"
    fi
    cp "$BIN_SRC" "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "拷贝二进制失败"; }
    chmod +x "$DIR/natpunch.new" || { cd /; rm -rf "$TMP"; die "赋予执行权限失败"; }
    WAS_RUNNING=0
    is_running && WAS_RUNNING=1
    stop
    if ! mv "$DIR/natpunch.new" "$BIN"; then
        log "替换二进制失败，尝试回滚"
        [ -n "$BAK" ] && [ -f "$BAK" ] && cp "$BAK" "$BIN"
        [ "$WAS_RUNNING" = "1" ] && start
        cd /; rm -rf "$TMP"; die "替换二进制失败"
    fi
    if [ -n "$WEB_SRC" ]; then
        rm -rf "$WEB"
        cp -r "$WEB_SRC" "$WEB" || { cd /; rm -rf "$TMP"; die "拷贝 web 失败"; }
        log "已更新 web 目录"
    fi
    cd /; rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    start
    log "升级完成，当前版本: $TARGET_VER"
}
# ================= 启停 =================
start() {
    if is_running; then log "已运行 (PID $(cat "$PID_FILE"))"; return 0; fi
    kill_all; rm -f "$PID_FILE"
    cd "$DIR" || die "无法进入 $DIR"
    log "启动 ..."
    nohup "$BIN" >"$LOG" 2>&1 &
    echo $! > "$PID_FILE"
    sleep 2
    if is_running; then
        log "启动成功 (PID $(cat "$PID_FILE"))"
    else
        log "启动失败，日志末尾："
        tail -n 20 "$LOG" 2>/dev/null || true
        rm -f "$PID_FILE"; die "启动失败"
    fi
}
stop() {
    if is_running; then log "停止中 ..."; kill_one "$(cat "$PID_FILE" 2>/dev/null)"; fi
    kill_all; rm -f "$PID_FILE"; log "已停止"
}
restart() { stop; sleep 2; start; }
status() {
    if is_running; then
        log "运行中 (PID $(cat "$PID_FILE"))"
        if command -v ss >/dev/null 2>&1; then
            ss -tlnp 2>/dev/null | grep -F "natpunch" || true
        fi
    else
        log "未运行"
    fi
}
# ================= 开机自启（安装时自动注册） =================
register_autostart() {
    if is_openwrt; then
        log "注册 OpenWrt init.d 自启 ..."
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
    kill -0 $NEWPID 2>/dev/null || {
        echo "[NatPunch] 启动失败" >&2
        rm -f "$PID_FILE"
        exit 1
    }
}
stop() {
    if [ -f "$PID_FILE" ]; then
        OP=$(cat "$PID_FILE" 2>/dev/null)
        if [ -n "$OP" ]; then
            kill "$OP" 2>/dev/null
            sleep 1
            kill -0 "$OP" 2>/dev/null && kill -9 "$OP" 2>/dev/null
        fi
        rm -f "$PID_FILE"
    fi
}
EOL
        chmod +x /etc/init.d/natpunch || die "无法赋予 init 脚本执行权限"
        if /etc/init.d/natpunch enable; then
            if [ -e /etc/rc.d/S99natpunch ]; then
                log "已启用开机自启 (init.d)"
            else
                log "警告: 自启脚本未生效 (/etc/rc.d/S99natpunch 不存在)"
            fi
        else
            log "警告: 启用开机自启失败"
        fi
        return 0
    fi
    if has_systemd; then
        log "注册 systemd 自启 ..."
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
        if systemctl enable natpunch >/dev/null 2>&1; then
            log "已启用开机自启 (systemd)"
        else
            log "警告: systemctl enable 失败"
        fi
        return 0
    fi
    log "警告: 未识别的系统，跳过开机自启注册"
    return 0
}
unregister_autostart() {
    if is_openwrt; then
        /etc/init.d/natpunch stop  >/dev/null 2>&1 || true
        /etc/init.d/natpunch disable >/dev/null 2>&1 || true
        rm -f /etc/init.d/natpunch
        rm -f /etc/rc.d/*natpunch 2>/dev/null || true
    fi
    if has_systemd; then
        systemctl stop natpunch >/dev/null 2>&1 || true
        systemctl disable natpunch >/dev/null 2>&1 || true
        rm -f /etc/systemd/system/natpunch.service
        systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    return 0
}
# ================= 清理旧版下载服务 =================
cleanup_download() {
    if ! is_openwrt; then
        log "非 OpenWrt 系统，跳过下载服务清理"
        return 0
    fi
    if command -v uci >/dev/null 2>&1; then
        if uci show uhttpd.download >/dev/null 2>&1; then
            uci delete uhttpd.download 2>/dev/null && log "已删除 uhttpd download 实例"
            uci commit uhttpd 2>/dev/null
        fi
        if uci show firewall.allow-npc-download >/dev/null 2>&1; then
            uci delete firewall.allow-npc-download 2>/dev/null && log "已删除 firewall 规则 allow-npc-download"
            uci commit firewall 2>/dev/null
        fi
        /etc/init.d/uhttpd restart >/dev/null 2>&1
        /etc/init.d/firewall restart >/dev/null 2>&1
    fi
    if [ -d "/opt/npc_download" ]; then
        rm -rf /opt/npc_download 2>/dev/null && log "已删除旧版下载目录: /opt/npc_download"
    fi
}
uninstall() {
    safe_dir
    stop; kill_all
    unregister_autostart
    cleanup_download
    [ -n "$DIR" ] && [ "$DIR" != "/" ] && rm -rf "$DIR"
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    log "已卸载"
}
# ================= 信息获取 =================
get_ip() {
    IP=""
    # 优先取公网出口 IP
    if command -v wget >/dev/null 2>&1; then
        IP=$(wget -qO- --timeout=3 https://api.ipify.org 2>/dev/null | head -n1)
    elif command -v curl >/dev/null 2>&1; then
        IP=$(curl -fsSL --max-time 3 https://api.ipify.org 2>/dev/null | head -n1)
    fi
    # 公网 API 不可用时取本机出口 IP
    if [ -z "$IP" ]; then
        IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1)
    fi
    if [ -z "$IP" ]; then
        IP=$(ip addr 2>/dev/null | grep 'inet ' | grep -v '127.0.0.1' | awk '{print $2}' | cut -d/ -f1 | head -n1)
    fi
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
    echo "=================================================="
    echo "              NatPunch 服务端管理脚本"
    echo "=================================================="
    echo "   1. 安装 NatPunch"
    echo "   2. 启动 NatPunch"
    echo "   3. 停止 NatPunch"
    echo "   4. 重启 NatPunch"
    echo "   5. 查看状态"
    echo "   6. 修改面板账号密码"
    echo "   7. 升级 NatPunch"
    echo "   8. 卸载 NatPunch"
    echo "   0. 退出"
    echo "=================================================="
    printf "请输入选项 [0-8]: "
}
# ================= 动作 =================
do_install() {
    acquire_lock
    prompt_credentials
    install || { release_lock; return 1; }
    apply_credentials
    register_autostart
    start
    RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip)
        echo ""
        echo "=================================================="
        echo "  ✅ 安装完成"
        if [ "$NEW_HTTPS" = "true" ]; then
            echo "  面板地址: https://$NEW_DOMAIN:$NEW_PORT"
        else
            echo "  面板地址: http://$IP:$NEW_PORT"
        fi
        echo "  用户名:   $NEW_USER"
        echo "  密码:     $NEW_PASS"
        echo "=================================================="
    fi
}
do_start() {
    acquire_lock
    start; RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        SCHEME=$(grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null && echo https || echo http)
        echo ""; echo "  面板地址: $SCHEME://$IP:$PORT"
    fi
}
do_stop()   { acquire_lock; stop;    release_lock; }
do_restart() {
    acquire_lock
    restart; RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        SCHEME=$(grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null && echo https || echo http)
        echo ""; echo "  面板地址: $SCHEME://$IP:$PORT"
    fi
}
do_status()  { status; }
do_passwd() {
    if [ ! -f "$CONF" ]; then die "NatPunch 未安装，找不到配置文件 $CONF"; fi
    acquire_lock
    OLD_USER=$(get_kv web_username); [ -n "$OLD_USER" ] || OLD_USER="admin"
    OLD_PORT=$(get_web_port)
    echo ""
    echo "--------------------------------------------------"
    echo "  修改 NatPunch 面板登录信息"
    echo "  当前用户名: $OLD_USER"
    echo "  当前端口:   $OLD_PORT"
    echo "--------------------------------------------------"
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
    set_kv web_port "$IN_PORT"
    set_kv web_username "$IN_USER"
    set_kv web_password "$IN_PASS"
    set_kv allow_user_change_username true
    log "已写入配置，正在重启服务 ..."
    restart; RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip)
        echo ""
        echo "=================================================="
        echo "  ✅ 修改完成"
        echo "  端口:   $IN_PORT"
        echo "  用户名: $IN_USER"
        echo "  密码:   $IN_PASS"
        SCHEME=$(grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null && echo https || echo http)
        echo "  面板:   $SCHEME://$IP:$IN_PORT"
        echo "=================================================="
    fi
}
do_upgrade() {
    if [ ! -x "$BIN" ]; then die "NatPunch 未安装，请先安装"; fi
    echo ""
    echo "--------------------------------------------------"
    echo "  升级 NatPunch"
    echo "  当前版本: $(get_current_ver)"
    echo "  正在获取最新版本 ..."
    echo "--------------------------------------------------"
    LATEST=$(get_latest_ver)
    if [ -n "$LATEST" ]; then
        DEFAULT_VER="$LATEST"; echo "  GitHub 最新版本: $LATEST"
    else
        DEFAULT_VER="$VER"; echo "  获取最新版本失败，回退到脚本默认: $VER"
    fi
    printf "  目标版本 [回车使用 %s]: " "$DEFAULT_VER"; read IN_VER
    [ -n "$IN_VER" ] || IN_VER="$DEFAULT_VER"
    case "$IN_VER" in *"/"*|*" "*|*".."*) die "版本号不合法" ;; esac
    acquire_lock
    upgrade "$IN_VER"; RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        SCHEME=$(grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null && echo https || echo http)
        echo ""
        echo "=================================================="
        echo "  ✅ 升级完成"
        echo "  目标版本: $IN_VER"
        echo "  面板地址: $SCHEME://$IP:$PORT"
        echo "=================================================="
    fi
}
do_uninstall() {
    printf "确认卸载 NatPunch？此操作会删除 %s、自启服务及所有配置（含证书）[y/N]: " "$DIR"; read ans
    case "$ans" in y|Y|yes|YES) ;; *) echo "已取消"; return 0 ;; esac
    acquire_lock
    uninstall
    release_lock
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
            if [ -n "${1:-}" ]; then
                acquire_lock; upgrade "$1"; RC=$?; release_lock
                [ $RC -eq 0 ] && log "升级完成: $1"
            else
                LATEST=$(get_latest_ver); [ -n "$LATEST" ] || LATEST="$VER"
                acquire_lock; upgrade "$LATEST"; RC=$?; release_lock
                [ $RC -eq 0 ] && log "升级完成: $LATEST"
            fi
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
        1) do_install ;;
        2) do_start ;;
        3) do_stop ;;
        4) do_restart ;;
        5) do_status ;;
        6) do_passwd ;;
        7) do_upgrade ;;
        8) do_uninstall ;;
        0) echo "已退出"; exit 0 ;;
        *) echo "无效选项，请重新输入" ;;
    esac
    echo ""
    printf "按回车键返回菜单..."
    read dummy
done