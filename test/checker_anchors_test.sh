#!/bin/sh
# 校验器信任锚对账：install.sh / install_server.sh / uninstall_client.sh 里内嵌的
# MSC_SHA256_{amd64,arm64,arm,mipsle}，必须与「当前工具链构建出的 minisign-check」逐一相符。
#
# 由来：这三个锚是安装脚本在目标机上校验下载到的静态签名校验器的信任根（防镜像替换）。
# 换 Go 工具链会改变产物哈希，锚点随之失效 —— 而 release 工作流里的守卫只在「打了 tag、
# 8 组构建都跑完」之后才发现，等于白烧一轮 CI（v26.10.49 就是这么废掉的），而且失败信息
# 出现在发布环节，很容易被误读成"包有问题"。本测试放进普通 master 推送就会跑的 check
# 里，把同一类问题拦在打 tag 之前。
#
# 复现性：构建参数必须与 release.yml 的 minisign-check 矩阵完全一致
# （-buildvcs=false -trimpath、CGO_ENABLED=0、arm 用 GOARM=7），否则哈希对不上、
# 本测试自己就会变成假警报。
set -e

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

SCRIPTS="install.sh install_server.sh uninstall_client.sh"
fail=0

for arch in amd64 arm64 arm mipsle; do
  goarm=""
  [ "$arch" = "arm" ] && goarm=7
  out="$TMP/minisign-check-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$goarm" \
    go build -buildvcs=false -trimpath -ldflags "-s -w -extldflags -static" \
    -o "$out" ./cmd/minisign-check
  actual=$(sha256sum "$out" | awk '{print $1}')

  archok=1
  for script in $SCRIPTS; do
    pinned=$(grep -E "^MSC_SHA256_${arch}=" "$script" | head -1 | cut -d'"' -f2)
    if [ -z "$pinned" ]; then
      echo "  ✗ $script 的 MSC_SHA256_${arch} 为空（该架构不启用内置校验器）"
      archok=0
    elif [ "$pinned" != "$actual" ]; then
      echo "  ✗ $script 的 MSC_SHA256_${arch} 与当前工具链的构建产物不符"
      echo "      锚点: $pinned"
      echo "      实际: $actual"
      archok=0
    fi
  done
  if [ "$archok" -eq 1 ]; then
    echo "  ✓ $arch 信任锚与构建产物一致"
  else
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "工具链（或 minisign-check 源码/构建参数）变过，锚点没跟上。"
  echo "把上面的「实际」值填进 $SCRIPTS 的 MSC_SHA256_* 后重试；"
  echo "如果这是刚换工具链的提交，锚点必须和工具链改动放在同一个提交里。"
  exit 1
fi

echo "校验器信任锚对账通过"
