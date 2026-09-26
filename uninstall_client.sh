#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 客户端命名 natpunch-client，与服务端 natpunch 完全隔离（进程名 / 自启名 / 二进制名均不同），
# 卸载与更新仅按客户端专属名称操作，同机部署服务端时互不影响。
# 兼容清理旧版命名（natpunch）的客户端残留。
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
set -u
REPO="NekoBoxHQ/NatPunch"
ACTION="${1:-uninstall}"
SERVER_DIR="/opt/natpunch"
SERVER_BIN="$SERVER_DIR/natpunch"
SERVER_CONF="$SERVER_DIR/conf/natpunch.conf"
SERVER_PID_FILE="$SERVER_DIR/natpunch.pid"
CLIENT_BIN_1="/usr/bin/natpunch-client"
CLIENT_BIN_2="/usr/local/bin/natpunch-client"
CLIENT_CONF_1="/etc/natpunch.conf"
CLIENT_CONF_2="/etc/natpunch/natpunch.conf"
CLIENT_CONF_3="/usr/local/etc/natpunch.conf"
CLIENT_INIT="/etc/init.d/natpunch-client"
CLIENT_SYSTEMD_1="/etc/systemd/system/natpunch-client.service"
CLIENT_SYSTEMD_2="/lib/systemd/system/natpunch-client.service"
# 旧版命名（迁移兼容，内容归属判定后清理）
LEGACY_BIN_1="/usr/bin/natpunch"
LEGACY_BIN_2="/usr/local/bin/natpunch"
LEGACY_INIT="/etc/init.d/natpunch"
LEGACY_UNIT_1="/etc/systemd/system/natpunch.service"
LEGACY_UNIT_2="/lib/systemd/system/natpunch.service"
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }

# ---------- 旧命名自启文件归属判定（区分旧客户端 / 服务端） ----------
# 返回: 0=客户端  1=服务端  2=不存在/无法判定
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

# ---------- 同机服务端检测 ----------
# 服务端特征：/opt/natpunch 二进制/配置/进程、旧命名自启内容判定为服务端、
# /usr/bin/natpunch 被无 -vkey= 的进程占用（早期服务端部署）。
SERVER_PRESENT=0
[ -f "$SERVER_BIN" ] && SERVER_PRESENT=1
[ -f "$SERVER_CONF" ] && SERVER_PRESENT=1
init_owner "$LEGACY_INIT"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
unit_owner "$LEGACY_UNIT_1"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
unit_owner "$LEGACY_UNIT_2"; [ "$?" -eq 1 ] && SERVER_PRESENT=1
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            /opt/natpunch/natpunch)
                SERVER_PRESENT=1
                break
                ;;
            /usr/bin/natpunch|/usr/local/bin/natpunch)
                cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
                case "$cmd" in
                    *" -vkey="*) ;;
                    *) SERVER_PRESENT=1; break ;;
                esac
                ;;
        esac
    done
fi

# ---------- 客户端进程精确识别 ----------
# 新客户端（natpunch-client）：exe 名专属，直接匹配。
# 旧客户端（natpunch）：exe 名为 natpunch 且命令行含 -vkey=（无 -vkey 的是服务端，排除）。
get_client_pids() {
    PIDS=""
    if [ -d /proc ]; then
        for d in /proc/[0-9]*; do
            p=${d#/proc/}
            [ "$p" = "$$" ] && continue
            exe=$(readlink "$d/exe" 2>/dev/null) || continue
            case "$exe" in
                */natpunch-client|*/natpunch-client\ \(deleted\))
                    PIDS="$PIDS $p"
                    ;;
                */natpunch|*/natpunch\ \(deleted\))
                    cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
                    case "$cmd" in
                        *" -vkey="*) PIDS="$PIDS $p" ;;
                    esac
                    ;;
            esac
        done
    else
        PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch' | grep ' -vkey=' | awk '{print $1}')
    fi
    # 排除服务端 pid 文件记录的进程，双保险
    if [ -f "$SERVER_PID_FILE" ]; then
        SPID=$(cat "$SERVER_PID_FILE" 2>/dev/null)
        case "$SPID" in
            ''|*[!0-9]*) ;;
            *)
                NEW=""
                for p in $PIDS; do
                    [ "$p" = "$SPID" ] && continue
                    NEW="$NEW $p"
                done
                PIDS="$NEW"
                ;;
        esac
    fi
    echo "$PIDS"
}
kill_client_pids() {
    PIDS=$(get_client_pids)
    [ -z "$PIDS" ] && return 0
    for p in $PIDS; do kill "$p" 2>/dev/null || true; done
    sleep 1
    for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null || true; done
    sleep 1
}

