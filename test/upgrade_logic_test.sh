#!/bin/sh
# 升级 / 安装脚本的自检：把纯逻辑函数抠出来跑一遍断言。
#
# 为什么需要它：这几段代码出错时**不会报错**，只会静默做错事 ——
#   · 版本挑错 → "更新"完反而降级，之后可能连不上服务端；
#   · 包校验被绕过 → 拿一个被替换过的 root 二进制去执行；
#   · 内嵌的 apply / guard 脚本有语法错 → 升级走到一半断在那儿，客户端停着；
#   · 备份清理漏一类 / 写错 rm 参数 → 备份无限堆在 /opt/natpunch 下，
#     或者反过来删掉升级唯一的 vkey 回滚点。
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

# 从脚本里原样抠出一个函数（第二个参数是文件名，默认 $SRC）
extract_fn() {
    awk -v fn="$1()" '
        index($0, fn " {") == 1 { grab = 1 }
        grab { print }
        grab && $0 == "}" { exit }
    ' "${2:-$SRC}"
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

# 三份实现必须一致。三个脚本各自独立下发（raw 一行 wget|sh），没法共享文件，
# 这份函数是靠复制维持的 —— 最典型的失手就是只改了一处（比如只把 install.sh 的
# sort -V 换掉），另外两个继续按字典序挑版本，静默降级。
ph_body() {
    awk '/^pick_highest_version\(\)/{g=1} g&&/awk .$/{a=1} a{print} a&&/^    .$/{exit}' "$1"
}
pb1="$(ph_body install.sh | md5sum | cut -d' ' -f1)"
pb2="$(ph_body install_server.sh | md5sum | cut -d' ' -f1)"
pb3="$(ph_body "$SRC" | md5sum | cut -d' ' -f1)"
if [ -n "$pb1" ] && [ "$pb1" = "$pb2" ] && [ "$pb2" = "$pb3" ]; then
    ok "三个脚本里的 pick_highest_version 代码体一致"
else
    bad "三个脚本里的 pick_highest_version 代码体不一致（install=${pb1:0:8} server=${pb2:0:8} client=${pb3:0:8}）—— 只改了一处？"
fi
if grep -q 'sort -V' install.sh install_server.sh "$SRC" 2>/dev/null | grep -v '^[^:]*:[0-9]*: *#' | grep -q .; then
    bad "还有脚本在用 sort -V 挑版本 —— busybox < 1.32 上会退化成字典序"
else
    ok "没有脚本再用 sort -V 挑版本（只剩注释里提到它）"
fi

echo "== 3. 两个脚本的基础语法 =="
for f in "$INSTALL" "$SRC"; do
    if sh -n "$f" 2>/tmp/_selfcheck_err; then ok "$f"; else bad "$f: $(cat /tmp/_selfcheck_err)"; fi
done
rm -f /tmp/_selfcheck_err

echo "== 4. 守护配置不能被改回会放弃重启的值（客户端与服务端都要查） =="
# procd 的 respawn <threshold> <timeout> <retry>：retry > 0 时崩够次数就永久放手。
# 两个脚本都必须查 —— 上一版只查了 install.sh，服务端 install_server.sh 里那句
# `respawn 3600 5 5` 就这么一路漏了过去，直到上真机看 procd 状态才发现。
# 服务端被守护放弃 = 所有客户端同时掉线且不会自愈，比客户端那边更严重。
SERVER_UNIT="install_server.sh"
for f in "$INSTALL" "$SERVER_UNIT"; do
    # 注意：install_server.sh 的 procd 段在 heredoc 里带缩进，所以这里不能锚 ^
    if grep -q 'procd_set_param respawn 3600 5 0' "$f"; then
        ok "$f 的 procd respawn retry 是 0（永不放弃）"
    else
        bad "$f 的 procd respawn retry 不是 0 —— 崩够次数后 procd 会彻底停止守护"
    fi
    if grep -qE '^[[:space:]]*StartLimitIntervalSec=0' "$f"; then
        ok "$f 的 systemd 关掉了启动频率限制"
    else
        bad "$f 的 systemd unit 缺 StartLimitIntervalSec=0 —— 多次重启后会被打成 failed 并停止重启"
    fi
done
# 反向断言：两个脚本里都不该再出现 retry>0 的 respawn 写法
if grep -qE 'procd_set_param respawn [0-9]+ [0-9]+ [1-9][0-9]*' "$INSTALL" "$SERVER_UNIT"; then
    bad "仍有 retry>0 的 respawn 写法 —— 那等于「崩够次数就永久放手」"
else
    ok "两个脚本都没有 retry>0 的 respawn 写法"
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

echo "== 6. 备份清理（漏一类就无限累积；清错则毁掉可回滚的那份） =="
# 背景：升级会在 /opt/natpunch 下留三类备份 ——
#   natpunch.bak.<时间戳>            旧二进制
#   conf.bak.<时间戳>/               整个配置目录（clients.json 里是全部客户端 vkey）
#   conf/natpunch.conf.bak.<时间戳>  改密时写的面板配置
# 清理函数漏掉哪一类，哪一类就永远堆着（conf.bak.* 目录就是这么堆起来的）；
# 而清错东西的后果更重 —— conf.bak.* 是升级唯一的 vkey 回滚点。
SERVER_SRC="install_server.sh"
cb="$(extract_fn clean_backups "$SERVER_SRC")"
co="$(extract_fn cleanup_old_backups "$SERVER_SRC")"
if [ -z "$cb" ]; then
    bad "没能从 $SERVER_SRC 抠出 clean_backups"
else
    info() { :; }   # clean_backups 会 info 一句，测试里不需要输出
    eval "$cb"
    T="$(mktemp -d)"
    mkdir -p "$T/conf"
    for ts in 20260101010101 20260202020202 20260303030303; do
        touch "$T/natpunch.bak.$ts" "$T/conf/natpunch.conf.bak.$ts"
        mkdir -p "$T/conf.bak.$ts" && touch "$T/conf.bak.$ts/clients.json"
    done
    touch "$T/natpunch" "$T/conf/natpunch.conf"      # 本体绝不能被当成备份删掉

    clean_backups "$T/natpunch" "二进制" >/dev/null 2>&1
    chk "$(ls -d "$T"/natpunch.bak.* 2>/dev/null)" "$T/natpunch.bak.20260303030303" \
        "二进制备份只留最新"

    clean_backups "$T/conf/natpunch.conf" "配置" >/dev/null 2>&1
    chk "$(ls -d "$T"/conf/natpunch.conf.bak.* 2>/dev/null)" "$T/conf/natpunch.conf.bak.20260303030303" \
        "面板配置备份只留最新"

    clean_backups "$T/conf" "配置目录" >/dev/null 2>&1
    chk "$(ls -d "$T"/conf.bak.* 2>/dev/null)" "$T/conf.bak.20260303030303" \
        "配置目录备份只留最新（rm -f 删不掉目录，且 2>/dev/null 会把失败藏起来）"

    chk "$([ -f "$T/natpunch" ] && echo alive)" "alive" "本体二进制没被误删"
    chk "$([ -f "$T/conf/natpunch.conf" ] && echo alive)" "alive" "本体配置没被误删"

    # PRE 为空时 "$PRE".bak.* 会退化成 .bak.*，那是在当前目录里删东西。
    # 必须放**两份**：只留最新的逻辑对单份本来就是「一个都不删」，
    # 只放一份的话这条断言是空的，测不出退化。
    mkdir -p "$T/cwd"
    touch "$T/cwd/.bak.20260101010101" "$T/cwd/.bak.20260202020202"
    ( cd "$T/cwd" && clean_backups "" "空" >/dev/null 2>&1 )
    chk "$(ls -A "$T/cwd" | sort | tr '\n' ' ')" ".bak.20260101010101 .bak.20260202020202 " \
        "PRE 为空时不误删当前目录"

    rm -rf "$T"
fi
case "$co" in
    *'clean_backups "$CONF_DIR"'*)
        ok "cleanup_old_backups 覆盖了 \$CONF_DIR（即 /opt/natpunch/conf.bak.*）" ;;
    *)
        bad "cleanup_old_backups 没清 \$CONF_DIR 的备份 —— /opt/natpunch/conf.bak.* 会无限累积" ;;
