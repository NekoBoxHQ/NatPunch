#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 仅操作客户端专属命名 natpunch-client，与服务端 natpunch 完全隔离
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
#
# 关键设计：后台执行用的 apply 脚本由本脚本内嵌生成（heredoc），
# 与主脚本同版本同源，绝不从网络拉取——杜绝 CDN 缓存旧版导致的
# "新版主脚本 + 旧版 apply" 混搭（曾导致 done 判定失效、卸载提示失真）。
set -u
REPO="NekoBoxHQ/NatPunch"
# 发布方 minisign 公钥，必须与 install.sh / install_server.sh / lib/install 内嵌值一致。
# 可用环境变量覆盖（自建发布链场景）。
NP_MINISIGN_PUBKEY="${MINISIGN_PUBKEY:-RWSD+MAfp/ZTI1gapgfvPeC1nkjQ3p52KovZQfxPjSO0f7DQX4FNe660}"

# 内置静态校验器（minisign-check）各架构 SHA256 信任锚，由发布方在打 tag 前填入。
# 必须内嵌于本脚本：若改用「同渠道下载的 SHA256SUMS」去校验校验器，在镜像被控时
# 攻击者可同时替换 包 / SHA256SUMS / 校验器 三者，构成循环信任，等于没有校验。
# 与 install.sh / install_server.sh 保持同一组值（release.yml 会逐个脚本比对）。
MSC_SHA256_amd64="e20c81421e5833c07a6c9c3d077650f7591effc63c1565b7086d9e133ea73576"
MSC_SHA256_arm64="152434e6f6d5aab0e8cafef602fc1a069b57b6d805d4954c5edb88550c6b9a9c"
MSC_SHA256_arm="517c7d12adeea6af6682b5957b81bb3f450cfdab1d7b7ade6abe7108e303ace1"
MSC_SHA256_mipsle="a4c2e73ac3a2810190882ecbf50d0a26accafe42843f48c73cb0709e83558865"
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
# 签名校验不可用时的统一收口。
# 这是往 root 目录装/换二进制：拿不到可信签名，就等于把「装什么」交给发布渠道和中间人。
# 因此默认中止；确需在无签名环境（自建发布链等）继续，显式设置 NATPUNCH_ALLOW_UNSIGNED=1。
sig_unavailable() {
    if [ "${NATPUNCH_ALLOW_UNSIGNED:-}" = "1" ]; then
        warn "$1 —— 已按 NATPUNCH_ALLOW_UNSIGNED=1 放行（SHA256 已强制校验，但不防发布渠道被控）"
        return 0
    fi
    die "$1。出于防篡改默认中止；确需在无签名环境下继续，请设置 NATPUNCH_ALLOW_UNSIGNED=1 后重试"
}
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
# pick_highest_version：从 GitHub releases JSON 里挑版本号最高的 tag。
#
# 两个坑都绕开了：
#   1) /releases/latest 是"按发布时间最新"，不是"版本号最高"。给旧分支补发一个
#      patch 之后，latest 会指向旧版 —— 升级反而降级。install.sh 早就为此改成
#      取最高版本了，这里必须对齐。
#   2) 不用 `sort -V`：busybox 的 sort 直到 1.32 才支持 -V，更老的 OpenWrt 上
#      会静默退化成字典序，于是 v26.9.9 排在 v26.9.111 后面。这里自己按点分段
#      做数值比较，任何 awk 都能跑。
pick_highest_version() {
    # 逐段做数值比较，不拼成一个大数字串去比 —— awk 的双精度只有 15~17 位
    # 有效数字，40 位的补零串一比就退化成"只看前几位"，v26.9.111 与 v26.9.120
    # 会被判相等、v26.9.9 反而排到 v26.9.111 前面（实测踩过）。
    #
    # 按 "{" 切块再逐个找 tag_name，而不是按行取第 4 个字段：
    # 那样只对"每个 tag 独占一行"的响应成立，碰上一行里塞多个对象的
    # （压缩过的 JSON、镜像改写过的响应）就会只看见第一个，静默选错版本。
    awk '
        function setbest(v, a, n,   j) {
            bestv = v; bn = n
            for (j = 1; j <= n; j++) b[j] = a[j]
        }
        function consider(v,   s, n, i, a, m, x, y) {
            s = v; sub(/^v/, "", s)
            n = split(s, a, ".")
            for (i = 1; i <= n; i++) if (a[i] !~ /^[0-9]+$/) return
            if (bestv == "") { setbest(v, a, n); return }
            m = (n > bn) ? n : bn
            for (i = 1; i <= m; i++) {
                x = (i <= n)  ? a[i] + 0 : 0
                y = (i <= bn) ? b[i] + 0 : 0
                if (x > y) { setbest(v, a, n); return }
                if (x < y) return
            }
        }
        {
            c = split($0, chunk, "{")
            for (k = 1; k <= c; k++) {
                if (chunk[k] !~ /"tag_name"[[:space:]]*:/) continue
                split(chunk[k], q, "\"")
                if (q[4] != "") consider(q[4])
            }
        }
        END { if (bestv != "") print bestv }
    '
}
# verify_package：下载后的完整性校验。install.sh 早就有这套（SHA256 强制 +
# minisign 分级），更新路径一直是裸的 —— 只做了个 tar 结构检查，等于把
# "从网上拿一个 root 二进制并立刻执行"这件事完全托付给 HTTPS。
# 这里补齐到与 install.sh 同级：SHA256 强制；有 minisign 就强制验签，
# 没有就告警放行（与 install.sh 的降级策略一致）。
verify_package() {
    _pkg="$1"; _dir="$2"; _base="$3"
    if ! fetch_to "$_base/SHA256SUMS" "$_dir/SHA256SUMS"; then
        warn "获取 SHA256SUMS 失败"
        return 1
    fi
    # SHA256SUMS 的第二列可能带一个前导 '*'（coreutils 的二进制模式标记，
    # Windows 上的 sha256sum 默认就带）。去掉再比，免得整份校验静默失效。
    _expect="$(awk -v f="$_pkg" '{ gsub(/^\*/, "", $2) } $2==f {print $1; exit}' "$_dir/SHA256SUMS")"
    [ -n "$_expect" ] || { warn "SHA256SUMS 中没有 $_pkg 条目"; return 1; }
    if command -v sha256sum >/dev/null 2>&1; then
        _actual="$(sha256sum "$_dir/pkg.tar.gz" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        _actual="$(shasum -a 256 "$_dir/pkg.tar.gz" | awk '{print $1}')"
    else
        warn "未找到 sha256sum/shasum，无法校验"
        return 1
    fi
    [ "$_expect" = "$_actual" ] || {
        warn "sha256 校验失败（期望 $_expect，实际 $_actual）"
        return 1
    }
    log "sha256 校验通过"
    _sig_ok=0
    if fetch_to "$_base/SHA256SUMS.minisig" "$_dir/SHA256SUMS.minisig"; then
        # 1) 系统 minisign（存在即强制校验，失败即中止）
        if command -v minisign >/dev/null 2>&1; then
            if minisign -Vm "$_dir/SHA256SUMS" -P "$NP_MINISIGN_PUBKEY" \
                -x "$_dir/SHA256SUMS.minisig" >/dev/null 2>&1; then
                log "minisign 签名校验通过"
                _sig_ok=1
            else
                warn "minisign 签名校验失败"
                return 1
            fi
        fi
        # 2) 内置静态校验器（OpenWrt 等没有 minisign 包的环境）：与包同源下载，
        #    但哈希必须与脚本内嵌信任锚一致才执行 —— 避免用同渠道的东西验自己。
        if [ "$_sig_ok" -eq 0 ]; then
            _msc_arch=""
            case "$(uname -m)" in
                x86_64|amd64) _msc_arch="amd64" ;;
                aarch64|arm64) _msc_arch="arm64" ;;
                armv7l|armv6l) _msc_arch="arm" ;;
                mips|mipsel|mipsle) _msc_arch="mipsle" ;;
            esac
            case "$_msc_arch" in
                amd64)  _msc_expect="$MSC_SHA256_amd64" ;;
                arm64)  _msc_expect="$MSC_SHA256_arm64" ;;
                arm)    _msc_expect="$MSC_SHA256_arm" ;;
                mipsle) _msc_expect="$MSC_SHA256_mipsle" ;;
                *)      _msc_expect="" ;;
            esac
            _msc_actual=""
            if [ -n "$_msc_arch" ] && [ -n "$_msc_expect" ] \
                && fetch_to "$_base/minisign-check-linux-$_msc_arch" "$_dir/msc"; then
                if command -v sha256sum >/dev/null 2>&1; then
                    _msc_actual="$(sha256sum "$_dir/msc" | awk '{print $1}')"
                elif command -v shasum >/dev/null 2>&1; then
                    _msc_actual="$(shasum -a 256 "$_dir/msc" | awk '{print $1}')"
                fi
                if [ -n "$_msc_actual" ] && [ "$_msc_expect" = "$_msc_actual" ]; then
                    chmod +x "$_dir/msc" 2>/dev/null || true
                    printf 'untrusted comment: minisign public key\n%s\n' "$NP_MINISIGN_PUBKEY" > "$_dir/natpunch.pub"
                    if "$_dir/msc" "$_dir/natpunch.pub" "$_dir/SHA256SUMS.minisig" "$_dir/SHA256SUMS" >/dev/null 2>&1; then
                        log "minisign 签名校验通过（内置静态校验器）"
                        _sig_ok=1
                    else
                        warn "minisign 签名校验失败（内置校验器）"
                        return 1
                    fi
                else
                    warn "内置校验器哈希与内嵌信任锚不符（疑似被篡改），已拒绝执行"
                    return 1
                fi
            fi
        fi
    fi

    if [ "$_sig_ok" -ne 1 ]; then
        sig_unavailable "签名校验不可用（无 minisign 且内置校验器不可得）" || return 1
    fi
    return 0
}
# ---------- 修改 systemd unit 的 KillMode ----------
# 使用 grep + echo 追加，避免 busybox sed 不支持 a 命令
ensure_killmode_process() {
    U="$1"
    [ -f "$U" ] || return 0
    # —— 顺带把 ExecStart 从 /bin/sh -c 包裹改回直接 exec 客户端本体 ——
    # 两件事必须配套：KillMode=process 只杀"主进程"，而 sh 包裹会让主进程变成那个
    # shell，真正的 natpunch-client 反而活下来（被 init 收养，PPID=1），下次启动
    # 就多一个同 vkey 的客户端、一起连服务端抢同一条隧道。真机上抓到过：
    # restart 之后一个 29 分钟前的旧进程和新进程同时连着。
    # 整行覆盖成已知的正确形态，不依赖旧行长什么样；失败只告警，不阻断升级。
    if grep -q '^ExecStart=/bin/sh -c ' "$U" 2>/dev/null; then
        BAK="/tmp/natpunch-unit-$(basename "$U").pre_directexec.bak"
        cp -a "$U" "$BAK" 2>/dev/null || true
        # 不要写成正则字面量 /^ExecStart=\/bin\/sh -c /、也不要在替换串里写 \/ ——
        # Debian 的 awk 是 **mawk**，它不把字符串里的 \/ 折成 /，会原样写进文件，
        # 结果是 ExecStart=\/usr\/bin\/... 这种坏单元（systemd 直接起不来）。
        # 用 -v 传新行 + 字符串匹配，全程不出现反斜杠，mawk / gawk / busybox awk 一致。
        awk -v new='ExecStart=/usr/bin/natpunch-client -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp ${TLS_FLAG}' \
            '$0 ~ "^ExecStart=/bin/sh -c " { print new; next } { print }' "$U" > "$U.tmp" \
            && mv "$U.tmp" "$U" || rm -f "$U.tmp"
        if ! grep -q '^ExecStart=/usr/bin/natpunch-client ' "$U" 2>/dev/null; then
            # 改写没成功就把备份放回去 —— 绝不能留下一个坏单元（那会让服务起不来），
            # 宁可维持现状（客户端会漏杀，但不影响启动与连接）。
            [ -f "$BAK" ] && cp -a "$BAK" "$U" 2>/dev/null
            echo "==> 警告: 改写 ExecStart 失败，已回滚单元原样（不影响启动，但 stop 仍会留下残留客户端）" >&2
        fi
    fi
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
    # 取版本号最高的 tag（不是 /releases/latest），拿不到就退回 latest 并告警
    VER_LIST=""
    if [ "$HAS_WGET" -eq 1 ]; then
        VER_LIST="$(wget -qO- --timeout=10 \
            "https://api.github.com/repos/$REPO/releases?per_page=20" 2>/dev/null)"
    else
        VER_LIST="$(curl -fsSL --max-time 10 \
            "https://api.github.com/repos/$REPO/releases?per_page=20" 2>/dev/null)"
    fi
    VER="$(printf '%s\n' "$VER_LIST" | pick_highest_version)"
    if [ -n "$VER" ]; then
        BASE_URL="https://github.com/$REPO/releases/download/$VER"
        log "目标版本: $VER"
    else
        BASE_URL="https://github.com/$REPO/releases/latest/download"
        warn "无法确定最高版本号，回退到 releases/latest（可能不是最新版）"
    fi
    # 已经是最新版本就别动。升级 = 停客户端 → 断隧道 → 再拉起来，
    # 版本没变还走一遍，等于白给自己制造一次失联窗口。
    if [ -n "$VER" ]; then
        CUR="$(tcmd 5 "$CLIENT_BIN_1" -version 2>/dev/null \
            | sed -n 's/^Version: *//p' | head -n1)"
        if [ -z "$CUR" ] && [ -f "$CLIENT_BIN_2" ]; then
            CUR="$(tcmd 5 "$CLIENT_BIN_2" -version 2>/dev/null \
                | sed -n 's/^Version: *//p' | head -n1)"
        fi
        if [ -n "$CUR" ] && [ "$CUR" = "$VER" ]; then
            log "当前已是最新版本 $VER，无需更新"
            exit 0
        fi
        [ -n "$CUR" ] && log "当前版本 $CUR → $VER"
    fi
    URL="$BASE_URL/$PKG"
    TMP_DIR="/tmp/natpunch_update.$$"
    rm -rf "$TMP_DIR"
    mkdir -p "$TMP_DIR" || die "无法创建临时目录"
    # 下载/解压阶段失败时自动清理临时目录；
    # 后台 apply 脚本就绪并脱离会话启动后，由 apply 脚本负责清理，这里再解除 trap
    trap 'rm -rf "$TMP_DIR" 2>/dev/null || true' EXIT INT TERM
    fetch_to "$URL" "$TMP_DIR/pkg.tar.gz" || die "下载失败: $URL"
    # 完整性校验：与 install.sh 同级（SHA256 强制 + minisign 分级）。
    # 放在这里（前台、可见）而不是后台 apply 里 —— 校验失败要当场报出来，
    # 不能让升级在后台悄悄中止。
    verify_package "$PKG" "$TMP_DIR" "$BASE_URL" \
        || die "包完整性校验失败，已中止升级（当前客户端未被动过）"
    tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || die "压缩包损坏"
    tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || die "解压失败"
    BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client 2>/dev/null | head -n1)"
    [ -n "${BIN_SRC:-}" ] || die "压缩包内未找到 natpunch-client 二进制"
    # —— 内嵌生成 update apply 脚本（唯一 PID 副本，与主脚本同版本，无外部拉取） ——
    APPLY="/tmp/natpunch_apply.$$"
    cat > "$APPLY" <<'APPLY'
