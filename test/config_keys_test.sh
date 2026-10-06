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

echo "== 4. 面板静态资源对账（模板引用的文件必须真的在） =="
# 由来：v26.10.x 的一次「按面板实际入口清理不可达的页面与接口」把
# web/static/js/echarts.min.js 当成"面板里没人用的图表库"删掉了，理由写的是
# "仪表盘是数字条 + CSS"。但仪表盘（views/index/index.html）只是往 chartdatas
# 里塞配置，真正的 echarts.init 在 static/js/language.js 的 setLang 里统一调用 ——
# **模板里搜不到 "echarts" 字样**，于是被误判成无人使用。删掉之后仪表盘的
# 负载/CPU/内存/连接数/带宽/流量统计/连接类型 七张图全成了空白框，
# 而编译、CI、面板其它页面一切正常，只有人肉打开面板才看得见。
# 所以这里改成机器核对，而不是靠"搜关键词猜有没有人用"。
miss=0
nref=0
for r in $(grep -rhoE '/static/[A-Za-z0-9._/-]+' web/views/ 2>/dev/null | sort -u); do
    nref=$((nref + 1))
    [ -e "web$r" ] || { bad "模板引用了 $r，但 web$r 不存在（面板对应功能会静默变成空白）"; miss=1; }
done
if [ "$nref" -eq 0 ]; then
    bad "没能从 web/views/ 里抠出任何 /static/ 引用（模板路径变了？）"
elif [ "$miss" -eq 0 ]; then
    ok "$nref 个静态资源引用全部存在"
fi
# 上面那条只能保证"引用了就存在"。如果连 layout.html 里的加载行也一起删掉，
# 它就查不出来了 —— 所以再单独钉一条语义断言：有模板填 chartdatas，就得有画的人。
if git grep -l 'chartdatas\[' -- web/views >/dev/null 2>&1; then
    if grep -q 'static/js/echarts.min.js' web/views/public/layout.html \
       && [ -f web/static/js/echarts.min.js ]; then
        ok "有模板在用 chartdatas，layout.html 也确实加载了 echarts"
    else
        bad "有模板在用 chartdatas，但 echarts 没了（layout.html 的 <script> 被删 或 文件缺失）—— 仪表盘图表会全变空白框"
    fi
    if grep -q 'echarts\.init' web/static/js/language.js; then
        ok "language.js 里确实在调 echarts.init（chartdatas 有人消费）"
    else
        bad "language.js 里没有 echarts.init —— chartdatas 填了也没人画"
    fi
else
    ok "没有模板使用 chartdatas（这一版不需要 echarts）"
fi

echo
if [ "$FAIL" -eq 1 ]; then
    echo "配置键对账失败"
    exit 1
fi
echo "配置键对账通过"
