#!/bin/sh
# ================= 基本配置 =================
DIR="/opt/natpunch"
BIN="$DIR/natpunch"
CONF_DIR="$DIR/conf"
CONF="$CONF_DIR/nps.conf"
WEB="$DIR/web"
PID_FILE="$DIR/natpunch.pid"
LOCK_DIR="$DIR/natpunch.lock.d"
LOG="$DIR/natpunch.log"
VER="v26.9.1"
REPO="lima-droid/NatPunch"
URL="https://github.com/$REPO/releases/download/$VER/linux_amd64_server.tar.gz"
API_URL="https://api.github.com/repos/$REPO/releases/latest"
# 客户端下载：全部依赖 GitHub 项目 Release，服务端不存放任何下载文件
DL_DOMAIN=""
# 客户端连接端口 = TLS bridge 端口（8025，加密连接）；明文 bridge 28082 已弃用
WEB_PORT="8025"
DL_REPO="lima-droid/NatPunch"
# ================= 工具 =================
log()  { echo "[NatPunch] $*"; }
die()  { echo "[NatPunch] 错误: $*"; exit 1; }
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
# ================= 锁 =================
LOCK_MODE=""
acquire_lock() {
    mkdir -p "$DIR" 2>/dev/null
    rm -rf "$LOCK_DIR" 2>/dev/null
    if mkdir "$LOCK_DIR" 2>/dev/null; then
        echo $$ > "$LOCK_DIR/pid" 2>/dev/null || true
        LOCK_MODE="held"; return 0
    fi
    if [ -f "$LOCK_DIR/pid" ]; then
        OLD=$(cat "$LOCK_DIR/pid" 2>/dev/null)
        if [ -n "$OLD" ] && kill -0 "$OLD" 2>/dev/null; then
            die "已有 nps 操作进行中 (PID $OLD)，请稍后重试"
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
    [ -n "$EXE" ] || return 1
    case "$EXE" in *" (deleted)") return 1 ;; esac
    [ "$(norm "$EXE")" = "$(norm "$BIN")" ]
}
find_all_nps() {
    if [ -f "$PID_FILE" ]; then
        p=$(cat "$PID_FILE" 2>/dev/null)
        case "$p" in ''|*[!0-9]*) ;; *) [ -d "/proc/$p" ] && echo "$p" ;; esac
    fi
    if command -v pgrep >/dev/null 2>&1; then
        pgrep -x natpunch 2>/dev/null
    elif command -v pidof >/dev/null 2>&1; then
        pidof nps 2>/dev/null | tr ' ' '\n'
    fi
    ps w 2>/dev/null | grep -F "$BIN" | grep -v grep | awk '{print $1}'
}
kill_one() {
    p="$1"; [ -n "$p" ] || return 0
    kill "$p" 2>/dev/null || true
    sleep 1
    kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true
}
kill_all() {
    LIST=$(find_all_nps)
    [ -n "$LIST" ] || return 0
    for p in $LIST; do [ "$p" != "$$" ] && kill "$p" 2>/dev/null || true; done
    sleep 1
    for p in $LIST; do
        [ "$p" != "$$" ] && kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true
    done
    sleep 1
}
# ================= 配置写入 =================
set_kv() {
    key="$1"; val="$2"
    case "$key" in *[!A-Za-z0-9_]*) die "非法配置键: $key" ;; esac
    [ -f "$CONF" ] || die "配置文件不存在: $CONF"
    awk -v k="$key" '
        {
            n = split($0, arr, k"=")
            if (n > 2) { next }
            if (n == 2) {
                line = $0
                pos = index(line, k"=")
                if (pos > 1) {
                    prev = substr(line, pos-1, 1)
                    if (prev != " " && prev != "\t" && prev != "#") { next }
                }
            }
            print
        }
    ' "$CONF" > "$CONF.clean" && mv "$CONF.clean" "$CONF"
    if grep -q "^[#[:space:]]*$key[[:space:]]*=" "$CONF" 2>/dev/null; then
        esc=$(printf '%s' "$val" | sed 's/[&|\\]/\\&/g')
        sed -i "s|^[#[:space:]]*$key[[:space:]]*=.*|$key=$esc|" "$CONF" || die "写入 $key 失败"
    else
        printf '%s=%s\n' "$key" "$val" >> "$CONF" || die "追加 $key 失败"
    fi
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
    case "$IN_PORT" in *[!0-9]*) die "端口必须是纯数字" ;; esac
    if [ "$IN_PORT" -lt 1 ] || [ "$IN_PORT" -gt 65535 ]; then die "端口范围必须是 1-65535"; fi
    printf "  用户名   [默认 admin]: "; read IN_USER
    [ -n "$IN_USER" ] || IN_USER="admin"
    printf "  密码     [默认 123  ]: "; read IN_PASS
    [ -n "$IN_PASS" ] || IN_PASS="123"
    case "$IN_USER" in *"="*|*"