esac

echo "== 7. 升级流程必须刷新自启单元 =="
# 背景：unit 文件里的加固（procd 的 respawn retry、systemd 的 StartLimitIntervalSec）
# 只有在「写单元」那一步才生效。upgrade 原先只换二进制、完全不碰 unit —— 于是这些加固
# 永远到不了已安装的机器。真机上就是这么积的：服务端的 unit 从装完那天起就没刷新过，
# procd 的 retry 一直停在 5、systemd 一直缺 StartLimitIntervalSec=0，
# 而客户端那边更麻烦 —— 单元是 install.sh 写的，升级用的却是 uninstall_client.sh，
# 两个脚本互不知道对方写不写单元，那条链路没法在这里一并保住（本次只修服务端）。
upg="$(extract_fn upgrade "$SERVER_SRC")"
if [ -z "$upg" ]; then
    bad "没能从 $SERVER_SRC 抠出 upgrade()"
elif printf '%s\n' "$upg" | grep -q 'register_autostart'; then
    ok "install_server.sh 的 upgrade() 会刷新自启单元"
else
    bad "install_server.sh 的 upgrade() 不刷新自启单元 —— unit 里的加固到不了存量机器"
fi

echo "== 8. systemd 客户端单元不能把 ExecStart 包成 sh -c =="
# 背景：KillMode=process 只杀"主进程"。ExecStart 若是 /bin/sh -c '...'，systemd 眼里的
# 主进程就是那个 shell —— systemctl stop / restart 只杀 shell，真正的 natpunch-client
# 活下来被 init 收养（PPID=1），下次启动再多一个同 vkey 的客户端，一起连服务端抢同一条
# 隧道。真机上抓到过：restart 之后一个 29 分钟前的旧进程和新进程同时在连。
# 这两条是配套的，改一条必须看另一条。
# 目标形态：/bin/sh -c 'exec <客户端> ...' —— exec 让 systemd 的主进程就是客户端，
# 保留 shell 又让 ${TLS_FLAG} 里的多个 flag 能按空格展开。两者缺一不可，见单元里的长注释。
if grep -q "^ExecStart=/bin/sh -c 'exec /usr/bin/natpunch-client " "$INSTALL"; then
    ok "install.sh 的 ExecStart 是 sh -c 'exec 客户端' 形态"
