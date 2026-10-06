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
    armv7l|armv6l)  PKG="linux_arm_client.tar.gz";;
    mips|mipsel|mipsle) PKG="linux_mipsle_client.tar.gz";;
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
# ---------- 获取版本（按版本号取最高，latest 按发布时间排序会指向旧版） ----------
log "获取最新版本..."
VER=""
VER_LIST=""
if [ "$HAS_WGET" -eq 1 ]; then
    VER_LIST="$(wget -qO- --timeout=10 "https://api.github.com/repos/$REPO/releases?per_page=20" 2>/dev/null)"
else
    VER_LIST="$(curl -fsSL --max-time 10 "https://api.github.com/repos/$REPO/releases?per_page=20" 2>/dev/null)"
fi
VER="$(echo "$VER_LIST" | grep '"tag_name"' | sed 's/.*: *"\([^"]*\)".*/\1/' | grep -E '^v[0-9]' | sort -V | tail -n1)"
echo "    ${VER:-最新发布}"
# ---------- 下载 ----------
if [ -n "$VER" ]; then
    BASE_URL="https://github.com/$REPO/releases/download/$VER"
else
    BASE_URL="https://github.com/$REPO/releases/latest/download"
fi
URL="$BASE_URL/$PKG"
log "下载 $PKG ..."
mkdir -p "$TMP_DIR" || die "无法创建临时目录 $TMP_DIR"
fetch "$URL" "$TMP_DIR/pkg.tar.gz" || die "下载失败: $URL"
[ -s "$TMP_DIR/pkg.tar.gz" ] || die "下载文件为空: $URL"
# 强制 SHA256 校验（F2-8）：校验失败即中止
log "校验 sha256..."
fetch "$BASE_URL/SHA256SUMS" "$TMP_DIR/SHA256SUMS" || die "获取 SHA256SUMS 失败"
[ -s "$TMP_DIR/SHA256SUMS" ] || die "SHA256SUMS 为空"
# 第二列可能带一个前导 '*'（coreutils 的二进制模式标记），去掉再比
EXPECT="$(awk -v f="$PKG" '{ gsub(/^\*/, "", $2) } $2==f {print $1; exit}' "$TMP_DIR/SHA256SUMS")"
[ -n "$EXPECT" ] || die "SHA256SUMS 中无 $PKG 条目"
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP_DIR/pkg.tar.gz" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "$TMP_DIR/pkg.tar.gz" | awk '{print $1}')"
else
    die "未找到 sha256sum/shasum 工具，无法校验"
fi
[ "$EXPECT" = "$ACTUAL" ] || die "sha256 校验失败（期望 $EXPECT，实际 $ACTUAL）"
log "sha256 校验通过"
# 分级签名校验：系统 minisign → 内置静态校验器（minisign-check，OpenWrt 无 minisign 包场景）→ 降级 SHA256 兜底
# 环境变量 MINISIGN_PUBKEY 可覆盖内置公钥（自建发布链场景）
MINISIGN_PUBKEY="${MINISIGN_PUBKEY:-RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660}"

# 内置静态校验器（minisign-check）各架构 SHA256 信任锚，由发布方在打 tag 前填入。
# 必须内嵌于本脚本：若改用「同渠道下载的 SHA256SUMS」去校验校验器，在镜像被控时
# 攻击者可同时替换 包 / SHA256SUMS / 校验器 三者，构成循环信任，等于没有校验（复评⚠️1）。
# 留空 = 不启用内置校验器（仅告警跳过，SHA256 仍强制校验）；填入 = 锚定后才执行。
MSC_SHA256_amd64="e20c81421e5833c07a6c9c3d077650f7591effc63c1565b7086d9e133ea73576"
MSC_SHA256_arm64="152434e6f6d5aab0e8cafef602fc1a069b57b6d805d4954c5edb88550c6b9a9c"
MSC_SHA256_arm="517c7d12adeea6af6682b5957b81bb3f450cfdab1d7b7ade6abe7108e303ace1"
MSC_SHA256_mipsle="a4c2e73ac3a2810190882ecbf50d0a26accafe42843f48c73cb0709e83558865"

sig_ok=0
if [ -n "${MINISIGN_PUBKEY:-}" ]; then
    if fetch "$BASE_URL/SHA256SUMS.minisig" "$TMP_DIR/SHA256SUMS.minisig"; then
        # 1) 系统 minisign（存在即强制校验，失败即中止）
        if command -v minisign >/dev/null 2>&1; then
            minisign -Vm "$TMP_DIR/SHA256SUMS" -P "$MINISIGN_PUBKEY" -x "$TMP_DIR/SHA256SUMS.minisig" || die "minisign 签名校验失败"
            log "minisign 签名校验通过"
            sig_ok=1
        fi
        # 2) 内置静态校验器（与包同源下载；其哈希须与脚本内嵌信任锚一致，否则拒绝执行）
        if [ "$sig_ok" -eq 0 ]; then
            case "$ARCH" in
                x86_64|amd64) MSC_ARCH="amd64";;
                aarch64|arm64) MSC_ARCH="arm64";;
                armv7l|armv6l) MSC_ARCH="arm";;
                mips|mipsel|mipsle) MSC_ARCH="mipsle";;
                *) MSC_ARCH="" ;;
            esac
            if [ -n "$MSC_ARCH" ]; then
                MSC="minisign-check-linux-$MSC_ARCH"
                if fetch "$BASE_URL/$MSC" "$TMP_DIR/$MSC"; then
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
                            MSC_ACTUAL="$(sha256sum "$TMP_DIR/$MSC" | awk '{print $1}')"
                        elif command -v shasum >/dev/null 2>&1; then
                            MSC_ACTUAL="$(shasum -a 256 "$TMP_DIR/$MSC" | awk '{print $1}')"
                        else
                            MSC_ACTUAL=""
                        fi
                        if [ -n "$MSC_ACTUAL" ] && [ "$MSC_EXPECT" = "$MSC_ACTUAL" ]; then
                            chmod +x "$TMP_DIR/$MSC" 2>/dev/null || true
                            printf 'untrusted comment: minisign public key\n%s\n' "$MINISIGN_PUBKEY" > "$TMP_DIR/natpunch.pub"
                            if "$TMP_DIR/$MSC" "$TMP_DIR/natpunch.pub" "$TMP_DIR/SHA256SUMS.minisig" "$TMP_DIR/SHA256SUMS"; then
                                log "minisign 签名校验通过（内置静态校验器）"
                                sig_ok=1
                            else
                                die "minisign 签名校验失败（内置校验器）"
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
    else
        warn "发布未提供 SHA256SUMS.minisig，跳过签名校验（SHA256 已强制校验）"
    fi
