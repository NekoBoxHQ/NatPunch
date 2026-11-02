#!/bin/sh
# 检查脚本语法 + 验证 NATPUNCH_BASE_URL 的版本解析（dl 根目录 / 版本目录 / 无 version.txt）
set -u
cd "$(dirname "$0")/.." || exit 1
FAIL=0

echo "== sh -n =="
for f in install.sh install_server.sh uninstall_client.sh scripts/selfhost-publish.sh; do
    if sh -n "$f" 2>/dev/null; then echo "  OK   $f"; else echo "  FAIL $f"; FAIL=1; fi
done

echo
echo "== 版本解析：从 uninstall_client.sh 里抽出 nx_resolve_base_url 单独跑 =="
FN="$(awk '/nx_resolve_base_url\(\) \{/{f=1} f{print} f&&/^[[:space:]]*\}$/{exit}' uninstall_client.sh | sed 's/^    //')"
if [ -z "$FN" ]; then echo "  FAIL 没抽到函数"; exit 1; fi

TMP="$(mktemp -d)" || exit 1
trap 'rm -rf "$TMP"' EXIT

# 用桩替换网络请求：读到 VERSION_TXT 的内容
cat > "$TMP/run.sh" <<'EOF'
nx_fetch_stdout() { cat "$VERSION_TXT" 2>/dev/null; }
EOF
printf '%s\n' "$FN" >> "$TMP/run.sh"

check() { # check <用例名> <base> <version_env> <version.txt 内容> <期望 BASE_URL> <期望 VER>
    name=$1; base=$2; venv=$3; vtxt=$4; want_url=$5; want_ver=$6
    printf '%s' "$vtxt" > "$TMP/version.txt"
    out="$(VERSION_TXT="$TMP/version.txt" NATPUNCH_BASE_URL="$base" NATPUNCH_VERSION="$venv" \
        sh -c ". '$TMP/run.sh'; nx_resolve_base_url; echo \"\$BASE_URL|\$VER\"")"
    url=${out%%|*}; ver=${out##*|}
    if [ "$url" = "$want_url" ] && [ "$ver" = "$want_ver" ]; then
        echo "  OK   $name -> $url ($ver)"
    else
        echo "  FAIL $name -> 实际 $url ($ver)  期望 $want_url ($want_ver)"
        FAIL=1
    fi
}

check "dl 根目录 + version.txt"      "https://p.example.com/static/dl" "" "v26.10.77" \
      "https://p.example.com/static/dl/v26.10.77" "v26.10.77"
check "dl 根目录 + 尾斜杠 + version.txt" "https://p.example.com/static/dl/" "" "v26.10.77" \
      "https://p.example.com/static/dl/v26.10.77" "v26.10.77"
check "版本目录（无 version.txt）"    "https://p.example.com/static/dl/v26.10.76" "" "" \
      "https://p.example.com/static/dl/v26.10.76" "本地目录"
check "显式 NATPUNCH_VERSION 优先"    "https://p.example.com/static/dl" "v26.10.70" "v26.10.77" \
      "https://p.example.com/static/dl" "v26.10.70"

echo
if [ $FAIL -eq 0 ]; then echo "自建源版本解析：全部通过"; else echo "自建源版本解析：有失败"; fi
exit $FAIL