#!/bin/sh
# NatPunch 客户端更新应用脚本（替换+重启）
# 由 uninstall_client.sh 更新流程内嵌生成后脱离会话后台执行。
# 环境变量（由调用方注入）：
#   NP_TMP_DIR / NP_BIN_SRC / NP_CLIENT_BIN_1 / NP_CLIENT_BIN_2
#   NP_CLIENT_INIT / NP_CLIENT_SYSTEMD_1 / NP_CLIENT_SYSTEMD_2
set -u
log()  { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
TMP_DIR="${NP_TMP_DIR:-}"
BIN_SRC="${NP_BIN_SRC:-}"
CLIENT_BIN_1="${NP_CLIENT_BIN_1:-/usr/bin/natpunch-client}"
CLIENT_BIN_2="${NP_CLIENT_BIN_2:-/usr/local/bin/natpunch-client}"
CLIENT_INIT="${NP_CLIENT_INIT:-/etc/init.d/natpunch-client}"
CLIENT_SYSTEMD_1="${NP_CLIENT_SYSTEMD_1:-/etc/systemd/system/natpunch-client.service}"
CLIENT_SYSTEMD_2="${NP_CLIENT_SYSTEMD_2:-/lib/systemd/system/natpunch-client.service}"
# 任何退出路径都清理临时目录（正常路径已显式清理，此处兜底异常路径）
trap 'rm -rf "$TMP_DIR" 2>/dev/null || true' EXIT INT TERM
[ -n "$BIN_SRC" ] && [ -f "$BIN_SRC" ] || { warn "NP_BIN_SRC 无效: $BIN_SRC"; exit 1; }
[ -n "$TMP_DIR" ] || { warn "NP_TMP_DIR 为空"; exit 1; }
log "开始应用更新"
log "源文件: $BIN_SRC"
log "目标:   $CLIENT_BIN_1"
# ---------- 工具 ----------
tcmd() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "$@"
    else
        N="$1"; shift; "$@"
    fi
}
ensure_killmode_process() {
    U="$1"
    [ -f "$U" ] || return 0
    # —— 顺带把 ExecStart 从 /bin/sh -c 包裹改回直接 exec 客户端本体 ——
    # 两件事必须配套：KillMode=process 只杀"主进程"，而 sh 包裹会让主进程变成那个
    # shell，真正的 natpunch-client 反而活下来（被 init 收养，PPID=1），下次启动
    # 就多一个同 vkey 的客户端、一起连服务端抢同一条隧道。真机上抓到过：
    # restart 之后一个 29 分钟前的旧进程和新进程同时连着。
    # 整行覆盖成已知的正确形态，不依赖旧行长什么样；失败只告警，不阻断升级。
    if grep -q '^ExecStart=/bin/sh -c ' "$U" 2>/dev/null; then
        BAK="/tmp/natpunch-unit-$(basename "$U").pre_directexec.bak"
        cp -a "$U" "$BAK" 2>/dev/null || true
        # 不要写成正则字面量 /^ExecStart=\/bin\/sh -c /、也不要在替换串里写 \/ ——
        # Debian 的 awk 是 **mawk**，它不把字符串里的 \/ 折成 /，会原样写进文件，
        # 结果是 ExecStart=\/usr\/bin\/... 这种坏单元（systemd 直接起不来）。
        # 用 -v 传新行 + 字符串匹配，全程不出现反斜杠，mawk / gawk / busybox awk 一致。
        awk -v new='ExecStart=/usr/bin/natpunch-client -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp ${TLS_FLAG}' \
            '$0 ~ "^ExecStart=/bin/sh -c " { print new; next } { print }' "$U" > "$U.tmp" \
            && mv "$U.tmp" "$U" || rm -f "$U.tmp"
        if ! grep -q '^ExecStart=/usr/bin/natpunch-client ' "$U" 2>/dev/null; then
            # 改写没成功就把备份放回去 —— 绝不能留下一个坏单元（那会让服务起不来），
            # 宁可维持现状（客户端会漏杀，但不影响启动与连接）。
            [ -f "$BAK" ] && cp -a "$BAK" "$U" 2>/dev/null
            echo "==> 警告: 改写 ExecStart 失败，已回滚单元原样（不影响启动，但 stop 仍会留下残留客户端）" >&2
        fi
    fi
    if grep -q '^KillMode=' "$U" 2>/dev/null; then
        awk 'BEGIN{FS=OFS="="} /^KillMode=/{print "KillMode","process"; next} {print}' "$U" > "$U.tmp" \
            && mv "$U.tmp" "$U" || rm -f "$U.tmp"
    else
        if grep -q '^RestartSec=' "$U" 2>/dev/null; then
            awk '/^RestartSec=/{print; print "KillMode=process"; next} {print}' "$U" > "$U.tmp" \
                && mv "$U.tmp" "$U" || rm -f "$U.tmp"
        else
            echo "KillMode=process" >> "$U"
        fi
    fi
}
client_running() {
    SELF_PID=$$
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$SELF_PID" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)") return 0 ;;
            esac
        done
        return 1
    fi
    ps w 2>/dev/null | grep -v grep | grep -q 'natpunch-client'
}
kill_client_pids() {
    SIGNAL="$1"
    SELF_PID=$$
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$SELF_PID" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)")
                    kill $SIGNAL "$p" 2>/dev/null || true
                    ;;
            esac
        done
    else
        ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}' | while read -r p; do
            [ "$p" = "$SELF_PID" ] && continue
            kill $SIGNAL "$p" 2>/dev/null || true
        done
    fi
}
# ---------- 1. 替换二进制（先备份） ----------
[ -f "$CLIENT_BIN_1" ] && cp -f "$CLIENT_BIN_1" "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
if ! cp -f "$BIN_SRC" "$CLIENT_BIN_1"; then
    warn "写入 $CLIENT_BIN_1 失败，尝试回滚"
    [ -f "${CLIENT_BIN_1}.update_bak" ] && cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_1"
    rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
    exit 1
