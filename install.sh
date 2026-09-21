#!/bin/sh
# ============================================================
#  NatPunch 客户端一键安装（OpenWrt 软路由 / 通用 Linux）
#
#  用法: sh install.sh --openwrt 'VKEY' [SERVER] [PORT] [下载地址]
#  默认从 GitHub 项目 Release 自动获取最新版本下载，服务端无需存放任何文件
# ============================================================
set -e

VKEY="${2:-}"
SERVER="${3:-h.g.s-ui.com}"
PORT="${4:-8025}"
DL_BASE="${5:-auto}"

[ -n "$VKEY" ] || { echo "用法: sh $0 --openwrt 'VKEY' [SERVER] [PORT]"; exit 1; }

echo "==> 检测架构..."
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)   BIN="npc_amd64" ;;
    aarch64|arm64)  BIN="npc_arm64" ;;
    *)
        echo "不支持该架构: $ARCH"
        echo "当前下载源仅提供 amd64 / arm64 客户端"
        exit 1
        ;;
esac

echo "==> 下载 npc (${BIN})..."
rm -f /tmp/npc
dl() { uclient-fetch -q -O "$2" "$1" 2>/dev/null || wget -q -O "$2" "$1" 2>/dev/null || curl -fsS -o "$2" "$1" 2>/dev/null; }
dl_ok=""

if [ "$DL_BASE" = "auto" ]; then
    echo "==> 获取最新版本 (GitHub API)..."
    VER=""
    if uclient-fetch -q -O /tmp/nps_ver.json "https://api.github.com/repos/lima-droid/NatPunch/releases/latest" 2>/dev/null \
       || wget -q -O /tmp/nps_ver.json "https://api.github.com/repos/lima-droid/NatPunch/releases/latest" 2>/dev/null; then
        VER=$(grep '"tag_name"' /tmp/nps_ver.json 2>/dev/null | head -n1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    fi
    rm -f /tmp/nps_ver.json
    [ -n "$VER" ] || VER="v0.26.38"
    echo "    最新版本: $VER"
    if dl "https://github.com/lima-droid/NatPunch/releases/download/$VER/$BIN" /tmp/npc; then
        dl_ok=1
    fi
else
    if dl "${DL_BASE}/${BIN}" /tmp/npc; then
        dl_ok=1
    fi
fi

if [ -z "$dl_ok" ]; then
    echo "下载失败，请检查网络，或指定镜像/可用下载源:"
    echo "  sh $0 --openwrt 'VKEY' ${SERVER} ${PORT} https://镜像前缀/https://github.com/lima-droid/NatPunch/releases/download/v0.26.38"
    exit 1
fi

# 校验 ELF 文件头，防止 404 页面等错误内容被当成 npc 安装
if [ "$(head -c 4 /tmp/npc 2>/dev/null)" != "$(printf '\177ELF')" ]; then
    echo "下载的文件不是有效的可执行程序（ELF），请检查下载源"
    rm -f /tmp/npc
    exit 1
fi
chmod 755 /tmp/npc
cp -f /tmp/npc /usr/bin/npc
chmod 755 /usr/bin/npc
rm -f /tmp/npc

echo "==> 写入 vkey 配置..."
printf 'SERVER=%s\nPORT=%s\nVKEY=%s\n' "$SERVER" "$PORT" "$VKEY" > /etc/npc.conf
chmod 600 /etc/npc.conf

if [ -f /etc/rc.common ]; then
    echo "==> 注册 OpenWrt 开机自启..."
    cat > /etc/init.d/npc <<EOF
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    [ -x /usr/bin/npc ] || return 0
    /usr/bin/npc -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp -tls_enable=true >>/tmp/npc.log 2>&1 &
}
stop() {
    killall npc 2>/dev/null || true
}
EOF
    chmod +x /etc/init.d/npc
    /etc/init.d/npc enable
    /etc/init.d/npc start
elif command -v systemctl >/dev/null 2>&1; then
    echo "==> 注册 systemd 开机自启..."
    cat > /etc/systemd/system/npc.service <<EOF
[Unit]
Description=NPS Client
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/npc -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp -tls_enable=true
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable npc >/dev/null 2>&1 || true
    if ! systemctl start npc; then
        echo "==> systemctl start npc 失败，最近日志:"
        journalctl -u npc --no-pager -n 15 2>/dev/null || true
        exit 1
    fi
else
    echo "==> 使用 rc.local 开机自启..."
    /usr/bin/npc -server="${SERVER}:${PORT}" -vkey="${VKEY}" -type=tcp -tls_enable=true >>/tmp/npc.log 2>&1 &
    if ! grep -q "/usr/bin/npc" /etc/rc.local 2>/dev/null; then
        if grep -q "^exit 0" /etc/rc.local 2>/dev/null; then
            sed -i "s|^exit 0|/usr/bin/npc -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp -tls_enable=true >>/tmp/npc.log 2>\&1 \&\nexit 0|" /etc/rc.local
        else
            echo "/usr/bin/npc -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp -tls_enable=true >>/tmp/npc.log 2>&1 &" >> /etc/rc.local
        fi
    fi
fi

sleep 2
npc_ok=""
if command -v systemctl >/dev/null 2>&1 && systemctl is-active npc >/dev/null 2>&1; then
    npc_ok=1
elif [ -x /etc/init.d/npc ] && /etc/init.d/npc status >/dev/null 2>&1; then
    npc_ok=1
elif ps w 2>/dev/null | grep -v grep | grep -q "/usr/bin/npc"; then
    npc_ok=1
fi
if [ -n "$npc_ok" ]; then
    echo ""
    echo "==> npc 已启动 ✓  vkey: ${VKEY}"
    echo "    现在可以回到管理台 http://${SERVER}:28080 查看设备上线状态"
else
    echo "==> npc 未检测到运行，请查看: systemctl status npc 或 /tmp/npc.log"
    exit 1
fi