else
    bad "install.sh 的 ExecStart 不是 sh -c 'exec ...' 形态 —— 要么漏杀客户端，要么多 flag 会被 systemd 当成一个参数"
fi
if grep -qE "^ExecStart=/usr/bin/natpunch-client |^ExecStart=/bin/sh -c '/usr/bin/natpunch-client " "$INSTALL"; then
    bad "install.sh 里还有旧的 ExecStart 形态（不 exec 会漏杀 / 无 shell 时变量不拆分）"
else
    ok "install.sh 里没有旧的 ExecStart 形态"
fi
# TLS_FLAG 必须允许多个 flag：面板的部署命令要带证书指纹
if grep -q 'TLS_FLAG 的每一段都必须是' "$INSTALL"; then
    ok "install.sh 逐段校验 TLS_FLAG（允许多 flag）"
else
    bad "install.sh 还只把 TLS_FLAG 当单段 —— 带指纹的部署命令会被它 die 掉"
fi
# 面板的部署按钮必须下发证书指纹（手工「TLS 命令」早就带了，这两个按钮一直漏）
if grep -q 'bridge_fingerprint}} -tls_fingerprint=' web/views/client/list.html 2>/dev/null; then
    ok "面板部署按钮下发了 -tls_fingerprint"
else
    bad "面板部署按钮没带 -tls_fingerprint —— 装出来的客户端只防被动窃听，日志会刷警告"
fi
# 存量机器靠更新路径迁移单元（不是所有人都会重装）
if grep -q 'pre_execform' "$SRC" && grep -q 'exec /usr/bin/natpunch-client' "$SRC"; then
    ok "uninstall_client.sh 会把存量单元迁移到 exec 形态"
else
    bad "uninstall_client.sh 不迁移存量单元的 ExecStart —— 更新后仍会漏杀客户端"
fi

echo "== 9. 面板终端 WS 必须关掉 beego 的模板渲染 =="
# 背景：beego 在 action 返回后判断 `!ResponseWriter.Started && Output.Status == 0` 才渲染
# 模板；WebSocket 的 hijack 不经过 beego 的 Write/WriteHeader，两个条件恒成立 →
# 它去渲染不存在的 views/terminalcontroller/ws.tpl → template.go:75 panic → 被 recover
# 抓住打一串 [C] 堆栈。成功的终端会话结束时同样走这个判断，于是"每开一次终端 panic 一次"。
# 真机实测：升级前当天 24 条 [C] 全来自 /terminal/ws，而当天真实 [E] 是 0 条 ——
# 这些堆栈一直在掩盖真错误。
# ws_controllers 那条 Go 测试（web/controllers/terminal_test.go）钉的是"闸门本身"，
# 这里钉的是"Ws() 有没有用这个闸门"——两者缺一不可。
WS_SRC="web/controllers/terminal.go"
if [ ! -f "$WS_SRC" ]; then
    bad "找不到 $WS_SRC"
elif awk '/func \(s \*TerminalController\) Ws\(\) \{/{g=1} g&&/^}/{exit} g' "$WS_SRC" | grep -q 's.EnableRender = false'; then
    ok "TerminalController.Ws() 里关了模板渲染"
else
    bad "Ws() 里没有 s.EnableRender = false —— 面板终端会重新变成每开一次 panic 一次"
fi

echo
if [ "$FAIL" -eq 1 ]; then
    echo "自检失败"
    exit 1
fi
echo "自检全部通过"