fi
chmod 755 "$CLIENT_BIN_1"
if [ -f "$CLIENT_BIN_2" ]; then
    cp -f "$BIN_SRC" "$CLIENT_BIN_2" && chmod 755 "$CLIENT_BIN_2" || true
fi
# ---------- 2. 修正 KillMode + daemon-reload（超时保护） ----------
if command -v systemctl >/dev/null 2>&1; then
    ensure_killmode_process "$CLIENT_SYSTEMD_1"
    ensure_killmode_process "$CLIENT_SYSTEMD_2"
    tcmd 10 systemctl daemon-reload >/dev/null 2>&1 || true
fi
# ---------- 3. systemd-run 独立单元只做 stop（避开 cgroup 连带杀） ----------
if command -v systemctl >/dev/null 2>&1 && command -v systemd-run >/dev/null 2>&1; then
    if systemd-run --unit="natpunch-apply-stop-$$" --collect --wait --quiet \
            /bin/sh -c "systemctl stop natpunch-client" >/dev/null 2>&1; then
        log "已通过独立单元停止客户端"
        sleep 1
        # 兜底清扫：stop 时若单元还是旧的 /bin/sh -c 包裹，KillMode=process 只杀那个
        # shell，真正的客户端活下来（被 init 收养，PPID=1）；start 之后就成了两个同
        # vkey 的客户端。存量机器第一次走新流程时就会碰到这种残留。
        # 复用 kill_client_pids —— 它按 /proc/<pid>/exe 匹配，精确到客户端本体，
        # 碰不到面板 SSH 的 shell，也碰不到 apply 脚本自己。
        kill_client_pids TERM
        sleep 1
        kill_client_pids KILL
        sleep 1
        # stop 后 apply 脚本已脱离客户端 cgroup，可以安全 start
        if tcmd 15 systemctl start natpunch-client >/dev/null 2>&1; then
            sleep 2
            if client_running; then
                log "更新完成（独立单元停止 + 常规启动）"
                rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
                rm -rf "$TMP_DIR"
                exit 0
            fi
            warn "systemctl start 成功但进程未运行，进入常规流程"
        else
            warn "systemctl start 失败，进入常规流程"
        fi
    else
        warn "systemd-run stop 失败，回退常规流程"
    fi