fi
[ "$sig_ok" -eq 1 ] || warn "未找到签名校验工具，跳过签名校验（SHA256 已强制校验）"
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
# TLS_FLAG 必须整体加单引号：init.d 用 `. /etc/natpunch.conf` source 本文件，
# 值含空格时无引号会被拆成多条命令执行（修复：多参数 TLS_FLAG source 崩溃）
cat > "$CONF" <<EOF
SERVER=$SERVER
PORT=$PORT
VKEY=$VKEY
TLS_FLAG='$TLS_FLAG'
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
    # respawn <threshold> <timeout> <retry>
    # retry 必须是 0。procd 源码 service/instance.c 的判定是：
    #     if (respawn_count > respawn_retry && respawn_retry > 0) {
    #         respawn = 0; halt = 1;      // 彻底放手，不再拉起
    #     }
    # 写成 5 就是"一小时内崩 6 次就永久停止守护"。客户端明明会自己重连，
    # 却可能因为路由器 OOM 之类的连踹几次被守护放弃，只能重启设备才能恢复 ——
    # 这正是"无缘无故不启动"。retry=0 走的是 else 分支，永不放弃。
    procd_set_param respawn 3600 5 0
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
WATCHDOG_PID="/var/run/natpunch-client-watchdog.pid"
start() {
    [ -f /etc/natpunch.conf ] || exit 1
    # 这条分支只给"有 /etc/rc.common 但没有 procd"的极少数环境兜底。
    # 原来这里只写了一句 `cmd &` —— 进程一死就没人再管它，与 procd 分支
    # 的 respawn 行为完全不对等，正是"无缘无故不启动"。
    # 外面套一层循环看住：客户端退出就等 5 秒重来。
    # stop 必须按 pid 文件把循环本体也收掉，否则 killall 杀掉客户端之后
    # 循环会立刻把它再拉起来，stop 形同虚设。
    (
        while :; do
            . /etc/natpunch.conf
            /usr/bin/natpunch-client -server="${SERVER}:${PORT}" -vkey="${VKEY}" \
                -type=tcp ${TLS_FLAG:-} >>/tmp/natpunch-client.log 2>&1 &
            _np_pid=$!
            wait "$_np_pid"
            sleep 5
        done
    ) &
    echo $! > "$WATCHDOG_PID"
}
stop() {
    if [ -f "$WATCHDOG_PID" ]; then
        kill "$(cat "$WATCHDOG_PID" 2>/dev/null)" 2>/dev/null
        rm -f "$WATCHDOG_PID"
    fi
    killall natpunch-client 2>/dev/null
}
INIT
    fi
    chmod +x "$INIT"
    "$INIT" enable 2>/dev/null || true
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
# StartLimitIntervalSec=0：关掉启动频率限制，语义就是"永远重启"。
# 默认值（10s / burst 5）+ RestartSec=3 目前恰好不会触发（10 秒窗口内最多 4 次），
# 但那是靠 3 > 10/5 这个巧合撑着的：谁把 RestartSec 调到 2 秒以内，systemd 就会把
# 服务打成 failed 并永久停止重启。别赌默认值。
StartLimitIntervalSec=0
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
    # enable 的结果不能吞：这一次能起来（下面还会 restart），但下次重启设备
    # 就不会自启了，而用户看到的是"安装成功"。失败要在下面明确报出来。
    if ! systemctl enable natpunch-client >/dev/null 2>&1; then
        warn "systemctl enable 失败，客户端不会开机自启"
    fi
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
    # 自启到底注册上没有 —— 上面只验证了"进程此刻活着"，
    # 那次能起来不代表下次开机还能起来，两件事必须分开确认。
    AUTOSTART=0
    if [ "$IS_OPENWRT" -eq 1 ]; then
        for _f in /etc/rc.d/S*natpunch-client; do
            if [ -x "$_f" ]; then AUTOSTART=1; break; fi
        done
    elif command -v systemctl >/dev/null 2>&1; then
        systemctl is-enabled --quiet natpunch-client 2>/dev/null && AUTOSTART=1
    fi
    if [ "$AUTOSTART" -eq 1 ]; then
        echo "==> 安装成功 ✓ ${VER_OUT:-${VER:-最新版}}（已注册开机自启）"
        exit 0
    fi
    echo "==> 客户端已启动，但【开机自启未注册】✗" >&2
    echo "==> 现在能正常用，重启设备后不会自动拉起。请手动补上：" >&2
    if [ "$IS_OPENWRT" -eq 1 ]; then
        echo "      $INIT enable" >&2
    else
        echo "      systemctl enable natpunch-client" >&2
    fi
    exit 3
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
