#!/bin/sh
# 配置键对账：conf/natpunch.conf 里**生效的**每个键，代码里都必须真的读它。
#
# 为什么需要它：重写/精简启动逻辑时，很容易顺手把某一行 AppConfig 读取丢掉 ——
# 代码照样编译、CI 照样全绿，但功能**静默失效**。
# 本脚本的由来就是真实踩过的坑：重写 cmd/natpunch/natpunch.go 时漏掉了
#   bridge.ServerTlsEnable = beego.AppConfig.DefaultBool("tls_enable", false)
# 后果是服务端永远不开 TLS 桥接（bridge/bridge.go 的 `if ServerTlsEnable`），
# 面板的「TLS 一键命令」也永远不出现（web/controllers/base.go 的 useTls），
# 而配置文件里明明写着 tls_enable=true。
#
# 用法：sh test/config_keys_test.sh
set -u
cd "$(dirname "$0")/.." || exit 1
CONF="conf/natpunch.conf"

FAIL=0
ok()  { echo "  ✓ $1"; }
bad() { echo "  ✗ $1"; FAIL=1; }

# 1) conf 里声明的生效键（忽略注释行与空行）
KEYS="$(grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$CONF" | sed 's/=.*//' | tr -d ' \t' | sort -u)"
# 2) 代码里通过 AppConfig 读取的键
# 注意方法名里可能带数字（DefaultInt64 / DefaultFloat），字符类必须含 0-9，
# 否则会漏掉 DefaultInt64 这种读取，进而把配置键误报成"没人读"。
READ="$(git grep -h -o -E 'AppConfig\.[A-Za-z_0-9]+\("[a-z_0-9]+"' -- '*.go' \
        | sed 's/.*("//' | tr -d '"' | sort -u)"

echo "== 1. conf 声明的每个生效键，代码里都有人读 =="
n=0
for k in $KEYS; do
    n=$((n + 1))
    if ! printf '%s\n' "$READ" | grep -qx "$k"; then
        bad "$CONF 声明了 $k，但代码里没有任何地方读它（配置形同虚设）"
    fi
done
[ "$FAIL" -eq 0 ] && ok "$n 个生效键全部有代码读取"

echo "== 2. 关键键必须两边都在（漏了会静默降级） =="
for k in tls_enable bridge_type bridge_port log_level log_path web_username web_password; do
    if printf '%s\n' "$KEYS" | grep -qx "$k"; then
        ok "$CONF 含 $k"
    else
        bad "$CONF 缺少关键键 $k"
    fi
    if printf '%s\n' "$READ" | grep -qx "$k"; then
        ok "代码读取 $k"
    else
        bad "代码从不读取 $k —— 该功能会静默失效"
    fi
done

echo "== 3. 代码里自动生成的默认配置模板同样对账 =="
# defaultNatPunchConf 是「二进制在空目录里裸跑」时写出来的配置。
# 注意首行是 `const defaultNatPunchConf = \`http_proxy_ip=...`，键和 Go 语法挤在同一行，
# 必须先把前缀剥掉再取键，否则第一个键会被漏掉（我第一版就栽在这里）。
TPL_KEYS="$(sed -n '/^const defaultNatPunchConf = /,/^`$/p' cmd/natpunch/natpunch.go \
            | sed 's/^const defaultNatPunchConf = `//' \
            | grep -E '^[a-z_0-9]+=' | sed 's/=.*//' | sort -u)"
tn=0
for k in $TPL_KEYS; do
    tn=$((tn + 1))
    if ! printf '%s\n' "$READ" | grep -qx "$k"; then
        bad "默认模板声明了 $k，但代码里没有任何地方读它"
    fi
done
[ "$tn" -gt 0 ] || bad "没能从 cmd/natpunch/natpunch.go 里抠出默认模板的键"
[ "$tn" -gt 0 ] && ok "默认模板 $tn 个键全部有代码读取"

echo
if [ "$FAIL" -eq 1 ]; then
    echo "配置键对账失败"
    exit 1
fi
echo "配置键对账通过"
