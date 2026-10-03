#!/bin/sh
# NatPunch 客户端管理脚本（卸载 / 更新）
# 仅操作客户端专属命名 natpunch-client，与服务端 natpunch 完全隔离（进程名/自启名/二进制名均不同），互不影响
# 用法:
#   sh uninstall_client.sh             卸载客户端（默认）
#   sh uninstall_client.sh uninstall   卸载客户端
#   sh uninstall_client.sh update      更新客户端（保留 /etc/natpunch.conf 配置）
set -u
REPO="NekoBoxHQ/NatPunch"
ACTION="${1:-uninstall}"
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
log() { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }

# ---------- 更新模式：保留配置，仅替换二进制并重启 ----------
if [ "$ACTION" = "update" ]; then
    log "更新 NatPunch 客户端（保留配置）..."
    if [ ! -f "$CLIENT_BIN_1" ] && [ ! -f "$CLIENT_BIN_2" ]; then
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
    # 面板 SSH 终端更是客户端打通的 PTY：kill 客户端 = PTY 会话一起断。
    # 因此将"替换+重启"逻辑写成仓库独立脚本 update_client_apply.sh，
    # 此处无条件拉取后写入 /tmp，用 setsid 彻底脱离当前会话后台执行，当前脚本立即退出；
    # 否则脚本随会话一起被杀 → 客户端停而不启 → 设备失联。
    # 注意：不能用环境变量做"是否已 detach"判定——面板 SSH 的 shell 继承客户端进程环境，
    # 之前 export 的 NATPUNCH_UPDATE_DETACHED 会残留到客户端进程链，导致本段被跳过。
    # 本脚本不存在重入（apply 逻辑在独立脚本中），故此处无条件执行。
    APPLY_URL="https://cdn.jsdelivr.net/gh/$REPO@master/update_client_apply.sh"
    fetch_apply() {
        if command -v wget >/dev/null 2>&1; then
            wget -q -O /tmp/natpunch_apply.sh "$1"
        elif command -v curl >/dev/null 2>&1; then
            curl -fsSL -o /tmp/natpunch_apply.sh "$1"
        else
            warn "未找到 wget / curl"; exit 1
        fi
    }
    fetch_apply "$APPLY_URL" || { warn "拉取更新脚本失败"; exit 1; }
    # 内容校验：jsdelivr 偶发 404 缓存返回非脚本内容，必须是以 #!/bin/sh 开头的有效脚本
    if ! head -1 /tmp/natpunch_apply.sh 2>/dev/null | grep -q '^#!'; then
        APPLY_URL="https://raw.githubusercontent.com/$REPO/master/update_client_apply.sh"
        fetch_apply "$APPLY_URL" || { warn "拉取更新脚本失败"; exit 1; }
    fi
    [ -s /tmp/natpunch_apply.sh ] || { warn "更新脚本为空"; exit 1; }
    chmod 755 /tmp/natpunch_apply.sh
    echo "==> 升级文件已就绪，流程转入后台执行"
    echo "==> SSH 断开后自动完成替换与重启，日志: /tmp/natpunch_update.log"
    echo "==> 完成后客户端自动重启，隧道恢复后请重新连接"
    # 关键保护（面板 SSH 升级场景）：更新子脚本由客户端派生，位于 natpunch-client.service 的 cgroup 内；
    # systemd 默认 KillMode=control-group，stop 会连带杀掉更新子脚本 → 客户端停而不启。
    # 必须在 detach 前（主脚本还活着、客户端还在跑时）先把 unit 改为 KillMode=process 并重载。
    if command -v systemctl >/dev/null 2>&1; then
        for U in /etc/systemd/system/natpunch-client.service /lib/systemd/system/natpunch-client.service; do
            if [ -f "$U" ]; then
                if grep -q '^KillMode=' "$U" 2>/dev/null; then
                    sed -i 's/^KillMode=.*/KillMode=process/' "$U" 2>/dev/null || true
                else
                    sed -i '/^RestartSec=/a KillMode=process' "$U" 2>/dev/null || true
                    grep -q '^KillMode=process' "$U" || echo "KillMode=process" >> "$U"
                fi
            fi
        done
        systemctl daemon-reload >/dev/null 2>&1 &
        DR=$!
        sleep 3
        kill "$DR" 2>/dev/null || true
    fi
    if command -v setsid >/dev/null 2>&1; then
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" NP_CLIENT_INIT="$CLIENT_INIT" NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" setsid sh /tmp/natpunch_apply.sh > /tmp/natpunch_update.log 2>&1 < /dev/null &
    else
        NP_TMP_DIR="$TMP_DIR" NP_BIN_SRC="$BIN_SRC" NP_CLIENT_BIN_1="$CLIENT_BIN_1" NP_CLIENT_BIN_2="$CLIENT_BIN_2" NP_CLIENT_INIT="$CLIENT_INIT" NP_CLIENT_SYSTEMD_1="$CLIENT_SYSTEMD_1" NP_CLIENT_SYSTEMD_2="$CLIENT_SYSTEMD_2" nohup sh /tmp/natpunch_apply.sh > /tmp/natpunch_update.log 2>&1 < /dev/null &
    fi
    # 关键：清空 EXIT/INT/TERM trap，避免 exit 0 时把 $TMP_DIR（升级文件）删掉，
    # 否则后台子脚本替换时会找不到源文件 → 客户端停而不启。TMP_DIR 由子脚本末尾清理。
    trap - EXIT INT TERM
    exit 0
