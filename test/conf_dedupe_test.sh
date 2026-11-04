#!/bin/sh
# 校验 install_server.sh 的 dedupe_conf_keys：重复键只保留最后一次，且无重复时不改文件
set -u
cd "$(dirname "$0")/.." || exit 1
FAIL=0
ok()  { echo "  ✓ $1"; }
bad() { echo "  ✗ $1"; FAIL=1; }

FN="$(awk '/^dedupe_conf_keys\(\) \{/{f=1} f{print} f&&/^\}$/{exit}' install_server.sh)"
[ -n "$FN" ] || { echo "  ✗ 没能从 install_server.sh 抽出 dedupe_conf_keys"; exit 1; }

TMP="$(mktemp -d)" || exit 1
trap 'rm -rf "$TMP"' EXIT

# 桩：只提供函数依赖的变量与 info()
cat > "$TMP/harness.sh" <<'EOF'
CONF=""
info() { :; }
EOF
printf '%s\n' "$FN" >> "$TMP/harness.sh"

run() { # run <conf 内容> -> 输出处理后的文件内容
    printf '%s' "$1" > "$TMP/natpunch.conf"
    CONF="$TMP/natpunch.conf"
    export CONF
    sh -c ". '$TMP/harness.sh'; CONF='$TMP/natpunch.conf'; dedupe_conf_keys; cat '$TMP/natpunch.conf'"
}

echo "== dedupe_conf_keys =="

# 1) 重复键：保留最后一次（/natpunch）
OUT="$(run 'web_port=8443
web_base_url=
web_base_url=/natpunch
tls_enable=true
')"
if [ "$(printf '%s\n' "$OUT" | grep -c '^web_base_url')" = "1" ]; then
    ok "重复键被合并为一行"
else
    bad "重复键没有被合并：$OUT"
fi
# 空值 + 非空同时存在 = 模板遗留，应恢复到根路径（空值）
if printf '%s\n' "$OUT" | grep -qx 'web_base_url='; then
    ok "空值与非空重复时恢复为根路径（web_base_url=，模板遗留 bug 自愈）"
else
    bad "没有恢复成根路径：$OUT"
fi
if printf '%s\n' "$OUT" | grep -qx 'tls_enable=true' && printf '%s\n' "$OUT" | grep -qx 'web_port=8443'; then
    ok "其它键与顺序未受影响"
else
    bad "其它键被破坏：$OUT"
fi

# 2) 无重复：不动文件（保持原样，包括注释与空行）
SRC='web_port=8443
# 注释里出现 web_base_url= 不算键
web_base_url=

tls_enable=true
'
OUT2="$(run "$SRC")"
if [ "$OUT2" = "$(printf '%s' "$SRC")" ]; then
    ok "无重复时文件内容原样保留（注释/空行都不变）"
else
    bad "无重复时文件被改动：$OUT2"
fi


# 3) 两个非空重复：保留最后一次（真正的后写覆盖先写语义）
OUT3="$(run 'web_base_url=/old
web_base_url=/new
')"
if printf '%s\n' "$OUT3" | grep -qx 'web_base_url=/new' && [ "$(printf '%s\n' "$OUT3" | grep -c '^web_base_url')" = "1" ]; then
    ok "两个非空重复时保留最后一次（/new）"
else
    bad "非空重复处理不对：$OUT3"
fi
echo
if [ "$FAIL" -eq 1 ]; then echo "配置去重测试失败"; exit 1; fi
echo "配置去重测试通过"
