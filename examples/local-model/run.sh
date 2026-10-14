#!/bin/sh
# 一份真的会说话的技能，端到端跑一遍。
#
# 它不接进 `tools/check_all.sh`，因为它**慢**：本机这个 7B 模型一次调用约 170 秒，
# 其中约 93 秒是加载。一条要跑三分钟的守卫会被从默认路径里拿掉，然后就没有人跑它了
# ——所以它是一条**明确按需跑**的演示，不是守卫。
#
# 前提：tools/install_llama.sh 已经跑过，且 /mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf 在。
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
port="${LM_PORT:-18901}"
chain=$(mktemp -d)
pid=""

cleanup() {
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
    rm -rf "$chain"
}
trap cleanup EXIT

req() { printf '%s\n' "$1" | curl -sS --data-binary @- -H 'Content-Type: application/x-ndjson' "http://127.0.0.1:$port/k"; }

fails=0
ok()   { printf '  ok    %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; fails=$((fails + 1)); }

echo "—— 建（一个只给这一次演示用的内核）——"
cd "$root"
tools/go build -o "$chain/teps-shell" ./adapters/shell
# ★ 预算必须比一次模型调用大：这是**实测**出来的数（约 170 秒），不是猜的。
# 声明一个装不下它的预算，会让这次执行在加载到一半时被杀掉 ——
# 而「被杀掉」与「答错了」在链上是两个不同的判定，这一点正是这个项目在守的东西。
"$chain/teps-shell" -dir "$chain/log" -http "127.0.0.1:$port" -ms 900000 -tokens 100000 -money 1000 \
    >"$chain/out" 2>"$chain/err" &
pid=$!
i=0
while [ "$i" -lt 100 ]; do
    i=$((i + 1))
    grep -q LISTEN "$chain/err" 2>/dev/null && break
    sleep 0.1
done
grep -q LISTEN "$chain/err" || { echo "内核没起来" >&2; cat "$chain/err" >&2; exit 1; }
ok "内核在 127.0.0.1:$port"

echo
echo "—— 发布：一份会调本地模型的技能 ——"
# ★ 载荷的字节就是技能的身份，所以模型与运行时的路径钉在字节里。
# 换一台机器这份载荷就不可复现 —— 那是实话，而它由哈希链记着，不由注释承诺着。
# ★ 帧是一行一条。用 printf 拼一个多行的 JSON 会把它切成好几条帧，
# 而每一条都单独看起来像坏 JSON —— 报的是「帧不是 JSON 对象」，
# 而真正的问题是**那根本不是一条帧**。
body=$(python3 -c '
import json
man = {
    "skill_id": "local.ask", "version": "1.0.0", "kind": "script",
    "verify_assert": ["exit == 0", "stdout contains \"PONG\""],
    "verify_refute": [{"input": {"refute": True}, "assert": "exit == 0"}],
    "budget": {"ms": 400000}, "evidence": "E0", "idempotent": True, "license": "MIT",
    "provenance": {"source": "handwritten"},
    "env": {"interpreter": "/usr/bin/python3"},
}
files = {"main.py": open("examples/local-model/main.py").read()}
print(json.dumps({"t": "skill.publish", "v": 1, "key": "lm1",
                  "body": {"manifest": man, "files": files}}))')

r=$(req "$body")
ref=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
[ -n "$ref" ] && ok "发布成功，ref=${ref%${ref#??????}}…" || { bad "发布失败：$r"; exit 1; }
printf '%s' "$r" | grep -q '"evidence":"E0"' && ok "入账是 E0（它还没跑过）" || bad "入账等级不对"

echo
echo "—— 跑：真的问一次模型（约三分钟）——"
t0=$(date +%s)
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"lm2\",\"body\":{\"ref\":\"$ref\",\"attempt\":\"lm-run\",\"task_class\":\"task_exec\",\"input\":{\"prompt\":\"Reply with exactly: PONG\",\"n\":8},\"elevate\":true}}")
t1=$(date +%s)
printf '%s' "$r" | grep -q '"outcome":"ok"' && ok "核验器判通过（用时 $((t1 - t0)) 秒）" || bad "没通过：$r"
printf '%s' "$r" | grep -q '"verified_by":"verify_assert"' && ok "判定者是核验器，不是执行器" || bad "判定者不对"
printf '%s' "$r" | grep -q '"held":true' && ok "★ 必败用例真的被驳倒了（它有能力失败）" || bad "必败用例没被驳倒"
printf '%s' "$r" | grep -q '"to":"E1"' && ok "★ 提级到 E1：双边证据齐了" || bad "没提级：$r"

echo
echo "—— 模型说了什么 ——"
# ★ 响应是**包着的**：`{"t":…,"v":…,"seq":…,"rev":…,"body":{…}}`。
# 第一版直接读 `d["observed"]`，于是拿到 `null` 却印成空串 ——
# 一条**看起来像「模型什么都没说」**的输出，而真正的原因是读错了层。
printf '%s' "$r" | python3 -c '
import json,sys
d = json.load(sys.stdin)
obs = d["body"].get("observed") or {}
print("  exit:", obs.get("exit"), " latency_ms:", obs.get("latency_ms"))
print("  stdout:", obs.get("stdout", "").strip()[:200])
'

echo
if [ "$fails" -eq 0 ]; then
    echo "全部通过：一份真的会说话的技能，端到端。"
else
    echo "$fails 条失败。"
    exit 1
fi
