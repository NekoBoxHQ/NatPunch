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
# 版本号不写死：下载优先走 get_latest_ver 探测到的最高版本 tag（releases/download/$VER），
# 探测失败才回退 releases/latest/download（自动指向最新发布）。
REPO="NekoBoxHQ/NatPunch"
SELF="$(basename "$0")"
SERVICE_NAME="natpunch"
# 发布方 minisign 公钥（内置默认，环境变量 MINISIGN_PUBKEY 可覆盖：自建发布链场景）
MINISIGN_PUBKEY="${MINISIGN_PUBKEY:-RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660}"

# 内置静态校验器（minisign-check）各架构 SHA256 信任锚，由发布方在打 tag 前填入。
# 必须内嵌于本脚本：若改用「同渠道下载的 SHA256SUMS」去校验校验器，在镜像被控时
# 攻击者可同时替换 包 / SHA256SUMS / 校验器 三者，构成循环信任，等于没有校验（复评⚠️1）。
# 留空 = 不启用内置校验器（仅告警跳过，SHA256 仍强制校验）；填入 = 锚定后才执行。
MSC_SHA256_amd64="e20c81421e5833c07a6c9c3d077650f7591effc63c1565b7086d9e133ea73576"
MSC_SHA256_arm64="152434e6f6d5aab0e8cafef602fc1a069b57b6d805d4954c5edb88550c6b9a9c"
MSC_SHA256_arm="517c7d12adeea6af6682b5957b81bb3f450cfdab1d7b7ade6abe7108e303ace1"
MSC_SHA256_mipsle="a4c2e73ac3a2810190882ecbf50d0a26accafe42843f48c73cb0709e83558865"
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
LINE="-------------------------------"
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
    case "${DIR:-}" in
        /?*) ;;
        *) die "DIR 配置非法: ${DIR:-}" ;;
    esac
    [ "${DIR:-}" != "/" ] || die "DIR 不能为根目录"
}
# 路径含空格安全：用 find | head -n1
find_first_file() {
    find "$1" -type f -name "$2" 2>/dev/null | head -n1
}
find_first_dir() {
    find "$1" -type d -name "$2" 2>/dev/null | head -n1
}
is_openwrt() { [ -f /etc/openwrt_release ]; }
has_systemd() { command -v systemctl >/dev/null 2>&1; }
# timeout 命令不存在时（精简 Linux）直接调用，保证卸载/自启清理不因缺少 timeout 而失败
tcmd() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "$@"
    else
        N="$1"; shift; "$@"
    fi
}
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
        if [ -n "${OLD:-}" ] && kill -0 "${OLD:-}" 2>/dev/null; then
            die "已有 NatPunch 操作进行中 (PID ${OLD})，请稍后重试"
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
    [ "${LOCK_MODE:-}" = "held" ] || return 0
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    LOCK_MODE=""
}
# ================= 进程判断 =================
is_running() {
    [ -f "$PID_FILE" ] || return 1
    PIDV=$(cat "$PID_FILE" 2>/dev/null) || return 1
    [ -n "${PIDV:-}" ] || return 1
    case "$PIDV" in *[!0-9]*) return 1 ;; esac
    [ -d "/proc/$PIDV" ] || return 1
    EXE=$(readlink "/proc/$PIDV/exe" 2>/dev/null)
    if [ -n "${EXE:-}" ]; then
        case "$EXE" in *" (deleted)") return 1 ;; esac
        [ "$(norm "$EXE")" = "$(norm "$BIN")" ] && return 0
        return 1
    fi
    CMD=$(tr '\0' ' ' < "/proc/$PIDV/cmdline" 2>/dev/null)
    case "$CMD" in *"$BIN"*) return 0 ;; esac
    return 1
}
find_all_natpunch() {
    if [ -f "$PID_FILE" ]; then
        p=$(cat "$PID_FILE" 2>/dev/null)
        case "$p" in ''|*[!0-9]*) ;; *) [ -d "/proc/$p" ] && echo "$p" ;; esac
    fi
    TARGET="$(norm "$BIN")"
    if command -v pgrep >/dev/null 2>&1; then
        for p in $(pgrep -x natpunch 2>/dev/null); do
            [ "$p" = "$$" ] && continue
            exe=$(readlink "/proc/$p/exe" 2>/dev/null)
            [ -n "${exe:-}" ] && [ "$(norm "$exe")" = "$TARGET" ] && echo "$p"
        done
    else
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null)
            [ -n "${exe:-}" ] && [ "$(norm "$exe")" = "$TARGET" ] && echo "$p"
        done
    fi
}
kill_one() {
    p="${1:-}"; [ -n "$p" ] || return 0
    kill "$p" 2>/dev/null || true
    sleep 1
    kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true
}
kill_all() {
    LIST=$(find_all_natpunch | sort -u)
    [ -n "${LIST:-}" ] || return 0
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
    key="${1:-}"
    [ -f "$CONF" ] || return 1
    grep -E "^[[:space:]]*$key[[:space:]]*=" "$CONF" 2>/dev/null | head -n1 | sed "s/^[[:space:]]*$key[[:space:]]*=[[:space:]]*//"
}
set_kv() {
    key="${1:-}"; val="${2:-}"
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
# ================= 安全输入 =================
read_secret() {
    # read_secret <prompt> <varname>
    printf "  %s" "$1"
    stty -echo 2>/dev/null
    read "$2"
    stty echo 2>/dev/null
    echo
}
# ================= 交互输入 =================
prompt_credentials() {
    section "设置 NatPunch 面板登录信息"
    printf "  Web 端口 [默认 8080]: "; read IN_PORT
    [ -n "${IN_PORT:-}" ] || IN_PORT="8080"
    case "$IN_PORT" in *[!0-9]*) die "Web 端口必须是纯数字" ;; esac
    [ "$IN_PORT" -ge 1 ] && [ "$IN_PORT" -le 65535 ] || die "Web 端口范围必须是 1-65535"
    printf "  客户端 HTTP 端口 [默认 8024]: "; read IN_BRIDGE
    [ -n "${IN_BRIDGE:-}" ] || IN_BRIDGE="8024"
    case "$IN_BRIDGE" in *[!0-9]*) die "客户端 HTTP 端口必须是纯数字" ;; esac
    [ "$IN_BRIDGE" -ge 1 ] && [ "$IN_BRIDGE" -le 65535 ] || die "客户端 HTTP 端口范围必须是 1-65535"
    printf "  客户端 HTTPS 端口 [默认 8025]: "; read IN_TLS_PORT
    [ -n "${IN_TLS_PORT:-}" ] || IN_TLS_PORT="8025"
    case "$IN_TLS_PORT" in *[!0-9]*) die "客户端 HTTPS 端口必须是纯数字" ;; esac
    [ "$IN_TLS_PORT" -ge 1 ] && [ "$IN_TLS_PORT" -le 65535 ] || die "客户端 HTTPS 端口范围必须是 1-65535"
    printf "  启用 HTTPS/TLS (y/N): "; read IN_HTTPS
    NEW_HTTPS="false"; NEW_CERT=""; NEW_KEY=""; NEW_DOMAIN=""
    case "${IN_HTTPS:-}" in
        y|Y|yes|YES)
            NEW_HTTPS="true"
            printf "  域名: "; read NEW_DOMAIN
            [ -n "${NEW_DOMAIN:-}" ] || NEW_DOMAIN="$(get_ip)"
            printf "  pem [默认 /opt/natpunch/conf/server.pem]: "; read IN_CERT
            [ -n "${IN_CERT:-}" ] && NEW_CERT="$IN_CERT" || NEW_CERT="/opt/natpunch/conf/server.pem"
            printf "  key [默认 /opt/natpunch/conf/server.key]: "; read IN_KEY
            [ -n "${IN_KEY:-}" ] && NEW_KEY="$IN_KEY" || NEW_KEY="/opt/natpunch/conf/server.key"
            ;;
    esac
    printf "  用户名 [默认 admin]: "; read IN_USER
    [ -n "${IN_USER:-}" ] || IN_USER="admin"
    # 密码强制输入：空输入不允许落 123（F1-9）
    while :; do
        read_secret "密码   [必填，不允许为空]: " IN_PASS
        [ -n "${IN_PASS:-}" ] && break
        warn "密码不能为空，请重新输入"
    done
    case "$IN_USER" in *"="*) die "用户名不能包含 =" ;; esac
    case "$IN_PASS" in *"="*) die "密码不能包含 =" ;; esac
    NEW_PORT="$IN_PORT"; NEW_BRIDGE="$IN_BRIDGE"; NEW_TLS_PORT="$IN_TLS_PORT"; NEW_USER="$IN_USER"; NEW_PASS="$IN_PASS"
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
        set_kv tls_bridge_port "$NEW_TLS_PORT"
        set_kv web_open_ssl "true"
        set_kv web_cert_file "$NEW_CERT"
        set_kv web_key_file "$NEW_KEY"
        set_kv web_domain "$NEW_DOMAIN"
        info "已启用 HTTPS/TLS: $NEW_DOMAIN"
        info "pem: $NEW_CERT"
        info "key: $NEW_KEY"
        info "客户端 TLS 端口: $NEW_TLS_PORT"
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
    [ -n "${DL_TYPE:-}" ] || choose_downloader
    case "$DL_TYPE" in
        wget) wget -q --timeout=30 --tries=2 -O "$2" "$1" ;;
        curl) curl -fsSL --max-time 60 --retry 2 -o "$2" "$1" ;;
    esac
}
verify_package() {
    # verify_package <pkg_name> <base_url>：强制 SHA256 校验（F2-8），失败即中止；
    # 存在 minisign + MINISIGN_PUBKEY 时再做签名校验（失败即中止），否则警告跳过。
    # 调用前需已在临时目录内（$TMP）。
    local f="$1" base="$2"
    dl "$base/SHA256SUMS" SHA256SUMS || { cd /; rm -rf "$TMP"; die "获取 SHA256SUMS 失败"; }
    [ -s SHA256SUMS ] || { cd /; rm -rf "$TMP"; die "SHA256SUMS 为空"; }
    local EXPECT
    EXPECT=$(awk -v f="$f" '$2==f {print $1; exit}' SHA256SUMS)
    [ -n "$EXPECT" ] || { cd /; rm -rf "$TMP"; die "SHA256SUMS 中无 $f 条目"; }
    local ACTUAL
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL=$(sha256sum natpunch.tar.gz | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        ACTUAL=$(shasum -a 256 natpunch.tar.gz | awk '{print $1}')
    else
        cd /; rm -rf "$TMP"; die "未找到 sha256sum/shasum 工具，无法校验"
    fi
    [ "$EXPECT" = "$ACTUAL" ] || { cd /; rm -rf "$TMP"; die "sha256 校验失败（期望 $EXPECT 实际 $ACTUAL）"; }
    info "sha256 校验通过 ($f)"
    # 签名校验：系统 minisign → 内置静态校验器（minisign-check）→ 降级 SHA256 兜底。
    # 工具可得但校验失败即中止；发布未提供签名文件或校验工具不可得才警告跳过（SHA256 已强制）。
    if [ -n "${MINISIGN_PUBKEY:-}" ]; then
        if dl "$base/SHA256SUMS.minisig" SHA256SUMS.minisig; then
            local sig_ok=0
            if command -v minisign >/dev/null 2>&1; then
                if minisign -Vm SHA256SUMS -P "$MINISIGN_PUBKEY" -x SHA256SUMS.minisig >minisign.out 2>&1; then
                    sed 's/^/         /' minisign.out
                    info "minisign 签名校验通过"
                    sig_ok=1
                else
                    cd /; rm -rf "$TMP"; die "minisign 签名校验失败"
                fi
            fi
            if [ "$sig_ok" -eq 0 ]; then
                local MSC_ARCH=""
                case "$(uname -m)" in
                    x86_64|amd64) MSC_ARCH="amd64" ;;
                    aarch64|arm64) MSC_ARCH="arm64" ;;
                    armv7l|armv6l) MSC_ARCH="arm" ;;
                    mips|mipsel|mipsle) MSC_ARCH="mipsle" ;;
                esac
                if [ -n "$MSC_ARCH" ]; then
                    local MSC="minisign-check-linux-$MSC_ARCH"
                    if dl "$base/$MSC" "$MSC"; then
                        # 校验器与包同源下载：其哈希须与脚本内嵌信任锚一致，否则拒绝执行（防镜像篡改校验器）
                        local MSC_EXPECT MSC_ACTUAL
                        # 信任锚取自脚本内嵌常量，而非同渠道下载的 SHA256SUMS（避免循环信任，复评⚠️1）
                        case "$MSC_ARCH" in
                            amd64)  MSC_EXPECT="$MSC_SHA256_amd64" ;;
                            arm64)  MSC_EXPECT="$MSC_SHA256_arm64" ;;
                            arm)    MSC_EXPECT="$MSC_SHA256_arm" ;;
                            mipsle) MSC_EXPECT="$MSC_SHA256_mipsle" ;;
                            *)      MSC_EXPECT="" ;;
                        esac
                        if [ -n "$MSC_EXPECT" ]; then
                            if command -v sha256sum >/dev/null 2>&1; then
                                MSC_ACTUAL=$(sha256sum "$MSC" | awk '{print $1}')
                            elif command -v shasum >/dev/null 2>&1; then
                                MSC_ACTUAL=$(shasum -a 256 "$MSC" | awk '{print $1}')
                            else
                                MSC_ACTUAL=""
                            fi
                            if [ -n "$MSC_ACTUAL" ] && [ "$MSC_EXPECT" = "$MSC_ACTUAL" ]; then
                                chmod +x "$MSC" 2>/dev/null || true
                                printf 'untrusted comment: minisign public key\n%s\n' "$MINISIGN_PUBKEY" > natpunch.pub
                                if "./$MSC" natpunch.pub SHA256SUMS.minisig SHA256SUMS >msc.out 2>&1; then
                                    sed 's/^/         /' msc.out
                                    info "minisign 签名校验通过（内置静态校验器）"
                                    sig_ok=1
                                else
                                    cd /; rm -rf "$TMP"; die "minisign 签名校验失败（内置校验器）"
                                fi
                            else
                                warn "内置校验器 $MSC 哈希与内嵌信任锚不符，拒绝执行，跳过签名校验（SHA256 已强制校验）"
                            fi
                        else
                            warn "内置校验器 $MSC 未配置信任锚（本脚本 MSC_SHA256_* 为空），跳过签名校验（SHA256 已强制校验）"
                        fi
                    else
                        warn "无法下载内置校验器 $MSC，跳过签名校验（SHA256 已强制校验）"
                    fi
                fi
            fi
            [ "$sig_ok" -eq 1 ] || warn "未找到签名校验工具，跳过签名校验（SHA256 已强制校验）"
        else
            warn "发布未提供 SHA256SUMS.minisig，跳过签名校验（SHA256 已强制校验）"
        fi
    else
        warn "未配置 MINISIGN_PUBKEY，跳过签名校验（SHA256 已强制校验）"
    fi
}
fetch() {
    [ -n "${DL_TYPE:-}" ] || choose_downloader
    case "$DL_TYPE" in
        wget) wget -q --timeout=10 -O - "$1" 2>/dev/null ;;
        curl) curl -fsSL --max-time 10 "$1" 2>/dev/null ;;
    esac
}
get_latest_ver() {
    # 按版本号取最高：GitHub releases/latest 按【发布时间】排序，并行/连续发版时
    # 会指向后发布但版本号更低的 tag（如 98 晚于 99 发布 → latest=98）。
    # 故改用 releases 列表按 tag 版本排序；失败回退 tags API（最近推送在前）。
    V=""
    RESP=$(fetch "https://api.github.com/repos/$REPO/releases?per_page=20") || RESP=""
    if [ -n "${RESP:-}" ]; then
        V=$(echo "$RESP" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | grep -E '^v[0-9]' | sort -V | tail -n1)
    fi
    if [ -z "${V:-}" ]; then
        RESP=$(fetch "https://api.github.com/repos/$REPO/tags?per_page=10") || RESP=""
        V=$(echo "$RESP" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | grep -E '^v[0-9]' | head -n1)
    fi
    [ -n "${V:-}" ] && echo "$V" && return 0
    return 1
}
get_current_ver() {
    if [ -x "$BIN" ]; then
        V=$("$BIN" -version 2>/dev/null | head -n1)
        [ -n "${V:-}" ] && echo "$V" && return 0
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
        armv7l|armv6l) SERVER_PKG="linux_arm_server.tar.gz";;
        mips|mipsel|mipsle) SERVER_PKG="linux_mipsle_server.tar.gz";;
        *) die "不支持的架构: $ARCH（支持 amd64/arm64/armv7/mipsle）；请手动下载对应安装包";;
    esac
    info "下载最新发布 ($SERVER_PKG) ..."
    VER=$(get_latest_ver) || VER=""
    if [ -n "$VER" ]; then
        BASE_URL="https://github.com/$REPO/releases/download/$VER"
        info "最新版本: $VER"
    else
        BASE_URL="https://github.com/$REPO/releases/latest/download"
    fi
    dl "$BASE_URL/$SERVER_PKG" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    verify_package "$SERVER_PKG" "$BASE_URL"
    info "解压安装包 ..."
    tar -zxf natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "解压失败"; }
    rm -f natpunch.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    CONF_SRC=$(find_first_dir . conf)
    WEB_SRC=$(find_first_dir . web)
    [ -n "${BIN_SRC:-}" ]  || { cd /; rm -rf "$TMP"; die "压缩包中未找到 natpunch 二进制"; }
    [ -n "${CONF_SRC:-}" ] || { cd /; rm -rf "$TMP"; die "压缩包中未找到 conf 目录"; }
    [ -n "${WEB_SRC:-}" ]  || { cd /; rm -rf "$TMP"; die "压缩包中未找到 web 目录"; }
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
    # 升级前备份配置目录（clients.json 等含全部客户端 vkey）——vkey 升级不可变，
    # 异常覆盖时可用备份恢复，杜绝客户端批量掉线
    CONF_BAK=""
    if [ -d "$CONF_DIR" ]; then
        CONF_BAK="$DIR/conf.bak.$(date +%Y%m%d%H%M%S)"
        mkdir -p "$CONF_BAK" && cp -a "$CONF_DIR/." "$CONF_BAK/" 2>/dev/null \
            && info "已备份配置目录: $CONF_BAK" || CONF_BAK=""
    fi
    TARGET_VER="${1:-}"
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) SERVER_PKG="linux_amd64_server.tar.gz";;
        aarch64|arm64) SERVER_PKG="linux_arm64_server.tar.gz";;
        armv7l|armv6l) SERVER_PKG="linux_arm_server.tar.gz";;
        mips|mipsel|mipsle) SERVER_PKG="linux_mipsle_server.tar.gz";;
        *) die "不支持的架构: $ARCH（支持 amd64/arm64/armv7/mipsle）；请手动下载对应安装包";;
    esac
    if [ -n "$TARGET_VER" ]; then
        TARGET_URL="https://github.com/$REPO/releases/download/$TARGET_VER/$SERVER_PKG"
        BASE_URL="https://github.com/$REPO/releases/download/$TARGET_VER"
        info "下载 $TARGET_VER ..."
    else
        # 按版本号取最高（latest 按发布时间排序，并行发版会指向旧版）
        VER=$(get_latest_ver) || VER=""
        if [ -n "$VER" ]; then
            TARGET_URL="https://github.com/$REPO/releases/download/$VER/$SERVER_PKG"
            BASE_URL="https://github.com/$REPO/releases/download/$VER"
            info "下载最新发布 $VER ..."
        else
            TARGET_URL="https://github.com/$REPO/releases/latest/download/$SERVER_PKG"
            BASE_URL="https://github.com/$REPO/releases/latest/download"
            info "下载最新发布 ..."
        fi
    fi
    TMP="$DIR/.upgrade.$$"; rm -rf "$TMP"; mkdir -p "$TMP" || die "无法创建临时目录"
    cd "$TMP" || die "无法进入临时目录"
    dl "$TARGET_URL" natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "下载失败"; }
    verify_package "$SERVER_PKG" "$BASE_URL"
    info "解压安装包 ..."
    tar -zxf natpunch.tar.gz || { cd /; rm -rf "$TMP"; die "解压失败"; }
    rm -f natpunch.tar.gz
    BIN_SRC=$(find_first_file . natpunch)
    WEB_SRC=$(find_first_dir . web)
    [ -n "${BIN_SRC:-}" ] || { cd /; rm -rf "$TMP"; die "压缩包中未找到 natpunch 二进制"; }
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
        [ -n "${BAK:-}" ] && [ -f "$BAK" ] && cp "$BAK" "$BIN"
        [ "$WAS_RUNNING" = "1" ] && start
        cd /; rm -rf "$TMP"; die "替换二进制失败"
    fi
    if [ -n "${WEB_SRC:-}" ]; then
        rm -rf "$WEB"
        cp -r "$WEB_SRC" "$WEB" || { cd /; rm -rf "$TMP"; die "拷贝 web 失败"; }
        info "已更新 web 目录"
    fi
    cd /; rm -rf "$TMP"
    [ -f "$CONF" ] || die "缺少配置文件 $CONF"
    # 升级后自检：客户端数据（clients.json）缺失/为空/条数少于备份 → 自动恢复（vkey 不可变）
    if [ -n "$CONF_BAK" ] && [ -s "$CONF_BAK/clients.json" ]; then
        CNT_NEW=$(grep -c '"VerifyKey"' "$CONF_DIR/clients.json" 2>/dev/null || echo 0)
        CNT_BAK=$(grep -c '"VerifyKey"' "$CONF_BAK/clients.json" 2>/dev/null || echo 0)
        if [ ! -s "$CONF_DIR/clients.json" ] || [ "$CNT_NEW" -lt "$CNT_BAK" ]; then
            warn "升级后客户端数据不完整（备份 $CNT_BAK 条 / 当前 $CNT_NEW 条），自动从备份恢复"
            cp -a "$CONF_BAK/." "$CONF_DIR/" || warn "备份恢复失败，请手动检查 $CONF_BAK"
        fi
    fi
    set_kv http_proxy_port  0
    set_kv https_proxy_port 0
    set_kv web_host         0.0.0.0
    start
    # 升级成功后才清理旧备份（失败/回滚路径不清理，保证可回滚）
    cleanup_old_backups
    log "升级完成，当前版本: ${TARGET_VER:-最新版}"
}
# ================= 备份清理 =================
# 升级/改密会累积 natpunch.bak.* 与 natpunch.conf.bak.* 备份，
# 每次升级后只保留最新一份，避免历史备份无限累积。
clean_backups() {
    PRE="$1"; DESC="$2"
    LIST=""
    for f in "$PRE".bak.*; do
        [ -e "$f" ] || continue
        LIST="$LIST $f"
    done
    [ -z "$LIST" ] && return 0
    # 时间戳 %Y%m%d%H%M%S 定长，字典序即时间序，取最后一个保留
    # shellcheck disable=SC2086（LIST 为 glob 展开结果，路径不含空格）
    set -- $(printf '%s\n' $LIST | sort)
    N=$#
    DEL=0
    i=1
    for f in "$@"; do
        if [ "$i" -lt "$N" ]; then
            rm -f "$f" 2>/dev/null && DEL=$((DEL+1))
        fi
        i=$((i+1))
    done
    [ "$DEL" -gt 0 ] && info "$DESC：已清理 $DEL 份旧备份，仅保留最新"
}
cleanup_old_backups() {
    clean_backups "$BIN"  "二进制备份"
    clean_backups "$CONF" "面板配置备份"
}
# ================= 启停 =================
start() {
    if is_running; then
        info "服务已在运行 (PID $(cat "$PID_FILE"))"
        return 0
    fi
    kill_all; rm -f "$PID_FILE"
    # systemd 服务优先（单进程管理：避免 nohup+systemd 双进程各持内存、互相覆盖 clients.json 导致 vkey 丢失）
    if [ -f /etc/systemd/system/natpunch.service ] || systemctl list-unit-files natpunch.service >/dev/null 2>&1; then
        systemctl start natpunch 2>/dev/null
        sleep 2
        if systemctl is-active natpunch >/dev/null 2>&1; then
            log "启动成功 (systemd 单进程)"
            return 0
        fi
        warn "systemd 启动失败，回退 nohup 启动"
    fi
    (
        cd "$DIR" || exit 1
        nohup "$BIN" >"$LOG" 2>&1 &
        echo $! > "$PID_FILE"
    )
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
    systemctl stop natpunch 2>/dev/null || true
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
        if [ -f /sbin/procd ] || grep -q "USE_PROCD" /etc/rc.common 2>/dev/null; then
            cat > /etc/init.d/natpunch <<'EOL'
#!/bin/sh /etc/rc.common
START=99
STOP=10
USE_PROCD=1
start_service() {
    procd_open_instance
    procd_set_param command /opt/natpunch/natpunch
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_set_param cwd /opt/natpunch
    procd_close_instance
}
EOL
        else
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
        fi
        chmod +x /etc/init.d/natpunch || die "无法赋予 init 脚本执行权限"
        /etc/init.d/natpunch enable 2>/dev/null && log "已启用开机自启 (init.d)" || warn "启用开机自启失败"
        return 0
    fi
    if has_systemd; then
        info "注册 systemd 自启 ..."
        cat > /etc/systemd/system/natpunch.service <<EOF
[Unit]
Description=NatPunch Server
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
WorkingDirectory=$DIR
ExecStart=$BIN
Restart=always
RestartSec=3
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
        rm -f /etc/init.d/natpunch
        rm -f /etc/rc.d/S*natpunch /etc/rc.d/K*natpunch 2>/dev/null
    fi
    if has_systemd; then
        tcmd 8 systemctl stop natpunch >/dev/null 2>&1 || true
        tcmd 8 systemctl disable natpunch >/dev/null 2>&1 || true
        rm -f /etc/systemd/system/natpunch.service
        tcmd 8 systemctl daemon-reload >/dev/null 2>&1 || true
    fi
}
cleanup_download() {
    if ! is_openwrt; then info "非 OpenWrt，跳过下载服务清理"; return 0; fi
    if command -v uci >/dev/null 2>&1; then
        uci show uhttpd.download >/dev/null 2>&1 && { uci delete uhttpd.download 2>/dev/null; uci commit uhttpd 2>/dev/null; info "已删除 uhttpd download"; }
        uci show firewall.allow-npc-download >/dev/null 2>&1 && { uci delete firewall.allow-npc-download 2>/dev/null; uci commit firewall 2>/dev/null; info "已删除 firewall 规则"; }
        tcmd 15 /etc/init.d/uhttpd restart >/dev/null 2>&1
        tcmd 20 /etc/init.d/firewall restart >/dev/null 2>&1
    fi
    [ -d "/opt/npc_download" ] && rm -rf /opt/npc_download 2>/dev/null && info "已删除旧版下载目录"
}
uninstall() {
    safe_dir; stop; kill_all
    info "清理自启服务 ..."
    unregister_autostart; cleanup_download
    if [ -n "${DIR:-}" ] && [ "$DIR" != "/" ]; then
        info "删除安装目录 $DIR ..."
        tcmd 20 rm -rf "$DIR" || warn "删除 $DIR 超时，请稍后手动清理"
    fi
    rm -rf "$LOCK_DIR" 2>/dev/null || true
    log "已卸载"
}
# ================= 信息获取 =================
get_ip() {
    IP=""
    for u in "https://api.ipify.org" "https://ifconfig.me/ip" "https://ipinfo.io/ip"; do
        if command -v wget >/dev/null 2>&1; then
            IP=$(wget -qO- --timeout=3 "$u" 2>/dev/null | head -n1)
        elif command -v curl >/dev/null 2>&1; then
            IP=$(curl -fsSL --max-time 3 "$u" 2>/dev/null | head -n1)
        fi
        [ -n "${IP:-}" ] && break
    done
    [ -z "${IP:-}" ] && IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1)
    [ -z "${IP:-}" ] && IP=$(ip addr 2>/dev/null | grep 'inet ' | grep -v '127.0.0.1' | awk '{print $2}' | cut -d/ -f1 | head -n1)
    [ -n "${IP:-}" ] || IP="<本机IP>"
    echo "$IP"
}
get_web_port() {
    P=$(get_kv web_port)
    [ -n "${P:-}" ] && echo "$P" && return 0
    echo "8080"
}
# ================= 菜单 =================
show_menu() {
    echo ""
    printf '%b\n' "${C_CYAN}${LINE}${C_RESET}"
    printf '%b\n' "${C_BOLD}         服务端管理脚本${C_RESET}"
    printf '%b\n' "${C_CYAN}${LINE}${C_RESET}"
    printf '%b\n' "   ${C_GREEN}1${C_RESET}  安装 NatPunch"
    printf '%b\n' "   ${C_GREEN}2${C_RESET}  启动 NatPunch"
    printf '%b\n' "   ${C_GREEN}3${C_RESET}  停止 NatPunch"
    printf '%b\n' "   ${C_GREEN}4${C_RESET}  重启 NatPunch"
    printf '%b\n' "   ${C_GREEN}5${C_RESET}  NatPunch 状态"
    printf '%b\n' "   ${C_GREEN}6${C_RESET}  NatPunch 配置"
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
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "${HOST:-}" ] || HOST="$(get_ip)"
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
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "${HOST:-}" ] || HOST="$(get_ip)"
        else SCHEME="http"; HOST="$(get_ip)"; fi
        echo ""; kv "面板地址" "$SCHEME://$HOST:$PORT"; echo ""
    fi
}
do_status() { title "运行状态"; status; echo ""; }
do_passwd() {
    if [ ! -f "$CONF" ]; then die "NatPunch 未安装，找不到配置文件 $CONF"; fi
    title "NatPunch 配置"
    acquire_lock
    OLD_USER=$(get_kv web_username); [ -n "${OLD_USER:-}" ] || OLD_USER="admin"
    OLD_PORT=$(get_web_port)
    section "当前信息"
    kv "用户名" "$OLD_USER"; kv "Web 端口" "$OLD_PORT"
    info "客户端连接端口保持不变（避免已在线客户端失联）"
    section "输入新信息（回车保持当前值）"
    printf "  新 Web 端口 [回车保持 %s]: " "$OLD_PORT"; read IN_PORT
    [ -n "${IN_PORT:-}" ] || IN_PORT="$OLD_PORT"
    case "$IN_PORT" in *[!0-9]*) die "端口必须是纯数字" ;; esac
    [ "$IN_PORT" -ge 1 ] && [ "$IN_PORT" -le 65535 ] || die "端口范围必须是 1-65535"
    printf "  新用户名 [回车保持 %s]: " "$OLD_USER"; read IN_USER
    [ -n "${IN_USER:-}" ] || IN_USER="$OLD_USER"
    read_secret "新密码 (不能为空): " IN_PASS
    [ -n "${IN_PASS:-}" ] || die "密码不能为空"
    case "$IN_USER" in *"="*) die "用户名不能包含 =" ;; esac
    case "$IN_PASS" in *"="*) die "密码不能包含 =" ;; esac
    cp "$CONF" "$CONF.bak.$(date +%Y%m%d%H%M%S)" 2>/dev/null
    set_kv web_port "$IN_PORT"; set_kv web_username "$IN_USER"; set_kv web_password "$IN_PASS"
    set_kv allow_user_change_username true
    info "已写入配置，正在重启服务 ..."
    restart; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "${HOST:-}" ] || HOST="$(get_ip)"
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
    if [ -n "${LATEST:-}" ]; then
        info "GitHub 最新版本: $LATEST"
    else
        warn "无法获取最新版本号（将直接下载最新发布）"
    fi
    printf "         目标版本 [回车使用最新版]: "; read IN_VER
    case "${IN_VER:-}" in
        '') ;;
        *[!A-Za-z0-9._-]*) die "版本号不合法（仅允许字母数字 . _ -）" ;;
    esac
    acquire_lock; upgrade "$IN_VER"; RC=$?; release_lock
    if [ $RC -eq 0 ]; then
        PORT=$(get_web_port)
        if grep -q "^web_open_ssl=true" "$CONF" 2>/dev/null; then
            SCHEME="https"; HOST=$(get_kv web_domain 2>/dev/null); [ -n "${HOST:-}" ] || HOST="$(get_ip)"
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
    case "${ans:-}" in y|Y|yes|YES) ;; *) echo "  已取消"; return 0 ;; esac
    acquire_lock; uninstall; release_lock
    echo ""
    printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
    printf '%b\n' "${C_GREEN}${C_BOLD}  [OK] 卸载完成${C_RESET}"
    printf '%b\n' "${C_GREEN}${LINE}${C_RESET}"
    echo ""
    exit 0
}
# ================= 入口 =================
trap 'release_lock; exit 130' INT TERM
trap 'release_lock' EXIT
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
    if ! read -r CHOICE; then echo "已退出"; exit 0; fi
    case "${CHOICE:-}" in
        1) do_install ;; 2) do_start ;; 3) do_stop ;; 4) do_restart ;; 5) do_status ;;
        6) do_passwd ;; 7) do_upgrade ;; 8) do_uninstall ;; 0) echo "已退出"; exit 0 ;;
        '') ;;  # 空回车：直接重显菜单，不报"无效选项"
        *) echo "无效选项，请重新输入" ;;
    esac
    echo ""
    printf "按回车键返回菜单..."
    read -r dummy || exit 0
done
