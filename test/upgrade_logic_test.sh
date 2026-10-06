#!/bin/sh
# 升级 / 安装脚本的自检：把纯逻辑函数抠出来跑一遍断言。
#
# 为什么需要它：这几段代码出错时**不会报错**，只会静默做错事 ——
#   · 版本挑错 → "更新"完反而降级，之后可能连不上服务端；
#   · 包校验被绕过 → 拿一个被替换过的 root 二进制去执行；
#   · 内嵌的 apply / guard 脚本有语法错 → 升级走到一半断在那儿，客户端停着。
# 这些都必须在下发之前被机器挡住，不能靠人肉 review。
#
# 用法：sh test/upgrade_logic_test.sh
set -u
cd "$(dirname "$0")/.." || exit 1
SRC="uninstall_client.sh"
INSTALL="install.sh"

FAIL=0
ok()  { echo "  ✓ $1"; }
bad() { echo "  ✗ $1"; FAIL=1; }
chk() { if [ "$1" = "$2" ]; then ok "$3"; else bad "$3（得到 '$1'，期望 '$2'）"; fi; }

# 从脚本里原样抠出一个函数
extract_fn() {
    awk -v fn="$1()" '
        index($0, fn " {") == 1 { grab = 1 }
        grab { print }
        grab && $0 == "}" { exit }
    ' "$SRC"
}
# 从脚本里抠出一段 heredoc 正文（按结束标记）
extract_heredoc() {
    awk -v tag="$1" '
        !grab && $0 ~ ("<<." tag ".") { grab = 1; next }
        grab && $0 == tag { exit }
        grab { print }
    ' "$SRC"
}

echo "== 1. 内嵌脚本语法（shell 不会解析 heredoc 正文，必须单独验） =="
for tag in APPLY GUARD; do
    body="$(extract_heredoc "$tag")"
    if [ -z "$body" ]; then bad "没能从 $SRC 抠出 $tag"; continue; fi
    printf '%s\n' "$body" > /tmp/_selfcheck_$tag.sh
    if sh -n /tmp/_selfcheck_$tag.sh 2>/tmp/_selfcheck_err; then
        ok "$tag 语法正确（$(printf '%s\n' "$body" | wc -l) 行）"
    else
        bad "$tag 语法错误: $(cat /tmp/_selfcheck_err)"
    fi
    rm -f /tmp/_selfcheck_$tag.sh /tmp/_selfcheck_err
done

echo "== 2. 版本挑选（错了会静默降级） =="
pf="$(extract_fn pick_highest_version)"
if [ -z "$pf" ]; then
    bad "没能从 $SRC 抠出 pick_highest_version"
else
    eval "$pf"
    chk "$(printf '[{"tag_name":"v26.9.111"},{"tag_name":"v26.9.120"},{"tag_name":"v26.9.109"}]' | pick_highest_version)" \
        "v26.9.120" "从一行 JSON 里取最高（不是第一个）"
    chk "$(printf '[\n  {"tag_name": "v26.9.9"},\n  {"tag_name": "v26.9.111"}\n]' | pick_highest_version)" \
        "v26.9.111" "多行 JSON + 字典序陷阱（v26.9.9 不能赢 v26.9.111）"
    chk "$(printf '[{"tag_name":"v26.9.111"},{"tag_name":"nightly"},{"tag_name":"v26.10.1-beta"},{"tag_name":"v26.10.2"}]' | pick_highest_version)" \
        "v26.10.2" "跳过非法 / 预发布 tag"
    chk "$(printf '[{"name":"x"}]' | pick_highest_version)" \
        "" "没有可识别 tag 时输出空（调用方会回退 latest 并告警）"
    chk "$(printf '[{"tag_name":"v26.10.0"},{"tag_name":"v26.9.999"}]' | pick_highest_version)" \
        "v26.10.0" "次版本优先于补丁号"
fi

echo "== 3. 两个脚本的基础语法 =="
for f in "$INSTALL" "$SRC"; do
    if sh -n "$f" 2>/tmp/_selfcheck_err; then ok "$f"; else bad "$f: $(cat /tmp/_selfcheck_err)"; fi
done
rm -f /tmp/_selfcheck_err

echo "== 4. 守护配置不能被改回会放弃重启的值 =="
# procd 的 respawn <threshold> <timeout> <retry>：retry > 0 时崩够次数就永久放手。
if grep -q 'procd_set_param respawn 3600 5 0' "$INSTALL"; then
    ok "procd respawn 的 retry 是 0（永不放弃）"
else
    bad "procd respawn 的 retry 不是 0 —— 崩溃超过 retry 次后 procd 会彻底停止守护"
fi
if grep -q '^StartLimitIntervalSec=0' "$INSTALL"; then
    ok "systemd 关掉了启动频率限制"
else
    bad "systemd unit 缺 StartLimitIntervalSec=0 —— 短时间多次重启后会被打成 failed 并停止重启"
fi

echo "== 5. 签名链不能被改回「拿不到签名就放行」 =="
# 背景：SHA256 比对的 SHA256SUMS 与包来自同一渠道，属于自洽校验。攻击者控制发布渠道时，
# 只要不上传 .minisig、或把内置校验器换成乱码，就能把签名这层整个降级掉。
# 所以「签名不可用」必须是中止，而不是打印一句警告继续装 —— 这是往 root 目录放二进制。
for f in "$INSTALL" install_server.sh "$SRC"; do
    if grep -q 'sig_unavailable' "$f"; then
        ok "$f 走统一收口 sig_unavailable"
    else
        bad "$f 缺少 sig_unavailable"
    fi
    if grep -q '跳过签名校验' "$f"; then
        bad "$f 仍有「跳过签名校验」的放行分支"
    else
        ok "$f 没有签名放行分支"
    fi
done
if grep -q '哈希与内嵌信任锚不符（疑似被篡改），已拒绝执行' "$INSTALL"; then
    ok "校验器哈希不符是硬失败（不是警告）"
else
    bad "校验器哈希不符必须直接中止，否则换掉校验器就能逼出降级"
fi
if grep -q '^MSC_SHA256_amd64=' "$SRC"; then
    ok "uninstall_client.sh 内嵌了校验器信任锚"
else
    bad "uninstall_client.sh 缺 MSC_SHA256_* 信任锚，OpenWrt 上会永远落到「无校验手段」分支"
fi
if grep -q 'NATPUNCH_ALLOW_UNSIGNED' "$INSTALL" && grep -q 'NATPUNCH_ALLOW_UNSIGNED' "$SRC"; then
    ok "无签名环境留有显式逃生开关"
else
    bad "缺少 NATPUNCH_ALLOW_UNSIGNED 逃生开关"
fi

echo
if [ "$FAIL" -eq 1 ]; then
    echo "自检失败"
    exit 1
fi
echo "自检全部通过"