fi
# ---------- 4. 常规流程（OpenWrt init.d / 无 systemd-run） ----------
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
    if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
        tcmd 15 systemctl stop natpunch-client 2>/dev/null || true
    fi
fi
# 兜底杀进程
kill_client_pids TERM
sleep 1
kill_client_pids KILL
sleep 1
# ---------- 5. 启动客户端 ----------
start_client() {
    if [ -f /etc/openwrt_release ]; then
        if [ -f "$CLIENT_INIT" ]; then
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
            "$CLIENT_INIT" start 2>/dev/null || true
            sleep 1
            client_running && return 0
        fi
    elif command -v systemctl >/dev/null 2>&1; then
        tcmd 15 systemctl restart natpunch-client >/dev/null 2>&1 || true
        sleep 2
        client_running && return 0
        tcmd 15 systemctl start natpunch-client >/dev/null 2>&1 || true
        sleep 2
        client_running && return 0
    fi
    # 兜底：直接后台运行二进制
    if [ -f /etc/natpunch.conf ]; then
        SRV=""; PRT=""; VKY=""; TLS=""
        # 安全解析配置：不 source，只 grep 取值，避免配置内容被 shell 解释
        get_conf() {
            grep -E "^[[:space:]]*$1[[:space:]]*=" /etc/natpunch.conf 2>/dev/null \
                | head -n1 | sed "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*//"
        }
        SRV="$(get_conf SERVER)"; [ -n "$SRV" ] || SRV="$(get_conf server)"
        PRT="$(get_conf PORT)";   [ -n "$PRT" ] || PRT="$(get_conf port)"
        VKY="$(get_conf VKEY)";   [ -n "$VKY" ] || VKY="$(get_conf vkey)"
        TLS="$(get_conf TLS_FLAG)"
        [ -z "$PRT" ] && PRT=8024
        [ -z "$SRV" ] || [ -z "$VKY" ] && { warn "配置缺少 SERVER 或 VKEY"; return 1; }
        # install.sh 写配置时是 TLS_FLAG='...'，get_conf 取回来仍带着那对单引号。
        # 不剥掉的话下面按空格切词时，最后一个参数的尾巴上会挂一个引号
        # （实测：-tls_fingerprint=AB:CD 变成 -tls_fingerprint=AB:CD'），
        # 而且裸写成 TLS_FLAG='true' 时 case 的 true 分支根本匹配不上。
        TLS="$(printf '%s' "$TLS" | tr -d "'\"")"
        # 参数化启动，不用 eval：SERVER/VKEY 仅作为参数传给二进制，
        # 不会被 shell 解释，杜绝配置文件内容注入命令
        # 重建启动参数。原来只还原了 -tls_enable=true，把 -tls_fingerprint 丢了：
        # 配了指纹固定（F2-2）的场景走兜底路径时指纹不生效，开了 tls_strict
        # 还会直接拒绝启动。这里从 TLS_FLAG 里把两个都抠出来。
        TLS_ARG=""
        case "$TLS" in
            *tls_enable=true*|*tls=true*|true|1)
                TLS_ARG="-tls_enable=true"
                FP="$(printf '%s\n' "$TLS" | awk '{for(i=1;i<=NF;i++) if ($i ~ /^-tls_fingerprint=/) {sub(/^-tls_fingerprint=/,"",$i); print $i; exit}}')"
                [ -n "$FP" ] && TLS_ARG="$TLS_ARG -tls_fingerprint=$FP"
                ;;
        esac
        # shellcheck disable=SC2086（TLS_ARG 为空或固定白名单值）
        nohup "$CLIENT_BIN_1" -server="$SRV:$PRT" -vkey="$VKY" -type=tcp $TLS_ARG >> /tmp/natpunch-client.log 2>&1 &
        sleep 2
        client_running && return 0
    fi
    return 1
}
log "替换完成，启动客户端..."
UPDATE_OK=0
if ! start_client; then
    if [ -f "${CLIENT_BIN_1}.update_bak" ]; then
        warn "新版本启动失败，自动回滚旧版本"
        cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_1"
        chmod 755 "$CLIENT_BIN_1"
        [ -f "$CLIENT_BIN_2" ] && { cp -f "${CLIENT_BIN_1}.update_bak" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
        if start_client; then
            log "更新完成（回滚后启动）"
            UPDATE_OK=1
            rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
        else
            warn "回滚后仍无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
        fi
    else
        warn "客户端无法启动，请检查 $CLIENT_BIN_1 与 /etc/natpunch.conf"
    fi
else
    log "更新完成"
    UPDATE_OK=1
    rm -f "${CLIENT_BIN_1}.update_bak" 2>/dev/null || true
fi
rm -rf "$TMP_DIR"
if [ "$UPDATE_OK" -eq 0 ]; then
    # 升级失败了就明说。原来无论成败一律 exit 0，外面（面板 / 脚本调用方）
    # 看不出区别。另外提醒一句：服务是 enabled 状态没被碰过，
    # 重启设备仍然能恢复，不必现场处理。
    warn "升级失败：客户端当前处于停止状态。服务仍是 enabled，重启设备即可恢复。"
    if [ -f "${CLIENT_BIN_1}.update_bak" ]; then
        warn "旧版本已保留在 ${CLIENT_BIN_1}.update_bak，可手动拷回。"
    fi
    warn "日志见 ${NP_UPDATE_LOG:-/tmp/natpunch_update.log}"
    exit 4
fi
# 自删唯一副本
(
    sleep 1
    rm -f "$0"
) &
exit 0
APPLY
    chmod 755 "$APPLY"
    # —— KillMode=process：apply 脚本就绪后再改，失败也不影响 ——
    if command -v systemctl >/dev/null 2>&1; then
        ensure_killmode_process "$CLIENT_SYSTEMD_1"
        ensure_killmode_process "$CLIENT_SYSTEMD_2"
        tcmd 10 systemctl daemon-reload >/dev/null 2>&1 || true
    fi
    # —— 脱离手段探测 ——
    # 升级要在"自己的 SSH 通道被切断"的前提下跑完，所以 apply 脚本必须活过
    # 客户端被 stop 的那一刻。setsid 只换 session / 进程组，**不换 cgroup**：
    # 在 systemd 上真正保险的是把 apply 放进独立单元（独立 cgroup），
    # 这样 systemctl stop natpunch-client 怎么都杀不到它，不再只依赖 KillMode
    # 有没有写成功。两者都没有时宁可拒绝，也不要赌。
    HAS_SETSID=0
    command -v setsid >/dev/null 2>&1 && HAS_SETSID=1
    HAS_SYSTEMD_RUN=0
    # 判据用 /run/systemd/system 是否存在（systemd 真的是 1 号进程），
    # 不要用 `systemctl is-system-running` —— 它在 degraded 状态下返回非零，
    # 会把一台跑得好好的机器误判成"没有 systemd"，进而误判成"没有脱离手段"。
    if [ -d /run/systemd/system ] && command -v systemd-run >/dev/null 2>&1; then
        HAS_SYSTEMD_RUN=1
    fi
    if [ "$HAS_SYSTEMD_RUN" -eq 0 ] && [ "$HAS_SETSID" -eq 0 ]; then
        die "缺少 setsid / systemd-run，无法让升级脚本脱离当前会话。
    你的 SSH 很可能正是通过这个客户端的隧道进来的：原地替换二进制会把连接
    一起切断，而没有脱离手段时没人接手，升级可能停在半路（客户端停着）。
    脚本拒绝在缺脱离手段的情况下继续，请先补上 setsid 或改在带外通道升级。"
    fi
    # —— 看门狗：独立于 apply、独立于客户端 cgroup ——
    # 万一 apply 自己也被连带杀掉（KillMode 没写进去、procd 按进程组清场…），
    # 那段时间客户端就是停着的，没人拉起来 = 彻底失联。先挂一个
    # "过一会儿还不 alive 就把它拉起来"的独立任务，作为最后一道保险。
    # 幂等：客户端在跑就什么都不做，重复触发也无害。
    GUARD="/tmp/natpunch_guard.$$"
    cat > "$GUARD" <<'GUARD'
#!/bin/sh
# 由 uninstall_client.sh 生成：升级看门狗。
# 等一段时间后确认客户端是否还在跑，不在就按当前环境的服务管理器拉起。
set -u
DELAY="${NP_GUARD_DELAY:-90}"
CLIENT_INIT="${NP_CLIENT_INIT:-/etc/init.d/natpunch-client}"
LOG="${NP_UPDATE_LOG:-/tmp/natpunch_update.log}"
log() { echo "$(date '+%F %T') [guard] $*" >> "$LOG" 2>/dev/null; }
client_running() {
    SELF_PID=$$
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$SELF_PID" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$(basename "$exe" 2>/dev/null)" in
                "natpunch-client"|"natpunch-client (deleted)") return 0 ;;
            esac
        done
        return 1
    fi
    ps w 2>/dev/null | grep -v grep | grep -q 'natpunch-client'
}
sleep "$DELAY"
if client_running; then
    log "客户端在运行，看门狗不介入"
    rm -f "$0" 2>/dev/null || true
    exit 0
