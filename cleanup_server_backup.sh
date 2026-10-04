#!/bin/sh
# NatPunch 服务端备份清理脚本
# 服务端升级会生成 natpunch.bak.<时间戳>（旧二进制）与 conf/natpunch.conf.bak.<时间戳>（面板配置备份），
# 本脚本仅保留最新一份，其余全部删除。
# 用法:
#   sh cleanup_server_backup.sh
set -u
DIR="${NP_DIR:-/opt/natpunch}"
BIN="$DIR/natpunch"
CONF="$DIR/conf/natpunch.conf"
log()  { echo "==> $*"; }
warn() { echo "==> 警告: $*" >&2; }
die()  { echo "==> 错误: $*" >&2; exit 1; }
[ -d "$DIR" ] || die "未找到服务端目录: $DIR（请先安装 NatPunch 服务端）"
# ---------- 清理一组备份：只保留最新 ----------
# 时间戳为 %Y%m%d%H%M%S 定长，字典序即时间序，sort 后取最后一个保留
# clean_backups <前缀> <描述>
clean_backups() {
    PRE="$1"; DESC="$2"
    LIST=""
    for f in "$PRE".bak.*; do
        [ -e "$f" ] || continue
        LIST="$LIST $f"
    done
    if [ -z "$LIST" ]; then
        echo "  $DESC：无历史备份，跳过"
        return 0
    fi
    # shellcheck disable=SC2086（LIST 为 glob 展开结果，路径不含空格）
    set -- $(printf '%s\n' $LIST | sort)
    N=$#
    KEPT=""
    DEL=0
    i=1
    for f in "$@"; do
        if [ "$i" -lt "$N" ]; then
            rm -f "$f" 2>/dev/null && { echo "  已删除: $f"; DEL=$((DEL+1)); }
        else
            KEPT="$f"
        fi
        i=$((i+1))
    done
    echo "  $DESC：共 $N 份，保留 $KEPT，删除 $DEL 份"
}
log "清理 NatPunch 服务端历史备份（仅保留最新）..."
clean_backups "$BIN"  "二进制备份"
clean_backups "$CONF" "面板配置备份"
echo ""
log "清理完成"
