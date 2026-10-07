#!/usr/bin/env bash
# 每一个进仓库的 .go 文件都必须是 gofmt 干净的。
#
# 理由不是审美。格式各异的代码会产生「没人打算做的改动」：一次 gofmt 跑下来，
# diff 里混着真实的逻辑改动和几百行重排，审查的人就再也看不出哪一行是真的。
# 这个仓库的第一条纪律是「哈希即证据」，而一个读不清的 diff 不是证据。
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
gofmt="$root/.toolchain/go/bin/gofmt"

if [ ! -x "$gofmt" ]; then
  echo "check_format：工具链不在（先跑 tools/install_go.sh）" >&2
  exit 2
fi

files=$(cd "$root" && "$gofmt" -l core organs store adapters 2>/dev/null)
if [ -n "$files" ]; then
  echo "以下文件不是 gofmt 干净的："
  printf '%s\n' "$files" | sed 's/^/  /'
  exit 1
fi
echo "gofmt：全部干净。"