fi
log "等待 ${DELAY}s 后客户端仍不在，尝试拉起"
if [ -x "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" start >>"$LOG" 2>&1 || true
elif command -v systemctl >/dev/null 2>&1; then
    systemctl start natpunch-client >>"$LOG" 2>&1 || true
fi
sleep 3
if client_running; then
    log "看门狗已把客户端拉回"
else
    log "看门狗拉起失败，需要人工介入"
fi
# 自删：原先没有任何地方删它，每升级一次就在 /tmp 留一份 natpunch_guard.<pid>，只增不减。
# 删的是自己（$0 就是 /tmp/natpunch_guard.$$），与 apply 那条链路无关；
# 放最后 + || true，删不掉也不影响看门狗已经做完的事。
rm -f "$0" 2>/dev/null || true
GUARD
    chmod 755 "$GUARD"
    echo "==> 升级文件已就绪，流程转入后台执行"
    echo "==> SSH 断开后自动完成替换与重启，日志: $UPDATE_LOG"
    echo "==> 完成后客户端自动重启，隧道恢复后请重新连接"
    # 先挂看门狗（独立进程），再起 apply —— 顺序不能反，
    # 否则 apply 万一瞬间被杀就没有保险了。
    if [ "$HAS_SYSTEMD_RUN" -eq 1 ]; then
        systemd-run --unit="natpunch-guard-$$" --collect \
            --setenv=NP_CLIENT_INIT="$CLIENT_INIT" \
            --setenv=NP_UPDATE_LOG="$UPDATE_LOG" \
            /bin/sh "$GUARD" >/dev/null 2>&1 \
            || warn "看门狗启动失败（systemd-run），升级仍会继续"
    else
        NP_CLIENT_INIT="$CLIENT_INIT" NP_UPDATE_LOG="$UPDATE_LOG" \
            setsid sh "$GUARD" >/dev/null 2>&1 < /dev/null &
    fi
    # —— 启动 apply ——
    if [ "$HAS_SYSTEMD_RUN" -eq 1 ]; then
        # 独立单元 = 独立 cgroup：客户端怎么被 stop 都杀不到它
        systemd-run --unit="natpunch-apply-$$" --collect \
            --property=StandardOutput="append:$UPDATE_LOG" \
            --property=StandardError="append:$UPDATE_LOG" \
            --setenv=NP_TMP_DIR="$TMP_DIR" --setenv=NP_BIN_SRC="$BIN_SRC" \
            --setenv=NP_CLIENT_BIN_1="$CLIENT_BIN_1" \
            --setenv=NP_CLIENT_BIN_2="$CLIENT_BIN_2" \
            --setenv=NP_CLIENT_INIT="$CLIENT_INIT" \
            --setenv=NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" \
            --setenv=NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" \
            --setenv=NP_UPDATE_LOG="$UPDATE_LOG" \
            /bin/sh "$APPLY" >/dev/null 2>&1 \
            || die "systemd-run 启动升级单元失败，已中止（当前客户端未被动过）"
    else
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" \
        NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" \
        NP_CLIENT_INIT="$CLIENT_INIT" \
        NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" \
        setsid sh "$APPLY" > "$UPDATE_LOG" 2>&1 < /dev/null &
    fi
    # 清空 trap，避免 EXIT 删掉 $TMP_DIR（由 apply 脚本清理）
    trap - EXIT INT TERM
    exit 0
}
# ---------- 卸载模式 ----------
do_uninstall() {
    log "卸载 NatPunch 客户端..."
    rm -f "$DONE_FILE"
    # —— 内嵌生成 uninstall apply 脚本（唯一 PID 副本，与主脚本同版本，无外部拉取） ——
    APPLY="/tmp/natpunch_uninstall.$$"
    cat > "$APPLY" <<'APPLY'
#!/bin/sh
# NatPunch 客户端卸载应用脚本（静默完整卸载）
# 由 uninstall_client.sh 卸载模式内嵌生成后在后台执行（setsid/nohup 脱离当前会话）：
# 面板 SSH 会话由客户端隧道承载，杀客户端即断连；后台执行确保清理完整、无残留。
#
# 契约（与 uninstall_client.sh 主脚本约定）：
#   1. 完成后必须 touch "$NP_DONE_FILE"（默认 /tmp/natpunch_uninstall.done）
#   2. 所有输出写 "$LOG"（/tmp/natpunch_uninstall.log），stdout 保持干净
#   3. 不读 stdin
set -u
CLIENT_BIN_1="/usr/bin/natpunch-client"
CLIENT_BIN_2="/usr/local/bin/natpunch-client"
CLIENT_CONF_1="/etc/natpunch.conf"
CLIENT_CONF_2="/etc/natpunch/natpunch.conf"
CLIENT_CONF_3="/usr/local/etc/natpunch.conf"
CLIENT_INIT="/etc/init.d/natpunch-client"
CLIENT_SYSTEMD_1="/etc/systemd/system/natpunch-client.service"
CLIENT_SYSTEMD_2="/lib/systemd/system/natpunch-client.service"
CLIENT_PLIST="/Library/LaunchDaemons/com.natpunch.client.plist"
CLIENT_AGENT="${HOME:-}/Library/LaunchAgents/com.natpunch.client.plist"
LOG="${NP_LOG:-/tmp/natpunch_uninstall.log}"
DONE_FILE="${NP_DONE_FILE:-/tmp/natpunch_uninstall.done}"
log() { echo "$(date '+%F %T') $*" >> "$LOG"; }
# 退出时一定写 done 文件，保证主脚本不超时
finish() {
    touch "$DONE_FILE" 2>/dev/null || true
}
trap 'finish' EXIT INT TERM
# ---------- 客户端进程精确识别 ----------
get_client_pids() {
    PIDS=""
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            base=$(basename "$exe" 2>/dev/null)
            # 处理 "(deleted)" 后缀
            case "$base" in
                "natpunch-client"|"natpunch-client (deleted)") PIDS="$PIDS $p" ;;
            esac
        done
    else
        PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch-client' | awk '{print $1}')
    fi
    echo "$PIDS"
}
kill_client_pids() {
    PIDS=$(get_client_pids)
    [ -z "${PIDS:-}" ] && return 0
    for p in $PIDS; do kill "$p" 2>/dev/null || true; done
    sleep 1
    for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true; done
    sleep 1
}
# ================= 静默完整卸载 =================
log "开始卸载 natpunch-client"
# 1. 精确结束客户端进程
kill_client_pids
# 2. OpenWrt init.d
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
fi
# 只删精确匹配的 rc.d 软链，不用 *natpunch-client 通配
for f in /etc/rc.d/S??natpunch-client /etc/rc.d/K??natpunch-client \
         /etc/rc*.d/S??natpunch-client /etc/rc*.d/K??natpunch-client; do
    [ -e "$f" ] || continue
    rm -f "$f"