"*) die "用户名不能包含 = 或换行" ;; esac
    case "$IN_PASS" in *"="*|*"
"*) die "密码不能包含 = 或换行" ;; esac
    NEW_PORT="$IN_PORT"; NEW_USER="$IN_USER"; NEW_PASS="$IN_PASS"
}
apply_credentials() {
    [ -f "$CONF" ] || die "配置文件不存在: $CONF"
    set_kv web_port "$NEW_PORT"
    set_kv web_username "$NEW_USER"
    set_kv web_password "$NEW_PASS"
    set_kv allow_user_change_username true
    log "已设置面板端口: $NEW_PORT"
    log "已设置面板账号: $NEW_USER"
}
choose_downloader() {
    if command -v wget >/dev/null 2>&1; then DL="wget -O"
    elif command -v curl >/dev/null 2>&1; then DL="curl -L -o"
    else die "缺少 wget/curl"; fi
}
get_latest_ver() {
    if command -v wget >/dev/null 2>&1; then RESP=$(wget -q -O - "$API_URL" 2>/dev/null)
    elif command -v curl >/dev/null 2>&1; then RESP=$(curl -sL "$API_URL" 2>/dev/null)
    else return 1; fi
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
    need tar; choose_downloader
    mkdir -p "$DIR" || die "无法创建 $DIR"
    TMP="$DIR/.install.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    log "下载 $VER ..."
    $DL nps.tar.gz "$URL" || { rm -rf "$TMP"; die "下载失败"; }
    log "解压 ..."
    tar -zxf nps.tar.gz || { rm -rf "$TMP"; die "解压失败"; }
    rm -f nps.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    CONF_SRC=$(find_first_dir . conf)
    WEB_SRC=$(find_first_dir . web)
    [ -n "$BIN_SRC" ]  || { rm -rf "$TMP"; die "压缩包中未找到 nps 二进制"; }
    [ -n "$CONF_SRC" ] || { rm -rf "$TMP"; die "压缩包中未找到 conf 目录"; }
    [ -n "$WEB_SRC" ]  || { rm -rf "$TMP"; die "压缩包中未找到 web 目录"; }
    cp "$BIN_SRC" "$DIR/nps.new" || { rm -rf "$TMP"; die "拷贝二进制失败"; }
    chmod +x "$DIR/nps.new" || { rm -rf "$TMP"; die "赋予执行权限失败"; }
    mv "$DIR/nps.new" "$BIN" || { rm -rf "$TMP"; die "替换二进制失败"; }
    if [ ! -d "$CONF_DIR" ]; then cp -r "$CONF_SRC" "$CONF_DIR" || { rm -rf "$TMP"; die "拷贝 conf 失败"; }; fi
    if [ ! -d "$WEB" ]; then cp -r "$WEB_SRC" "$WEB" || { rm -rf "$TMP"; die "拷贝 web 失败"; }; fi
    rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    log "安装完成"
}
upgrade() {
    need tar; choose_downloader
    [ -x "$BIN" ] || die "NatPunch 未安装，请先安装"
    TARGET_VER="${1:-$VER}"
    TARGET_URL="https://github.com/$REPO/releases/download/$TARGET_VER/linux_amd64_server.tar.gz"
    TMP="$DIR/.upgrade.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    log "下载 $TARGET_VER ..."
    $DL nps.tar.gz "$TARGET_URL" || { rm -rf "$TMP"; die "下载失败"; }
    log "解压 ..."
    tar -zxf nps.tar.gz || { rm -rf "$TMP"; die "解压失败"; }
    rm -f nps.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    WEB_SRC=$(find_first_dir . web)
    [ -n "$BIN_SRC" ] || { rm -rf "$TMP"; die "压缩包中未找到 nps 二进制"; }
    if [ -f "$BIN" ]; then
        BAK="$BIN.bak.$(date +%Y%m%d%H%M%S)"
        cp "$BIN" "$BAK" 2>/dev/null && log "已备份旧二进制: $BAK"
    fi
    cp "$BIN_SRC" "$DIR/nps.new" || { rm -rf "$TMP"; die "拷贝二进制失败"; }
    chmod +x "$DIR/nps.new" || { rm -rf "$TMP"; die "赋予执行权限失败"; }
    stop
    mv "$DIR/nps.new" "$BIN" || { rm -rf "$TMP"; die "替换二进制失败"; }
    if [ -n "$WEB_SRC" ]; then
        rm -rf "$WEB"
        cp -r "$WEB_SRC" "$WEB" || { rm -rf "$TMP"; die "拷贝 web 失败"; }
        log "已更新 web 目录"
    fi
    rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    start
    log "升级完成，当前版本: $TARGET_VER"
}
start() {
    if is_running; then log "已运行 (PID $(cat "$PID_FILE"))"; return 0; fi
    kill_all; rm -f "$PID_FILE"; cd "$DIR" || die "无法进入 $DIR"
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
        if command -v ss >/dev/null 2>&1; then ss -tlnp 2>/dev/null | grep -F "nps" || true; fi
    else
        log "未运行"
    fi
}
enable() {
    if [ ! -f /etc/openwrt_release ]; then log "非 OpenWrt 系统，跳过自启"; return 0; fi
    cat > /etc/init.d/natpunch <<EOL
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    if [ -f "$PID_FILE" ]; then
        OP=\$(cat "$PID_FILE" 2>/dev/null)
        [ -n "\$OP" ] && kill -0 "\$OP" 2>/dev/null && exit 0
    fi
    cd "$DIR" || exit 1
    "$BIN" >"$LOG" 2>&1 &
    NEWPID=\$!
    echo \$NEWPID > "$PID_FILE"
    sleep 1
    kill -0 \$NEWPID 2>/dev/null || {
        echo "[NatPunch] 启动失败" >&2
        rm -f "$PID_FILE"
        exit 1
    }
}
stop() {
    if [ -f "$PID_FILE" ]; then
        OP=\$(cat "$PID_FILE" 2>/dev/null)
        if [ -n "\$OP" ]; then
            kill "\$OP" 2>/dev/null
            sleep 1
            kill -0 "\$OP" 2>/dev/null && kill -9 "\$OP" 2>/dev/null
        fi
        rm -f "$PID_FILE"
    fi
}
EOL
    chmod +x /etc/init.d/natpunch || die "无法赋予 init 脚本执行权限"
    if /etc/init.d/natpunch enable; then
        if [ -e /etc/rc.d/S99natpunch ]; then log "已启用开机自启"
        else die "自启脚本未生效 (/etc/rc.d/S99natpunch 不存在)"; fi
    else die "启用开机自启失败"; fi
}
# ================= 清理客户端下载服务 =================
cleanup_download() {
    if [ ! -f /etc/openwrt_release ]; then
        log "非 OpenWrt 系统，跳过下载服务清理"
        return 0
    fi
    if command -v uci >/dev/null 2>&1; then
        uci delete uhttpd.download 2>/dev/null && log "已删除 uhttpd download 实例"
        uci commit uhttpd 2>/dev/null
        uci delete firewall.allow-npc-download 2>/dev/null && log "已删除 firewall 规则 allow-npc-download"
        uci commit firewall 2>/dev/null
        /etc/init.d/uhttpd restart >/dev/null 2>&1
        /etc/init.d/firewall restart >/dev/null 2>&1
    fi
    if [ -d "/opt/npc_download" ]; then
        rm -rf /opt/npc_download 2>/dev/null && log "已删除旧版下载目录: /opt/npc_download"
    fi
}
uninstall() {
    stop; kill_all
    cleanup_download
    rm -rf "$DIR"
    rm -f /etc/init.d/natpunch
    rm -f /etc/rc.d/*natpunch 2>/dev/null || true
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    log "已卸载"
}
get_ip() {
    IP=$(ip addr 2>/dev/null | grep 'inet ' | grep -v '127.0.0.1' | awk '{print $2}' | cut -d/ -f1 | head -n1)
    [ -n "$IP" ] || IP="<本机IP>"
    echo "$IP"
}
get_web_port() {
    if [ -f "$CONF" ]; then
        P=$(grep -E '^web_port=' "$CONF" 2>/dev/null | head -n1 | cut -d= -f2-)
        [ -n "$P" ] && echo "$P" && return 0
    fi
    echo "8080"
}
# ================= 客户端安装命令 =================
show_client_cmd() {
    local SERVER_IP
    SERVER_IP=$(get_ip)
    echo ""
    echo "=================================================="
    echo "  NatPunch 客户端安装命令"
    echo "=================================================="
    echo ""
    echo "先在 Web 面板【客户端】页添加客户端，复制 VKEY，然后在目标设备 SSH 粘贴："
    echo ""
    echo "【软路由 / OpenWrt】"
    echo "uclient-fetch -qO- https://raw.githubusercontent.com/$DL_REPO/master/install.sh | sh -s -- --openwrt '你的VKEY' $SERVER_IP 8025"
    echo ""
    echo "【服务器 / Linux】"
    echo "wget -qO- https://raw.githubusercontent.com/$DL_REPO/master/install.sh | sh -s -- --openwrt '你的VKEY' $SERVER_IP 8025"
    echo ""
    echo "=================================================="
    echo "说明："
    echo "  1. install.sh 直连 GitHub Release 自动下载最新客户端"
    echo "  2. 把 你的VKEY 换成面板生成的真实值"
    echo "  3. 8025 是 TLS 桥接端口，如改了端口请对应替换"
    echo "=================================================="
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
    echo "   6. 启用开机自启 (仅 OpenWrt)"
    echo "   7. 修改面板账号密码"
    echo "   8. 升级 NatPunch"
    echo "   9. 卸载 NatPunch"
    echo "  10. 显示客户端安装命令"
    echo "   0. 退出"
    echo "=================================================="
    printf "请输入选项 [0-10]: "
}
do_install() {
    acquire_lock
    trap 'release_lock' EXIT INT TERM
    prompt_credentials
    install || { release_lock; return 1; }
    apply_credentials
    enable
    start
    RC=$?
    release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip)
        echo ""
        echo "=================================================="
        echo "  ✅ 安装完成"
        echo "  面板地址: http://$IP:$NEW_PORT"
        echo "  用户名:   $NEW_USER"
        echo "  密码:     $NEW_PASS"
        echo "=================================================="
        echo ""
        echo "  客户端安装命令见下方（直连 GitHub Release，服务端无需存放文件）"
        show_client_cmd
    fi
}
do_start() {
    acquire_lock; trap 'release_lock' EXIT INT TERM
    start; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        echo ""; echo "  面板地址: http://$IP:$PORT"
    fi
}
do_stop() { acquire_lock; trap 'release_lock' EXIT INT TERM; stop; release_lock; }
do_restart() {
    acquire_lock; trap 'release_lock' EXIT INT TERM
    restart; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        echo ""; echo "  面板地址: http://$IP:$PORT"
    fi
}
do_status() { status; }
do_enable() { acquire_lock; trap 'release_lock' EXIT INT TERM; enable; release_lock; }
do_passwd() {
    if [ ! -f "$CONF" ]; then die "NatPunch 未安装，找不到配置文件 $CONF"; fi
    acquire_lock; trap 'release_lock' EXIT INT TERM
    OLD_USER=$(grep -E '^web_username=' "$CONF" | head -n1 | cut -d= -f2-)
    [ -n "$OLD_USER" ] || OLD_USER="admin"
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
    if [ "$IN_PORT" -lt 1 ] || [ "$IN_PORT" -gt 65535 ]; then die "端口范围必须是 1-65535"; fi
    printf "  新用户名 [回车保持 %s]: " "$OLD_USER"; read IN_USER
    [ -n "$IN_USER" ] || IN_USER="$OLD_USER"
    printf "  新密码 (不能为空): "; read IN_PASS
    [ -n "$IN_PASS" ] || die "密码不能为空"
    case "$IN_USER" in *"="*|*"
"*) die "用户名不能包含 = 或换行" ;; esac
    case "$IN_PASS" in *"="*|*"
"*) die "密码不能包含 = 或换行" ;; esac
    cp "$CONF" "$CONF.bak.$(date +%Y%m%d%H%M%S)" 2>/dev/null
    set_kv web_port "$IN_PORT"
    set_kv web_username "$IN_USER"
    set_kv web_password "$IN_PASS"
    set_kv allow_user_change_username true
    log "已写入配置，正在重启服务 ..."
    restart; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip)
        echo ""
        echo "=================================================="
        echo "  ✅ 修改完成"
        echo "  端口:   $IN_PORT"
        echo "  用户名: $IN_USER"
        echo "  密码:   $IN_PASS"
        echo "  面板:   http://$IP:$IN_PORT"
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
    if [ -n "$LATEST" ]; then DEFAULT_VER="$LATEST"; echo "  GitHub 最新版本: $LATEST"
    else DEFAULT_VER="$VER"; echo "  获取最新版本失败，回退到脚本默认: $VER"; fi
    printf "  目标版本 [回车使用 %s]: " "$DEFAULT_VER"; read IN_VER
    [ -n "$IN_VER" ] || IN_VER="$DEFAULT_VER"
    case "$IN_VER" in *"/"*|*" "*|*".."*) die "版本号不合法" ;; esac
    acquire_lock; trap 'release_lock' EXIT INT TERM
    upgrade "$IN_VER"; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        IP=$(get_ip); PORT=$(get_web_port)
        echo ""
        echo "=================================================="
        echo "  ✅ 升级完成"
        echo "  目标版本: $IN_VER"
        echo "  面板地址: http://$IP:$PORT"
        echo "=================================================="
    fi
}
do_uninstall() {
    printf "确认卸载 NatPunch？此操作会删除 $DIR、下载服务及所有配置 [y/N]: "; read ans
    case "$ans" in y|Y|yes|YES) ;; *) echo "已取消"; return 0 ;; esac
    acquire_lock; trap 'release_lock' EXIT INT TERM
    uninstall; release_lock
}
do_show_client_cmd() { show_client_cmd; }
# 命令行直调
if [ -n "${1:-}" ]; then
    case "$1" in
        install)   do_install ;;
        start)     do_start ;;
        stop)      do_stop ;;
        restart)   do_restart ;;
        status)    do_status ;;
        enable)    do_enable ;;
        passwd)    do_passwd ;;
        upgrade)
            shift
            if [ -n "${1:-}" ]; then
                acquire_lock; trap 'release_lock' EXIT INT TERM
                upgrade "$1"; RC=$?; release_lock
                [ $RC -eq 0 ] && log "升级完成: $1"
            else
                LATEST=$(get_latest_ver); [ -n "$LATEST" ] || LATEST="$VER"
                acquire_lock; trap 'release_lock' EXIT INT TERM
                upgrade "$LATEST"; RC=$?; release_lock
                [ $RC -eq 0 ] && log "升级完成: $LATEST"
            fi
            ;;
        uninstall)  do_uninstall ;;
        client-cmd) do_show_client_cmd ;;
        *) echo "用法: $0 {install|start|stop|restart|status|enable|passwd|upgrade [版本]|uninstall|client-cmd}" ;;
    esac
    exit $?
fi
# 交互菜单
while :; do
    show_menu
    read CHOICE
    case "$CHOICE" in
        1) do_install ;;
        2) do_start ;;
        3) do_stop ;;
        4) do_restart ;;
        5) do_status ;;
        6) do_enable ;;
        7) do_passwd ;;
        8) do_upgrade ;;
        9) do_uninstall ;;
        10) do_show_client_cmd ;;
        0) echo "已退出"; exit 0 ;;
        *) echo "无效选项，请重新输入" ;;
    esac
    echo ""
    printf "按回车键返回菜单..."
    read dummy
done
