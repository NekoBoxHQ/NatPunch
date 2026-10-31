#!/bin/sh
# selfhost-publish.sh —— 把某个版本的发布物拉到面板服务器的静态目录，形成「自建源」。
#
# 用法：
#   sh selfhost-publish.sh <安装目录> <发布源URL> <版本> [脚本源URL]
# 例：
#   sh selfhost-publish.sh /opt/natpunch https://github.com/NekoBoxHQ/NatPunch/releases/download/v26.10.60 v26.10.60
#
# 结果：
#   <安装目录>/web/static/dl/<版本>/   包 + SHA256SUMS + .minisig + 校验器 + 三个脚本
#   <安装目录>/web/static/dl/          上面三个脚本的稳定入口（始终指向最新版本）
#
# 设计要点：
# - 只写静态目录，不动服务、不重启；面板通过磁盘覆盖层直接对外提供
# - 脚本一律从「脚本源」取原始版本（不注入任何地址）——
#   面板生成的命令会在管道里带上 NATPUNCH_BASE_URL，域名随面板访问域名走，
#   所以换域名/换端口都不需要重新生成脚本
# - 任何单个文件失败都不影响其余文件，也不会让安装/升级失败（自建源是增值能力）
set -u

DIR="${1:-/opt/natpunch}"
SRC_URL="${2:-}"
VER="${3:-}"
SCRIPT_URL="${4:-https://raw.githubusercontent.com/NekoBoxHQ/NatPunch/master}"

[ -n "$SRC_URL" ] || { echo "selfhost: 缺少发布源 URL" >&2; exit 1; }
[ -n "$VER" ] || { echo "selfhost: 缺少版本号" >&2; exit 1; }
SRC_URL="${SRC_URL%/}"

DLROOT="$DIR/web/static/dl"
VERDIR="$DLROOT/$VER"
mkdir -p "$VERDIR" 2>/dev/null || { echo "selfhost: 无法创建 $VERDIR" >&2; exit 1; }

fetch() { # fetch <url> <dest>：优先 curl，其次 wget
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --max-time 300 -o "$2" "$1" 2>/dev/null && return 0
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -q --timeout=60 -O "$2" "$1" 2>/dev/null && return 0
    fi
    return 1
}

ok=0; fail=0
for f in \
    linux_amd64_client.tar.gz linux_arm64_client.tar.gz linux_arm_client.tar.gz \
    linux_amd64_server.tar.gz linux_arm64_server.tar.gz linux_arm_server.tar.gz \
    minisign-check-linux-amd64 minisign-check-linux-arm64 minisign-check-linux-arm \
    SHA256SUMS SHA256SUMS.minisig
do
    [ -s "$VERDIR/$f" ] && { ok=$((ok+1)); continue; }
    if fetch "$SRC_URL/$f" "$VERDIR/$f"; then ok=$((ok+1)); else rm -f "$VERDIR/$f"; fail=$((fail+1)); fi
done

# 三个脚本：取原始版本，不做地址注入
for f in install.sh uninstall_client.sh install_server.sh; do
    if fetch "$SCRIPT_URL/$f" "$VERDIR/$f"; then
        chmod 644 "$VERDIR/$f"
        cp -f "$VERDIR/$f" "$DLROOT/$f" 2>/dev/null && chmod 644 "$DLROOT/$f"
        ok=$((ok+1))
    else
        fail=$((fail+1))
    fi
done

chmod 755 "$VERDIR"/minisign-check-linux-* 2>/dev/null
chmod 644 "$VERDIR"/* 2>/dev/null

# 自检：包能对应上校验和，才认为自建源可用。
# 退出码给调用方用：0=可用；1=不完整/校验不符（install_server.sh 会据此还原上一版 dl）。
RC=1
if [ -s "$VERDIR/SHA256SUMS" ] && [ -s "$VERDIR/linux_amd64_client.tar.gz" ]; then
    want=$(awk '$2=="linux_amd64_client.tar.gz"{print $1}' "$VERDIR/SHA256SUMS")
    got=$(sha256sum "$VERDIR/linux_amd64_client.tar.gz" 2>/dev/null | awk '{print $1}')
    if [ -n "$want" ] && [ "$want" = "$got" ]; then
        RC=0
        echo "selfhost: 已发布 $VER 到 $VERDIR（校验一致，ok=$ok fail=$fail）"
    else
        echo "selfhost: 失败——$VER 的 SHA256 与包不一致（ok=$ok fail=$fail）" >&2
    fi
else
    echo "selfhost: 失败——$VER 发布不完整（ok=$ok fail=$fail）" >&2
fi
# 稳定入口三个脚本缺任何一个也算不完整（客户端靠它们安装）
for f in install.sh uninstall_client.sh install_server.sh; do
    if [ ! -s "$DLROOT/$f" ]; then
        echo "selfhost: 失败——稳定入口缺 $DLROOT/$f" >&2
        RC=1
    fi
done
exit $RC