done
# 3. systemd
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        systemctl stop natpunch-client 2>/dev/null || true
        systemctl disable natpunch-client 2>/dev/null || true
        rm -f "$U"
    done
    systemctl daemon-reload 2>/dev/null || true
fi
# 4. FreeBSD rc.d
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then
    if [ -f /usr/local/etc/rc.d/natpunch-client ]; then
        service natpunch-client stop 2>/dev/null || true
        rm -f /usr/local/etc/rc.d/natpunch-client
        command -v sysrc >/dev/null 2>&1 && sysrc -x natpunch_client_enable 2>/dev/null || true
    fi
fi
# 5. macOS launchd
if [ -d /Library/LaunchDaemons ] && [ -f "$CLIENT_PLIST" ]; then
    launchctl bootout system "$CLIENT_PLIST" 2>/dev/null || true
    launchctl unload "$CLIENT_PLIST" 2>/dev/null || true
    rm -f "$CLIENT_PLIST"
fi
if [ -f "$CLIENT_AGENT" ]; then
    launchctl unload "$CLIENT_AGENT" 2>/dev/null || true
    rm -f "$CLIENT_AGENT"
fi
# 6. 清理 rc.local（仅 natpunch-client 行，备份不覆盖）
if [ -f /etc/rc.local ]; then
    BAK="/etc/rc.local.natpunch-client.bak"
    [ -f "$BAK" ] || cp -f /etc/rc.local "$BAK" 2>/dev/null || true
    if sed --version >/dev/null 2>&1; then
        sed -i '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    else
        sed -i '' '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    fi
