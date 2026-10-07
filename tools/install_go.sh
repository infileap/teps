#!/bin/sh
# 重建项目内自包含 Go 工具链。
#
# 为什么不用系统包管理器：本机 sudo 被 no_new_privs 挡住（"no new privileges" flag is set），
# /usr/local 只读，/var/cache/apt 属主是 nobody:nogroup —— 系统级安装这条路是死的。
# 所以工具链落在项目内，且可整体搬走。
#
# 版本与哈希是钉死的：工具链是构建输入，不是"随手装个最新的"。
set -eu

VERSION="go1.27.1"
SHA256="63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"
FILE="$VERSION.linux-amd64.tar.gz"

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tc="$root/.toolchain"
tmp="${TMPDIR:-/tmp}/$FILE"

# 直连镜像优先：go.dev/dl 会 302 到 dl.google.com，中间那一跳在长连接上不可靠。
MIRRORS="https://dl.google.com/go https://mirrors.aliyun.com/golang"

mkdir -p "$tc"

if [ -x "$tc/go/bin/go" ] && [ "$("$tc/go/bin/go" version 2>/dev/null | awk '{print $3}')" = "$VERSION" ]; then
    echo "已存在 $VERSION，跳过。"
else
    echo "下载 $VERSION ..."
    ok=0
    for base in $MIRRORS; do
        rm -f "$tmp"
        # 分块续传：网络是间歇性的，一次长连接会被掐断。
        i=0
        while [ "$i" -lt 30 ]; do
            i=$((i + 1))
            cur=$(sha256sum "$tmp" 2>/dev/null | cut -d' ' -f1)
            [ "$cur" = "$SHA256" ] && break
            curl -sS -L -m 150 -C - -o "$tmp" "$base/$FILE" 2>/dev/null || true
            printf '  %s attempt %2d: %s bytes\n' "$base" "$i" "$(stat -c%s "$tmp" 2>/dev/null || echo 0)"
        done
        if [ "$(sha256sum "$tmp" 2>/dev/null | cut -d' ' -f1)" = "$SHA256" ]; then ok=1; break; fi
    done

    [ "$ok" = 1 ] || { echo "所有镜像都失败，或 sha256 不匹配。" >&2; exit 1; }

    echo "校验通过，解压 ..."
    rm -rf "$tc/go"
    tar -C "$tc" -xzf "$tmp"
    rm -f "$tmp"
fi

# $HOME 只读，GOPATH/GOCACHE 必须落在项目内，否则 go build 直接失败。
mkdir -p "$tc/gopath" "$tc/gocache"

echo "完成：$("$tc/go/bin/go" version)"
echo "用 tools/go 调用（它负责设好 GOROOT/GOPATH/GOCACHE）。"
