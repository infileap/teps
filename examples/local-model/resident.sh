#!/bin/sh
# 常驻的模型：加载一次，之后每问一次都是一秒级。
#
# 与 run.sh 的区别只有一处：run.sh 每问一次起一个 llama-cli（约 160 秒），
# 这里连一个已经在跑的 llama-server。
#
# 它**发布不成技能**，直到 `env.reaches` 出现。旧规则里 `env.network` 是一个布尔值，
# 它把「连远端主机」（不可复现，该拒）与「连本机套接字」（由自己的字节钉住，可复现）
# 合成了一件。这一轮跑的就是那条分界线。
#
# 不接进 tools/check_all.sh：它要起一个 35 秒加载的服务。
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
port="${LM_PORT:-18910}"
chain=$(mktemp -d)
kpid=""
spid=""

cleanup() {
    [ -n "$kpid" ] && kill "$kpid" 2>/dev/null || true
    [ -n "$spid" ] && kill "$spid" 2>/dev/null || true
    rm -rf "$chain"
}
trap cleanup EXIT

req() { printf '%s\n' "$1" | curl -sS --data-binary @- -H 'Content-Type: application/x-ndjson' "http://127.0.0.1:$port/k"; }

fails=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fails=$((fails + 1)); }

cd "$root"
MODEL="/mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf"
# ★ 这个哈希是 `sha256sum` 实测出来的，不是抄的。它是 `artifact` —— 把「本机」
# 从一个形容词变成一条可以复核的句子。
ARTIFACT="a96b16179dc6cc9afdf0cf7a96a80c199cbd00b9be207c3465be21cb721cca5e"

echo "—— 起一个常驻的模型（加载一次，约 35 秒）——"
.toolchain/llama/llama-server -m "$MODEL" --host 127.0.0.1 --port 18911 -c 4096 >"$chain/lms.log" 2>&1 &
spid=$!
t0=$(date +%s)
i=0
while [ "$i" -lt 300 ]; do
    i=$((i + 1))
    curl -sS -m 2 "http://127.0.0.1:18911/health" 2>/dev/null | grep -q ok && break
    sleep 1
done
t1=$(date +%s)
curl -sS -m 2 "http://127.0.0.1:18911/health" 2>/dev/null | grep -q ok \
    && ok "模型常驻，就绪用时 $((t1 - t0)) 秒" \
    || { bad "模型没起来"; tail -5 "$chain/lms.log"; exit 1; }

echo
echo "—— 建 ——"
tools/go build -o "$chain/teps-shell" ./adapters/shell
"$chain/teps-shell" -dir "$chain/log" -http "127.0.0.1:$port" -ms 900000 -tokens 100000 -money 1000 \
    >"$chain/out" 2>"$chain/err" &
kpid=$!
i=0
while [ "$i" -lt 100 ]; do
    i=$((i + 1))
    grep -q LISTEN "$chain/err" 2>/dev/null && break
    sleep 0.1
done
grep -q LISTEN "$chain/err" || { echo "内核没起来" >&2; cat "$chain/err" >&2; exit 1; }
ok "内核在 127.0.0.1:$port"

echo
echo "—— 发布：一份连本机套接字的技能 ——"
# ★ env.reaches 而不是 env.network。一个远端主机仍然被拒；一个回环套接字
# 加上「它背后是什么」的哈希，是可复现的。
body=$(python3 -c '
import json
man = {
    "skill_id": "local.ask.resident", "version": "1.0.0", "kind": "script",
    "verify_assert": ["exit == 0", "stdout contains \"PONG\""],
    "verify_refute": [{"input": {"refute": True}, "assert": "exit == 0"}],
    "budget": {"ms": 60000}, "evidence": "E0", "idempotent": True, "license": "MIT",
    "provenance": {"source": "handwritten"},
    "env": {
        "interpreter": "/usr/bin/python3",
        "reaches": [{"socket": "tcp:127.0.0.1:18911", "artifact": "'"$ARTIFACT"'"}],
    },
}
files = {"resident.py": open("examples/local-model/resident.py").read()}
print(json.dumps({"t": "skill.publish", "v": 1, "key": "lr1",
                  "body": {"manifest": man, "files": files}}))')
r=$(req "$body")
ref=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
[ -n "$ref" ] && ok "★ 发布成功：env.reaches 让一份连本机套接字的技能可以被发布" \
    || { bad "发布失败：$r"; exit 1; }

echo
echo "—— 跑两次，看常驻的代价 ——"
# ★ 判据不是「它答对了」——那 run.sh 已经验过。这里的判据是**快**：
# 每问一次起一个进程要 160 秒，而这里不该接近那个数。
run_once() {
    local key="$1" att="$2" t0 t1 out
    t0=$(date +%s)
    out=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"$key\",\"body\":{\"ref\":\"$ref\",\"attempt\":\"$att\",\"task_class\":\"task_exec\",\"input\":{\"prompt\":\"Reply with exactly: PONG\",\"n\":8},\"elevate\":true}}")
    t1=$(date +%s)
    echo "$((t1 - t0))"
    printf '%s' "$out" > "$chain/last"
}

s1=$(run_once lr2 lr-a)
printf '%s' "$(cat "$chain/last")" | grep -q '"outcome":"ok"' && ok "第一次判通过（${s1} 秒）" || bad "第一次没通过"
printf '%s' "$(cat "$chain/last")" | grep -q '"held":true' && ok "★ 必败用例真的被驳倒了" || bad "必败用例没被驳倒"
printf '%s' "$(cat "$chain/last")" | grep -q '"to":"E1"' && ok "★ 提级到 E1" || bad "没提级"

s2=$(run_once lr3 lr-b)
printf '%s' "$(cat "$chain/last")" | grep -q '"outcome":"ok"' && ok "第二次判通过（${s2} 秒）" || bad "第二次没通过"

echo
echo "—— 对照 ——"
printf '  每问一次起一个进程（run.sh）：约 160 秒\n'
printf '  常驻（本轮）：第一次 %s 秒，第二次 %s 秒\n' "$s1" "$s2"
if [ "$s1" -lt 60 ]; then
    ok "★ 常驻之后一次调用远低于 160 秒"
else
    bad "常驻没有变快：${s1} 秒"
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "全部通过：模型常驻，而 env.reaches 让这份技能可以被发布。"
else
    echo "$fails 条失败。"
    exit 1
fi
