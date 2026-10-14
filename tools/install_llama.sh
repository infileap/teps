#!/bin/sh
# 重建项目内自包含的本地模型运行时（llama.cpp）。
#
# 为什么需要它：本项目的设计规则说**技能不许在运行时取网**
# （`store/manifest.go`：`env.network must be false`，理由是**可复现性**）。
# 那条规则有一个直接的推论，我把它写进了 README：
#
#     **在这个系统里，一个模型是一个本地程序，不是一次 API 调用。**
#
# 推论要能落地，就得有一个本地程序可跑。这个脚本装的就是那个程序。
#
# 版本与哈希是钉死的：运行时是构建输入，不是"随手装个最新的"。
# 这与 `tools/install_go.sh` 是同一条纪律，理由也一样 ——
# 一个「按最新版跑」的技能，它的内容哈希声称的东西与它实际跑的东西会分家。
set -eu

TAG="b11617"
SHA256="bc9c8bd3488e5778137c2289d951af60a94077a6bea51b7e30461033b084e07f"
FILE="llama-$TAG-bin-ubuntu-x64.tar.gz"
URL="https://github.com/ggml-org/llama.cpp/releases/download/$TAG/$FILE"

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tc="$root/.toolchain"
dst="$tc/llama"
tmp="${TMPDIR:-/tmp}/$FILE"

mkdir -p "$tc"

if [ -x "$dst/llama-cli" ]; then
    echo "已存在 llama.cpp $TAG，跳过。"
else
    echo "下载 llama.cpp $TAG ..."
    rm -f "$tmp"
    # 分块续传：网络是间歇性的，一次长连接会被掐断。
    i=0
    while [ "$i" -lt 40 ]; do
        i=$((i + 1))
        cur=$(sha256sum "$tmp" 2>/dev/null | cut -d' ' -f1)
        [ "$cur" = "$SHA256" ] && break
        curl -sS -L -m 300 -C - -o "$tmp" "$URL" 2>/dev/null || true
        printf '  attempt %2d: %s bytes\n' "$i" "$(stat -c%s "$tmp" 2>/dev/null || echo 0)"
    done

    got=$(sha256sum "$tmp" 2>/dev/null | cut -d' ' -f1)
    if [ "$got" != "$SHA256" ]; then
        echo "sha256 不匹配：要 $SHA256，得 $got" >&2
        exit 1
    fi

    echo "校验通过，解压 ..."
    rm -rf "$dst"
    mkdir -p "$dst"
    tar -C "$dst" -xzf "$tmp" --strip-components=1
    rm -f "$tmp"
fi

# 这一版发布包把可执行文件放在顶层；换个发布方式就找不到，所以直接说清。
if [ ! -x "$dst/llama-cli" ]; then
    echo "解压后没有 $dst/llama-cli —— 这个 tag 的目录形状与脚本假设的不同。" >&2
    ls "$dst" >&2
    exit 1
fi

echo "完成：$("$dst/llama-cli" --version 2>&1 | head -1)"
echo "用 tools/llama 调用。"
echo
echo "模型不在这个仓库里（它有几个 GB，而且是数据不是代码）。"
echo "本机已有的那个：/mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf"
echo "要跑它：tools/llama -m /mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf -p 'hi' -n 32"
