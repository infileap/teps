#!/usr/bin/env bash
# 成对负控的离线跑法。
#
# 它只用 curl 与 python3 跟内核说话 —— 没有 SDK、没有语言运行时、没有网络。
# 内核自己是一个二进制，由这个脚本现建（`tools/go` 包装器把整个工具链钉在仓库里）。
#
# 每一对跑两遍：
#   must_pass 那一半必须给出它声明的判定（通常 ok）
#   must_fail 那一半必须给出它声明的**另一种**判定
#
# ★ 一条**不分辨**的负控不是负控：如果必败那一半给出的判定与必过那一半一样，
# 那么这个技能没有被证明能失败，它只是被证明了两遍能通过。
# 这一条会被判成红，因为那正是这一整套东西存在的理由。
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
cd "$ROOT"

PORT=${NC_PORT:-18899}
URL="http://127.0.0.1:$PORT/k"
CHAIN=$(mktemp -d "${TMPDIR:-/tmp}/teps-nc-XXXXXX")
PID=""
fails=0
ok=0

cleanup() {
  [ -n "$PID" ] && kill "$PID" 2>/dev/null
  rm -rf "$CHAIN"
}
trap cleanup EXIT

req() { printf '%s\n' "$1" | curl -sS --data-binary @- -H 'Content-Type: application/x-ndjson' "$URL"; }
jq_() { python3 -c "import json,sys; d=json.load(sys.stdin); $1"; }

echo "—— 建内核 ——"
export PATH="$ROOT/tools:$PATH"
mkdir -p .out
tools/go build -o .out/teps-shell ./adapters/shell || { echo "  建不起来"; exit 1; }
.out/teps-shell -dir "$CHAIN" -http "127.0.0.1:$PORT" >/dev/null 2>"$CHAIN/err" &
PID=$!
for _ in $(seq 1 50); do grep -q '^LISTEN' "$CHAIN/err" 2>/dev/null && break; sleep 0.1; done
if ! grep -q '^LISTEN' "$CHAIN/err" 2>/dev/null; then echo "  内核没起来"; cat "$CHAIN/err"; exit 1; fi
echo "  ok   内核在 $URL"

echo
echo "—— 成对的负控 ——"
CASES=$(python3 -c "
import json
d=json.load(open('$HERE/cases.json'))
for c in d['cases']: print(c['id'])
")
for id in $CASES; do
  # ★ 落到**文件**再读，不要 source。
  # 载荷里有 `in=$(cat)`，而 `MAN="…$(cat)…"` 在双引号里会**真的执行**那个命令替换——
  # 于是脚本读到的是一份被替换过的 JSON。（同类的坑以前栽过：commit message 里的反引号。）
  python3 - "$HERE/cases.json" "$id" "$CHAIN" <<'PY'
import json,sys,os
d=json.load(open(sys.argv[1])); c=[x for x in d['cases'] if x['id']==sys.argv[2]][0]
out=sys.argv[3]
man={"skill_id":c["skill_id"],"version":"1.0.0","kind":"script",
     "verify_assert":[c["must_pass"]["assert"]],
     "verify_refute":[{"input":c["must_fail"]["input"],"assert":c["must_fail"]["assert"]}],
     "budget":{"ms":c["budget_ms"]},"evidence":"E0","idempotent":True,"license":"MIT",
     "provenance":{"source":"handwritten"},"env":{"interpreter":c["interpreter"]}}
w=lambda n,v: open(os.path.join(out,n),"w",encoding="utf-8").write(v)
w("man.json", json.dumps({"manifest":man,"files":c["files"]}))
w("pass_in.json", json.dumps(c["must_pass"]["input"]))
w("fail_in.json", json.dumps(c["must_fail"]["input"]))
w("pass_exp", c["must_pass"]["expect"])
w("fail_exp", c["must_fail"]["expect"])
w("catches", c["catches"])
PY
  MAN=$(cat "$CHAIN/man.json")
  PASS_IN=$(cat "$CHAIN/pass_in.json")
  FAIL_IN=$(cat "$CHAIN/fail_in.json")
  PASS_EXP=$(cat "$CHAIN/pass_exp")
  FAIL_EXP=$(cat "$CHAIN/fail_exp")
  CATCHES=$(cat "$CHAIN/catches")
  r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"nc-$id\",\"body\":$MAN}")
  ref=$(printf '%s' "$r" | jq_ "print(d.get('body',{}).get('ref',''))")
  if [ -z "$ref" ]; then
    printf '  FAIL  %-28s 注册不上：%s\n' "$id" "$r"; fails=$((fails + 1)); continue
  fi
  # ★ 负控**不是**「换个输入再跑一次看 outcome」。
  # 它是 `verify_refute` 里的一条声明：喂这个输入，这条断言**不应该成立**。
  # 引擎把结果放在 `elevation.refutations[]` 里，字段是 `held` 与 `verdict`。
  # 只读 outcome 会得到一个与必过那一半一样的数 —— 那正是「存在性判据冒充一致性判据」。
  p=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"nc-$id\",\"body\":{\"ref\":\"$ref\",\"attempt\":\"nc-$id\",\"task_class\":\"task_exec\",\"input\":$PASS_IN,\"elevate\":true}}")
  po=$(printf '%s' "$p" | jq_ "print(d.get('body',{}).get('outcome',''))")
  fv=$(printf '%s' "$p" | jq_ "r=d.get('body',{}).get('elevation',{}).get('refutations') or [{}]; print(r[0].get('verdict',''))")
  fh=$(printf '%s' "$p" | jq_ "r=d.get('body',{}).get('elevation',{}).get('refutations') or [{}]; print(str(r[0].get('held','')).lower())")
  want_held=false
  [ "$FAIL_EXP" = "failed" ] && want_held=true
  if [ "$po" = "$PASS_EXP" ] && [ "$fv" = "$FAIL_EXP" ] && [ "$fh" = "$want_held" ]; then
    ok=$((ok + 1))
    printf '  ok    %-28s 必过=%s 必败=%s(held=%s)\n' "$id" "$po" "$fv" "$fh"
  else
    fails=$((fails + 1))
    printf '  FAIL  %-28s 必过=%s（要 %s）必败=%s(held=%s)（要 %s/held=%s）\n' \
      "$id" "$po" "$PASS_EXP" "$fv" "$fh" "$FAIL_EXP" "$want_held"
    if [ "$fv" = "$po" ]; then
      printf '        ★ 这一对**不分辨**：两半给出同一个判定，所以它证明的是「能通过」两遍，不是「能失败」。\n'
    fi
  fi
  printf '        %s\n' "$CATCHES"
done

echo
if [ "$fails" -eq 0 ]; then
  printf '全部通过：%d 对负控，每对两半都给出了各自的判定。\n' "$ok"
  exit 0
fi
printf '%d 对里有 %d 对不合格。\n' "$((ok + fails))" "$fails"
exit 1
