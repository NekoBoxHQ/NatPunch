#!/bin/sh
# NatPunch 客户端一键安装
# 用法: sh install.sh --openwrt VKEY SERVER [PORT]

REPO="lima-droid/NatPunch"
PORT="${4:-8025}"
TLS_FLAG="${5:-}"
VKEY="$2"
SERVER="$3"

if [ -z "$VKEY" ] || [ -z "$SERVER" ]; then
    echo "用法: sh install.sh --openwrt VKEY SERVER [PORT]"
    exit 1
fi

echo "==> 检测架构..."
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) PKG="linux_amd64_client.tar.gz";;
    aarch64|arm64) PKG="linux_arm64_client.tar.gz";;
    *) echo "不支持架构: $ARCH"; exit 1;;
esac
echo "    $ARCH -> $PKG"

# 获取最新版本
echo "==> 获取最新版本..."
VER="$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -n1 | sed 's/.*: *"\([^"]*\)".*/\1/')"
[ -n "$VER" ] || VER="v26.9.3"
echo "    $VER"

# 下载
URL="https://github.com/$REPO/releases/download/$VER/$PKG"
echo "==> 下载 $PKG ..."
dl() { wget -q -O "$2" "$1" 2>/dev/null || curl -fsSL -o "$2" "$1" 2>/dev/null; }
dl "$URL" /tmp/natpunch.tar.gz || { echo "下载失败: $URL"; exit 1; }

# 解压
echo "==> 解压..."
cd /tmp && tar -zxf natpunch.tar.gz && rm -f natpunch.tar.gz
BIN_PATH="$(find /tmp -name natpunch -type f 2>/dev/null | head -n1)"
[ -n "$BIN_PATH" ] || { echo "解压失败: 未找到 natpunch 二进制"; exit 1; }
cp -f "$BIN_PATH" /usr/bin/natpunch && chmod 755 /usr/bin/natpunch && rm -f "$BIN_PATH"

# 写配置
printf "SERVER=%s\nPORT=%s\nVKEY=%s\nTLS_FLAG=%s\n" "$SERVER" "$PORT" "$VKEY" "$TLS_FLAG" > /etc/natpunch.conf
chmod 600 /etc/natpunch.conf

# 注册自启
if [ -d /etc/openwrt_release ] || [ -f /etc/openwrt_release ]; then
    cat > /etc/init.d/natpunch <<EOF
#!/bin/sh /etc/rc.common
START=99
STOP=10
start() {
    . /etc/natpunch.conf
    /usr/bin/natpunch -server=\${SERVER}:\${PORT} -vkey=\${VKEY} -type=tcp $TLS_FLAG >>/tmp/natpunch.log 2>&1 &
}
stop() { killall natpunch 2>/dev/null; }
EOF
    chmod +x /etc/init.d/natpunch
    /etc/init.d/natpunch enable
    /etc/init.d/natpunch start
else
    cat > /etc/systemd/system/natpunch.service <<EOF
[Unit]
Description=NatPunch Client
After=network.target
[Service]
Type=simple
ExecStart=/usr/bin/natpunch -server=${SERVER}:${PORT} -vkey=${VKEY} -type=tcp $TLS_FLAG
Restart=always
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable natpunch
    systemctl start natpunch
    sleep 3
    if ! systemctl is-active natpunch >/dev/null 2>&1; then
        echo "==> 启动失败，日志："
        journalctl -u natpunch -n 20 --no-pager 2>/dev/null
    fi
fi

sleep 2
if command -v systemctl >/dev/null 2>&1; then
    OK=$(systemctl is-active natpunch 2>/dev/null)
else
    OK=$(ps w 2>/dev/null | grep -v grep | grep -c "/usr/bin/natpunch")
fi
if [ "$OK" = "active" ] || [ "$OK" -gt 0 ] 2>/dev/null; then
    echo "==> 安装成功 ✓ $(/usr/bin/natpunch -version | head -n1)"
else
    echo "==> 启动失败，看 /tmp/natpunch.log"
    cat /tmp/natpunch.log 2>/dev/null | tail -10
fi