fi
if [ "$ACTION" != "uninstall" ]; then
    echo "用法: sh uninstall_client.sh [update|uninstall]" >&2
    exit 1
fi

# ================= 卸载模式 =================
# 断连保护（与升级链路同一机制）：
# 面板 SSH 会话在 natpunch-client.service 的 cgroup 内，systemd 默认 KillMode=control-group，
# `systemctl stop` 会连带杀掉正在执行的卸载脚本 → 卸载中断、残留。
# 因此：1) 先把 KillMode 改为 process（只杀主进程）；2) 卸载动作写成独立脚本 setsid 后台静默执行，
# 与当前 SSH 会话完全解耦——杀客户端即断连，但后台清理照常跑完，完整无残留。
if command -v systemctl >/dev/null 2>&1; then
    for U in "$CLIENT_SYSTEMD_1" "$CLIENT_SYSTEMD_2"; do
        [ -f "$U" ] || continue
        if grep -q '^KillMode=' "$U" 2>/dev/null; then
            sed -i 's/^KillMode=.*/KillMode=process/' "$U" 2>/dev/null || true
        else
            sed -i '/^RestartSec=/a KillMode=process' "$U" 2>/dev/null || true
            grep -q '^KillMode=process' "$U" || echo "KillMode=process" >> "$U"
        fi
    done
    systemctl daemon-reload >/dev/null 2>&1 &
    DR=$!
    sleep 3
    kill "$DR" 2>/dev/null || true
fi
# 拉取卸载应用脚本（jsdelivr 优先，raw 兜底；内容校验避免 CDN 缓存返回非脚本内容）
UN_URL="https://cdn.jsdelivr.net/gh/$REPO@master/uninstall_client_apply.sh"
fetch_unapply() {
    if command -v wget >/dev/null 2>&1; then
        wget -q -O /tmp/natpunch_uninstall.sh "$1"
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL -o /tmp/natpunch_uninstall.sh "$1"
    else
        return 1
    fi
}
fetch_unapply "$UN_URL" || { warn "拉取卸载脚本失败"; exit 1; }
if ! head -1 /tmp/natpunch_uninstall.sh 2>/dev/null | grep -q '^#!'; then
    UN_URL="https://raw.githubusercontent.com/$REPO/master/uninstall_client_apply.sh"
    fetch_unapply "$UN_URL" || { warn "拉取卸载脚本失败"; exit 1; }
fi
[ -s /tmp/natpunch_uninstall.sh ] || { warn "卸载脚本为空"; exit 1; }
chmod 755 /tmp/natpunch_uninstall.sh
# setsid 静默后台执行（输出仅写日志，不打扰终端）；SSH 断连不影响后续清理
if command -v setsid >/dev/null 2>&1; then
    setsid sh /tmp/natpunch_uninstall.sh >> /tmp/natpunch_uninstall.log 2>&1 < /dev/null &
else
    nohup sh /tmp/natpunch_uninstall.sh >> /tmp/natpunch_uninstall.log 2>&1 < /dev/null &
fi
exit 0