# ---------- 清理旧命名客户端自启（内容判定为客户端版才删，服务端不动） ----------
clean_legacy_autostart() {
    IO=2
    if [ -f "$LEGACY_INIT" ]; then
        init_owner "$LEGACY_INIT"; IO=$?
    fi
    if [ "$IO" = "0" ]; then
        "$LEGACY_INIT" disable 2>/dev/null || true
        rm -f "$LEGACY_INIT" /etc/rc.d/*natpunch 2>/dev/null
    elif [ "$IO" = "1" ]; then
        warn "$LEGACY_INIT 属于服务端，跳过删除"
    fi
    if command -v systemctl >/dev/null 2>&1; then
        for U in "$LEGACY_UNIT_1" "$LEGACY_UNIT_2"; do
            [ -f "$U" ] || continue
            unit_owner "$U"; UO=$?
            if [ "$UO" = "0" ]; then
                systemctl disable natpunch 2>/dev/null || true
                rm -f "$U"
            elif [ "$UO" = "1" ]; then
                warn "$U 属于服务端，跳过删除"
            fi
        done
        systemctl daemon-reload 2>/dev/null || true
    fi
}

# ---------- 更新模式：保留配置，仅替换二进制并重启 ----------
if [ "$ACTION" = "update" ]; then
    log "更新 NatPunch 客户端（保留配置）..."
    if [ ! -f "$CLIENT_BIN_1" ] && [ ! -f "$CLIENT_BIN_2" ] && [ ! -f "$LEGACY_BIN_1" ] && [ ! -f "$LEGACY_BIN_2" ]; then
        warn "未检测到已安装客户端，请使用 install.sh 全新安装"
        exit 1
    fi
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   PKG="linux_amd64_client.tar.gz" ;;
        aarch64|arm64)  PKG="linux_arm64_client.tar.gz" ;;
        *) warn "不支持架构: $ARCH（当前仅支持 x86_64 / arm64）"; exit 1 ;;
    esac
    VER=""
    if command -v wget >/dev/null 2>&1; then
        VER="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    elif command -v curl >/dev/null 2>&1; then
        VER="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
    fi
    log "最新版本: ${VER:-最新发布}"
    # 下载走 releases/latest/download，自动指向最新发布，不依赖写死的版本号
    URL="https://github.com/$REPO/releases/latest/download/$PKG"
    TMP_DIR="/tmp/natpunch_update.$$"
    mkdir -p "$TMP_DIR" || { warn "无法创建临时目录"; exit 1; }
    trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
    if command -v wget >/dev/null 2>&1; then
        wget -q -O "$TMP_DIR/pkg.tar.gz" "$URL" || { warn "下载失败: $URL"; exit 1; }
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$TMP_DIR/pkg.tar.gz" "$URL" || { warn "下载失败: $URL"; exit 1; }
    else
        warn "未找到 wget / curl"; exit 1
    fi
    [ -s "$TMP_DIR/pkg.tar.gz" ] || { warn "下载文件为空"; exit 1; }
    tar -tzf "$TMP_DIR/pkg.tar.gz" >/dev/null 2>&1 || { warn "压缩包损坏"; exit 1; }
    tar -zxf "$TMP_DIR/pkg.tar.gz" -C "$TMP_DIR" || { warn "解压失败"; exit 1; }
    BIN_SRC="$(find "$TMP_DIR" -type f -name natpunch-client | head -n1)"
    [ -n "$BIN_SRC" ] || { warn "压缩包内未找到 natpunch-client 二进制"; exit 1; }
    # —— 断连保护 ——
    # SSH 通常通过客户端打通的隧道连接：停止客户端 = 隧道断 = SSH 断。
    # 升级文件已下载并校验完成，此处让后续替换/重启流程脱离终端会话，
    # 忽略挂断信号并改道日志，否则脚本随 SSH 挂断被杀 → 客户端停而不启 → 设备失联。
    if [ -z "${NATPUNCH_UPDATE_DETACHED:-}" ]; then
        export NATPUNCH_UPDATE_DETACHED=1
        echo "==> 升级文件已就绪，流程转入后台执行"
        echo "==> SSH 断开后自动完成替换与重启，日志: /tmp/natpunch_update.log"
        echo "==> 完成后客户端自动重启，隧道恢复后请重新连接"
        trap '' HUP
        exec > /tmp/natpunch_update.log 2>&1 < /dev/null
    fi
    # 停止客户端服务与残留进程（按客户端专属名称精确操作，不影响同机服务端）
    if command -v systemctl >/dev/null 2>&1; then
        if [ -f "$CLIENT_SYSTEMD_1" ] || [ -f "$CLIENT_SYSTEMD_2" ]; then
            systemctl stop natpunch-client 2>/dev/null || true
        fi
    fi
    kill_client_pids
    # 替换二进制（新命名 natpunch-client）
    cp -f "$BIN_SRC" "$CLIENT_BIN_1" || { warn "写入 $CLIENT_BIN_1 失败"; exit 1; }
    chmod 755 "$CLIENT_BIN_1"
    [ -f "$CLIENT_BIN_2" ] && { cp -f "$BIN_SRC" "$CLIENT_BIN_2"; chmod 755 "$CLIENT_BIN_2"; }
    # —— 旧命名客户端迁移（v26.9.5 及以前装的 natpunch 命名）——
    # 老版本只有旧命名自启；若升级后直接清理，新命名自启不存在 → 客户端不自启、不启动 → 失联。
    # 此处从旧自启脚本 / rc.local 提取启动参数，注册新命名自启；提取失败则中止并提示重装。
    NEED_REG=0
    if [ -f "$LEGACY_INIT" ]; then init_owner "$LEGACY_INIT"; [ "$?" = "0" ] && NEED_REG=1; fi
    if [ "$NEED_REG" = "0" ] && command -v systemctl >/dev/null 2>&1; then
        for U in "$LEGACY_UNIT_1" "$LEGACY_UNIT_2"; do
            [ -f "$U" ] || continue
            unit_owner "$U"; [ "$?" = "0" ] && NEED_REG=1 && break
        done
    fi
    if [ "$NEED_REG" = "0" ] && grep -q 'natpunch.*-vkey=' /etc/rc.local 2>/dev/null; then
        NEED_REG=1
    fi
    if [ "$NEED_REG" = "1" ]; then
        ARGS=""
        if [ -z "$ARGS" ] && [ -f "$LEGACY_INIT" ]; then
            L=$(grep -m1 'natpunch.*-vkey=' "$LEGACY_INIT" 2>/dev/null)
            [ -n "$L" ] && ARGS=$(echo "$L" | sed 's/[;&"].*$//' | sed 's|[^ ]*natpunch ||')
        fi
        if [ -z "$ARGS" ] && command -v systemctl >/dev/null 2>&1; then
            for U in "$LEGACY_UNIT_1" "$LEGACY_UNIT_2"; do
                [ -f "$U" ] || continue
                L=$(grep -m1 'ExecStart=.*-vkey=' "$U" 2>/dev/null)
                [ -n "$L" ] && { ARGS=$(echo "$L" | sed 's/.*ExecStart=//; s/[;&"].*$//' | sed 's|[^ ]*natpunch ||'); break; }
            done
        fi
        if [ -z "$ARGS" ] && grep -q 'natpunch.*-vkey=' /etc/rc.local 2>/dev/null; then
            L=$(grep -m1 'natpunch.*-vkey=' /etc/rc.local 2>/dev/null)
            [ -n "$L" ] && ARGS=$(echo "$L" | sed 's/[;&"].*$//' | sed 's|[^ ]*natpunch ||')
        fi
        if [ -n "$ARGS" ]; then
            if [ -f /etc/openwrt_release ]; then
                cat > "$CLIENT_INIT" <<EOF
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    /usr/bin/natpunch-client $ARGS >>/tmp/natpunch-client.log 2>&1 &
    sleep 1
    kill -0 \$! 2>/dev/null || { echo "[NatPunch] 客户端启动失败" >&2; exit 1; }
}
stop() {
    killall natpunch-client 2>/dev/null
    sleep 1
    killall -9 natpunch-client 2>/dev/null || true
}
EOF
                chmod +x "$CLIENT_INIT" || { warn "无法赋予 init 脚本执行权限"; exit 1; }
                "$CLIENT_INIT" enable 2>/dev/null || true
            elif command -v systemctl >/dev/null 2>&1; then
                cat > "$CLIENT_SYSTEMD_1" <<EOF
[Unit]
Description=NatPunch Client
After=network.target
[Service]
Type=simple
ExecStart=/usr/bin/natpunch-client $ARGS
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
EOF
                systemctl daemon-reload 2>/dev/null || true
                systemctl enable natpunch-client 2>/dev/null || true
            fi
            log "旧版命名客户端已迁移到 natpunch-client 自启"
        else
            warn "无法从旧版客户端提取启动参数，请使用一键安装命令重新安装（配置 /etc/natpunch.conf 已保留）"
            exit 1
        fi
    fi
    # 重新启动客户端服务（natpunch-client 专属名称，不触碰服务端 natpunch）
    if [ -f /etc/openwrt_release ]; then
        [ -f "$CLIENT_INIT" ] && "$CLIENT_INIT" start 2>/dev/null || true
    elif command -v systemctl >/dev/null 2>&1; then
        systemctl restart natpunch-client 2>/dev/null || systemctl start natpunch-client 2>/dev/null || true
    fi
    sleep 3
    VER_OUT="$("$CLIENT_BIN_1" -version 2>/dev/null | head -n1)"
    # 清理旧命名客户端残留（进程已在上方精确清理；旧二进制仅在同机无服务端时删除）
    clean_legacy_autostart
    if [ "$SERVER_PRESENT" = "0" ]; then
        rm -f "$LEGACY_BIN_1" "$LEGACY_BIN_2"
    fi
    log "更新完成 ${VER_OUT:-最新版}"
    rm -rf "$TMP_DIR"
    trap - EXIT INT TERM
    exit 0
fi
if [ "$ACTION" != "uninstall" ]; then
    echo "用法: sh uninstall_client.sh [update|uninstall]" >&2
    exit 1
fi

# ================= 卸载模式 =================
# 1. 精确结束客户端进程（新命名 natpunch-client 专属匹配 + 旧命名 -vkey= 匹配，不影响同机服务端）
kill_client_pids
# 2. 停止/删除客户端自启（新命名无条件，旧命名内容判定为客户端才删）
if [ -f "$CLIENT_INIT" ]; then
    "$CLIENT_INIT" stop 2>/dev/null || true
    "$CLIENT_INIT" disable 2>/dev/null || true
    rm -f "$CLIENT_INIT"
fi
rm -f /etc/rc.d/*natpunch-client 2>/dev/null
for f in /etc/rc.d/S??natpunch-client /etc/rc.d/K??natpunch-client /etc/rc*.d/S??natpunch-client /etc/rc*.d/K??natpunch-client; do
    [ -e "$f" ] || continue
    rm -f "$f"
done
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        systemctl stop natpunch-client 2>/dev/null || true
        systemctl disable natpunch-client 2>/dev/null || true
        rm -f "$U"
    done
    systemctl daemon-reload 2>/dev/null || true
fi
clean_legacy_autostart
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then
    if [ -f /usr/local/etc/rc.d/natpunch-client ]; then
        service natpunch-client stop 2>/dev/null || true
        rm -f /usr/local/etc/rc.d/natpunch-client
        command -v sysrc >/dev/null 2>&1 && sysrc -x natpunch_client_enable 2>/dev/null || true
    fi
fi
if [ -d /Library/LaunchDaemons ]; then
    PLIST="/Library/LaunchDaemons/com.natpunch.client.plist"
    if [ -f "$PLIST" ]; then
        launchctl bootout system "$PLIST" 2>/dev/null || true
        launchctl unload "$PLIST" 2>/dev/null || true
        rm -f "$PLIST"
    fi
fi
if [ -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist" ]; then
    launchctl unload "$HOME/Library/LaunchAgents/com.natpunch.client.plist" 2>/dev/null || true
    rm -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist"
fi
# 3. 清理 rc.local
if [ -f /etc/rc.local ]; then
    cp -f /etc/rc.local /etc/rc.local.natpunch-client.bak 2>/dev/null || true
    if sed --version >/dev/null 2>&1; then
        sed -i '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d; /natpunch -server=/d; /natpunch.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    else
        sed -i '' '/natpunch-client -server=/d; /natpunch-client.*-vkey=/d; /natpunch -server=/d; /natpunch.*-vkey=/d' /etc/rc.local 2>/dev/null || true
    fi
fi
# 4. 删除客户端配置
rm -f "$CLIENT_CONF_1" "$CLIENT_CONF_2" "$CLIENT_CONF_3"
if [ -d /etc/natpunch ]; then
    if [ ! -f /etc/natpunch/natpunch ] && [ ! -f /etc/natpunch/conf/natpunch.conf ]; then
        rm -rf /etc/natpunch
    else
        warn "/etc/natpunch 目录含服务端文件，跳过删除"
    fi
fi
if [ -d /usr/local/etc/natpunch ]; then
    if [ ! -f /usr/local/etc/natpunch/natpunch ]; then
        rm -rf /usr/local/etc/natpunch
    else
        warn "/usr/local/etc/natpunch 目录含服务端文件，跳过删除"
    fi
fi
# 5. 删除客户端二进制（新命名无条件删除；旧命名仅在同机无服务端时删除）
rm -f "$CLIENT_BIN_1" "$CLIENT_BIN_2"
if [ "$SERVER_PRESENT" = "0" ]; then
    rm -f "$LEGACY_BIN_1" "$LEGACY_BIN_2"
fi
# 6. 清理日志
rm -f /tmp/natpunch.log /var/log/natpunch.log /tmp/natpunch-client.log /var/log/natpunch-client.log /tmp/natpunch 2>/dev/null || true
# 7. 复查
REMAIN=0
if [ -d /proc ]; then
    for d in /proc/[0-9]*; do
        p=${d#/proc/}
        [ "$p" = "$$" ] && continue
        exe=$(readlink "$d/exe" 2>/dev/null) || continue
        case "$exe" in
            */natpunch-client) REMAIN=1; echo "残留 PID $p: $(tr '\0' ' ' < "$d/cmdline" 2>/dev/null)" ;;
            */natpunch)
                cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
                case "$cmd" in
                    *" -vkey="*) REMAIN=1; echo "残留 PID $p: $cmd" ;;
                esac
                ;;
        esac
    done
fi
if [ "$REMAIN" = "1" ]; then
    warn "仍检测到客户端进程，请手动检查"
    exit 1
fi
log "客户端已卸载完成"
exit 0