fi
# 7. 删除客户端配置
rm -f "$CLIENT_CONF_1" "$CLIENT_CONF_2" "$CLIENT_CONF_3"
# 仅当目录内无服务端残留时才删目录
if [ -d /etc/natpunch ]; then
    if [ ! -f /etc/natpunch/natpunch ] && [ ! -f /etc/natpunch/conf/natpunch.conf ]; then
        rm -rf /etc/natpunch
    fi
fi
if [ -d /usr/local/etc/natpunch ]; then
    if [ ! -f /usr/local/etc/natpunch/natpunch ]; then
        rm -rf /usr/local/etc/natpunch
    fi
fi
# 8. 删除客户端二进制
rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
# 9. 清理客户端专属日志与临时文件
rm -f /tmp/natpunch-client.log /var/log/natpunch-client.log \
      /tmp/natpunch_update.log /tmp/natpunch_apply.sh 2>/dev/null || true
# 注意：不删 /tmp/natpunch_update.* 目录，可能正在被更新流程使用；
# 更新流程自身会清理自己的 TMP_DIR。
# 10. 复查残留（仅写日志）
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        base=$(basename "$exe" 2>/dev/null)
        case "$base" in
            "natpunch-client"|"natpunch-client (deleted)")
                REMAIN=1
                log "残留 PID $p: $(tr '\0' ' ' < "$d/cmdline" 2>/dev/null)"
                ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    log "仍检测到客户端进程，请手动检查"
else
    log "卸载完成，无残留"
fi
# 11. 自删（延迟到 shell 退出后；只删当前运行的唯一副本 $0，
#      不碰固定路径，避免误删下次新生成的 apply 脚本）
(
    sleep 1
    rm -f "$0"
) &
exit 0
APPLY
    chmod 755 "$APPLY"
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
        setsid sh "$APPLY" >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
    else
        NP_DONE_FILE="$DONE_FILE" \
        nohup sh "$APPLY" >> "$UNINSTALL_LOG" 2>&1 < /dev/null &
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
