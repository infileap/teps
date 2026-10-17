#!/usr/bin/env bash
# 跑 mutations/ 里的变异脚本，一条一条报结果。
#
# 为什么需要它：README 里有 56 处「变异 N/N」的主张，而在此之前**没有任何
# 一条命令能把它们跑一遍** —— 证据在仓库里（Round 91 把 mutations/ 追进了 git），
# 但「在仓库里」与「跑得起来」是两件事。
#
# 用法：
#   tools/run_mutations.sh              # 全跑（**慢**：实测一条约 30 秒）
#   tools/run_mutations.sh --list       # 只列，不跑
#   tools/run_mutations.sh --only mut_channels.py
#   tools/run_mutations.sh --timeout 120
#
# ★ 它**不进 check_all.sh**：79 条 × 约 30 秒 ≈ 40 分钟，那是另一件事的时间尺度。
# 进 check_all 的是「每一条都解析得了」（秒级），而**真的跑一遍**是这一条命令。
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
dir="$root/mutations"
only=""
limit="${MUT_TIMEOUT:-900}"

while [ $# -gt 0 ]; do
  case "$1" in
    --list) list_only=1 ;;
    --only) only="$2"; shift ;;
    --timeout) limit="$2"; shift ;;
    *) echo "用法：run_mutations.sh [--list] [--only <脚本>] [--timeout <秒>]" >&2; exit 2 ;;
  esac
  shift
done

if [ ! -d "$dir" ]; then
  echo "run_mutations：找不到 mutations/" >&2; exit 2
fi

scripts=$(cd "$dir" && ls mut_*.py 2>/dev/null | sort)
if [ -z "$scripts" ]; then
  echo "run_mutations：mutations/ 里一个 mut_*.py 都没有" >&2; exit 2
fi

if [ "${list_only:-0}" = "1" ]; then
  printf '%s\n' "$scripts"
  exit 0
fi

pass=0; fail=0
for s in $scripts; do
  if [ -n "$only" ] && [ "$s" != "$only" ]; then continue; fi
  out=$(cd "$root" && timeout "$limit" python3 "mutations/$s" 2>&1)
  rc=$?
  if [ $rc -eq 0 ]; then
    pass=$((pass + 1))
    printf '  ok    %-34s %s\n' "$s" "$(printf '%s' "$out" | grep -E '达到预期|被抓住' | tail -1)"
  else
    fail=$((fail + 1))
    printf '  FAIL  %-34s rc=%s\n' "$s" "$rc"
    printf '%s\n' "$out" | sed 's/^/        /' | tail -3
  fi
done

echo
if [ $fail -eq 0 ]; then
  echo "变异全部达到预期：$pass 条。"
  exit 0
fi
echo "$fail 条没有达到预期（$pass 条通过）。"
exit 1
