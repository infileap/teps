#!/usr/bin/env bash
# 跑全部守卫，一条命令。
#
# 为什么需要这个文件：守卫此前只有一份**清单**（README 里的九条命令），
# 而清单要靠人一条一条敲。少敲一条，那台守卫就静悄悄地不再守了——
# 而它的输出看起来和「全部通过」一样，因为它根本没有输出。
#
# 这正是本项目反复抓到的形状：**一个只在有人记得时才跑的检查，
# 报不出「没有人记得」。** 所以入口要有一处，且它必须能失败。
set -uo pipefail

cd "$(dirname "$0")/.."

fails=0
ran=0

run() { # run <名字> <命令...>
  name="$1"; shift
  ran=$((ran + 1))
  out=$("$@" 2>&1)
  rc=$?
  last=$(printf '%s' "$out" | tail -1)
  if [ "$rc" -eq 0 ]; then
    printf '  ok    %-22s %s\n' "$name" "$last"
  else
    # 失败时**最后一行**经常是空的（编译失败就是这样）。一条没有内容的 FAIL
    # 与一条 ok 一样不可读，所以把非空的前几行也打出来。
    printf '  FAIL  %-22s %s\n' "$name" "$last"
    printf '%s\n' "$out" | grep -v '^[[:space:]]*$' | head -6 | sed 's/^/        /'
    fails=$((fails + 1))
  fi
}

echo "—— 守卫 ——"
run check_format.sh   bash tools/check_format.sh
run check_kernel.py   python3 tools/check_kernel.py
run check_contract.py python3 tools/check_contract.py
run check_adapters.py python3 tools/check_adapters.py
run check_frames.py   python3 tools/check_frames.py
run check_organs.py   python3 tools/check_organs.py
run check_docs.py     python3 tools/check_docs.py
run check_assertions.py python3 tools/check_assertions.py
run check_nomodel.py  python3 tools/check_nomodel.py
run check_clients.py python3 tools/check_clients.py
run check_examples.py python3 tools/check_examples.py

echo
echo "—— 测试 ——"
# ★ 这一行第一版把 go test 的输出整个丢进了 /dev/null —— 于是失败时它只打印
# 一条空的 FAIL，**报了一个结论却不给证据**。现在失败时把 go test 自己的话带出来。
run "go test ./..." bash -c '
  export PATH="$PWD/tools:$PATH"
  out=$(go test ./... -count=1 2>&1) || { printf "%s\n" "$out" | grep -v "^ok\|no test files" | head -8; exit 1; }
  n=$(go test ./... -count=1 -v 2>&1 | grep -c "^=== RUN")
  printf "个测试 %s" "$n"
'

echo
# ★ 自检：守卫必须能失败。
# 一台永远绿的守卫与一台从来没跑过的守卫，输出一模一样。
# ★ 每一台**带自检**的守卫各检一次，不是只检一台：
# 一个只验第一台的自检循环，会让第二台的自检永远不被跑 ——
# 而它坏掉的样子与「通过」一模一样（它什么都不打印）。
selfcheck() { # selfcheck <名字> <自检命令>
  if eval "$2" >/dev/null 2>&1; then
    printf '  FAIL  %-22s 注入违规之后它仍然说通过 —— 这台守卫是绿的机器\n' "$1"
    fails=$((fails + 1))
  else
    printf '  ok    %-22s 注入违规之后它红了\n' "$1"
  fi
}
selfcheck "守卫自检 nomodel"  "TEPS_NOMODEL_SELF_TEST=1 python3 tools/check_nomodel.py"
selfcheck "守卫自检 clients" "TEPS_CLIENTS_SELF_TEST=1 python3 tools/check_clients.py"
selfcheck "守卫自检 examples" "TEPS_EXAMPLES_SELF_TEST=1 python3 tools/check_examples.py"
selfcheck "守卫自检 收件箱分账" "TEPS_EF_LEDGER_SELF_TEST=1 python3 tools/ef_ledger.py"
selfcheck "守卫自检 docs"     "TEPS_DOCS_SELF_TEST=1 python3 tools/check_docs.py"
selfcheck "守卫自检 organs"   "TEPS_ORGANS_SELF_TEST=1 python3 tools/check_organs.py"

echo
if [ "$fails" -eq 0 ]; then
  printf '全部通过：%d 台守卫 + 测试 + 守卫自检。\n' "$((ran - 1))"
else
  printf '%d 处失败（跑了 %d 项）。\n' "$fails" "$ran"
fi
exit $((fails > 0))
