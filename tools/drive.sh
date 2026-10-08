#!/usr/bin/env bash
# 自由判据的可执行形式：除了 curl，什么都不许用。
#
# 判据是「删掉一切宿主，内核仍然完整可用」。如果哪天这个脚本需要第二件工具，
# 判据就已经丢了 —— 这个脚本本身就是那条判据。
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
bin="$tmp/shell"
chain="$tmp/chain"
fails=0
pid=""

cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  rm -rf "$tmp"
}
trap cleanup EXIT

echo "构建 shell 适配器"
( cd "$root" && PATH="$root/tools:$PATH" go build -o "$bin" ./adapters/shell ) || exit 1

start() { # start <chain-dir> [extra flags...]
  "$bin" -dir "$1" -http 127.0.0.1:0 "${@:2}" 2>"$tmp/listen" &
  pid=$!
  local addr=""
  for _ in $(seq 1 200); do
    addr=$(sed -n 's/^LISTEN //p' "$tmp/listen" 2>/dev/null | head -1)
    [ -n "$addr" ] && break
    sleep 0.05
  done
  if [ -z "$addr" ]; then
    echo "适配器没有报告监听地址："
    cat "$tmp/listen"
    exit 1
  fi
  url="http://$addr/k"
}

req() { # req <frame-json>
  printf '%s\n' "$1" | curl -sS --data-binary @- \
    -H 'Content-Type: application/x-ndjson' "$url"
}

expect() { # expect <label> <response> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf '  ok    %s\n' "$1"
  else
    printf '  FAIL  %s\n        want: %s\n        got:  %s\n' "$1" "$3" "$2"
    fails=$((fails + 1))
  fi
}

head1=""

echo
echo "—— hello：服务端自报 epoch ——"
start "$chain"
r=$(req '{"t":"hello","v":1}')
expect "应答类型是 welcome" "$r" '"t":"welcome"'
expect "epoch 从 1 开始" "$r" '"epoch":1'

echo
echo "—— put：位置由服务端分配 ——"
r=$(req '{"t":"put","v":1,"key":"k1","body":{"task":"a"}}')
expect "第一条记录 seq=1" "$r" '"seq":1'
head1=$(printf '%s' "$r" | sed -n 's/.*"head":"\([0-9a-f]\{64\}\)".*/\1/p')
if [ -n "$head1" ]; then
  printf '  ok    服务端分配了 64 位十六进制链头\n'
else
  printf '  FAIL  没有拿到链头\n'
  fails=$((fails + 1))
fi

echo
echo "—— 同键重发：幂等，不追加 ——"
r=$(req '{"t":"put","v":1,"key":"k1","body":{"task":"a"}}')
expect "仍是 seq=1" "$r" '"seq":1'
expect "链头未变" "$r" "\"head\":\"$head1\""

echo
echo "—— client 自供服务端字段：必须拒绝 ——"
r=$(req '{"t":"put","v":1,"key":"k2","seq":99,"body":{}}')
expect "seq → client_authority" "$r" 'client_authority'
r=$(req '{"t":"put","v":1,"key":"k3","head":"deadbeef","body":{}}')
expect "head → client_authority" "$r" 'client_authority'
r=$(req '{"t":"put","v":1,"key":"k4","rev":7,"body":{}}')
expect "rev → client_authority" "$r" 'client_authority'

echo
echo "—— 空幂等键：拒绝 ——"
r=$(req '{"t":"put","v":1,"key":"","body":{}}')
expect "bad_request" "$r" 'bad_request'

echo
echo "—— status：被拒的请求一条都没写进去 ——"
r=$(req '{"t":"status","v":1}')
expect "seq 仍是 1" "$r" '"seq":1'
expect "链头仍是同一条" "$r" "\"head\":\"$head1\""

echo
echo "—— 预算：内核唯一的有状态之物 ——"
r=$(req '{"t":"spend","v":1,"body":{"ms":10,"tokens":10,"money":10}}')
expect "扣款成功并回报余额" "$r" '"left"'
r=$(req '{"t":"spend","v":1,"body":{"ms":999999,"tokens":0,"money":0}}')
expect "超额 → budget_exhausted" "$r" 'budget_exhausted'

echo
echo "—— 未知类型：拒绝，而不是断开 ——"
r=$(req '{"t":"nonsense","v":1}')
expect "bad_request" "$r" 'bad_request'
r=$(req '{"t":"status","v":1}')
expect "连接仍然可用" "$r" '"seq":1'

echo
echo "—— 重启：epoch 增长，位置与幂等键都要活着 ——"
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
start "$chain"
r=$(req '{"t":"hello","v":1}')
expect "epoch 增长到 2" "$r" '"epoch":2'
r=$(req '{"t":"status","v":1}')
expect "seq 仍是 1" "$r" '"seq":1'
expect "链头跨重启一致" "$r" "\"head\":\"$head1\""
r=$(req '{"t":"put","v":1,"key":"k1","body":{"task":"a"}}')
expect "同键跨重启仍幂等" "$r" "\"head\":\"$head1\""

echo
echo "—— 会话：状态机记在链上，不在适配器内存里 ——"
r=$(req '{"t":"session.open","v":1,"key":"s-open","body":{"session":"s1"}}')
expect "新会话处于 ready" "$r" '"state":"ready"'
r=$(req '{"t":"session.status","v":1,"body":{"session":"nope"}}')
expect "不存在的会话 → no_session" "$r" 'no_session'
r=$(req '{"t":"session.event","v":1,"key":"s1-open","body":{"session":"s1","event":"open"}}')
expect "ready --open--> routing" "$r" '"state":"routing"'

echo
echo "—— 非法迁移：拒绝，且一个字节都不写进链 ——"
before=$(req '{"t":"status","v":1}')
r=$(req '{"t":"session.event","v":1,"key":"s1-pass","body":{"session":"s1","event":"pass"}}')
expect "routing 不接受 pass" "$r" 'bad_request'
after=$(req '{"t":"status","v":1}')
if [ "$before" = "$after" ]; then
  printf '  ok    被拒的迁移没有动链\n'
else
  printf '  FAIL  被拒的迁移动了链\n        before: %s\n        after:  %s\n' "$before" "$after"
  fails=$((fails + 1))
fi

r=$(req '{"t":"session.event","v":1,"key":"s1-route","body":{"session":"s1","event":"route","strategy":"alpha"}}')
expect "routing --route--> executing" "$r" '"state":"executing"'
r=$(req '{"t":"session.event","v":1,"key":"s1-finish","body":{"session":"s1","event":"finish"}}')
expect "executing --finish--> verifying" "$r" '"state":"verifying"'
r=$(req '{"t":"session.event","v":1,"key":"s1-done","body":{"session":"s1","event":"pass"}}')
expect "verifying --pass--> done" "$r" '"state":"done"'
r=$(req '{"t":"session.status","v":1,"body":{"session":"s1"}}')
expect "轨迹完整" "$r" '"ready","routing","executing","verifying","done"'
expect "终态不可再走" "$r" '"terminal":true'

echo
echo "—— 停滞守卫：连续 3 步低增益，纯计数 ——"
req '{"t":"session.open","v":1,"key":"s2-open","body":{"session":"s2"}}' >/dev/null
for i in 1 2; do
  r=$(req "{\"t\":\"session.observe\",\"v\":1,\"key\":\"s2-o$i\",\"body\":{\"session\":\"s2\",\"gain\":0.01,\"action\":\"h$i\"}}")
done
expect "两步还不算停滞" "$r" '"stalled":false'
r=$(req '{"t":"session.observe","v":1,"key":"s2-o3","body":{"session":"s2","gain":0.01,"action":"h3"}}')
expect "第三步判定停滞" "$r" '"stalled":true'

echo
echo "—— 同策略重试：只允许一次，第二次必须换 ——"
req '{"t":"session.open","v":1,"key":"s3-open","body":{"session":"s3"}}' >/dev/null
req '{"t":"session.event","v":1,"key":"s3-e1","body":{"session":"s3","event":"open"}}' >/dev/null
req '{"t":"session.event","v":1,"key":"s3-e2","body":{"session":"s3","event":"route","strategy":"same"}}' >/dev/null
r=$(req '{"t":"session.event","v":1,"key":"s3-e3","body":{"session":"s3","event":"finish"}}')
expect "s3 到 verifying" "$r" '"state":"verifying"'
r=$(req '{"t":"session.retry","v":1,"key":"s3-r1","body":{"session":"s3","strategy":"same"}}')
expect "第一次同策略重试放行" "$r" '"state":"retrying"'
req '{"t":"session.event","v":1,"key":"s3-e4","body":{"session":"s3","event":"rerun"}}' >/dev/null
req '{"t":"session.event","v":1,"key":"s3-e5","body":{"session":"s3","event":"finish"}}' >/dev/null
r=$(req '{"t":"session.retry","v":1,"key":"s3-r2","body":{"session":"s3","strategy":"same"}}')
expect "第二次同策略重试被拒" "$r" 'bad_request'
r=$(req '{"t":"session.retry","v":1,"key":"s3-r3","body":{"session":"s3","strategy":"other"}}')
expect "换策略立即放行" "$r" '"state":"retrying"'

echo
echo "—— 技能店：内容寻址，canary 先行 ——"
pub1='{"t":"skill.publish","v":1,"key":"p1","body":{"manifest":{"skill_id":"read.file","version":"1.0.0","kind":"code","verify_assert":["exit == 0"],"evidence":"E1","license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"builtin"}},"files":{"main.go":"package main\n"}}}'
r=$(req "$pub1")
expect "注册成功，落在 canary" "$r" '"channel":"canary"'
expect "许可证闭包被算出（MIT = permissive = 0）" "$r" '"class":0'
expect "八步流水线走到了 package" "$r" '"name":"package"'
skref=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
if [ -n "$skref" ]; then
  printf '  ok    skill_ref 是 64 位内容哈希\n'
else
  printf '  FAIL  没拿到 skill_ref\n'
  fails=$((fails + 1))
fi

r=$(req "$pub1")
expect "同内容重注册得到同一个 ref（去重）" "$r" "\"ref\":\"$skref\""

r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$skref\"}}")
expect "resolve 读回清单" "$r" '"skill_id":"read.file"'
expect "resolve 读回载荷索引" "$r" '"files":["main.go"]'

echo
echo "—— 技能店：三道门各拒一次，且都带回诊断 ——"
r=$(req '{"t":"skill.publish","v":1,"key":"p2","body":{"manifest":{"skill_id":"bad.lic","version":"1","kind":"code","verify_assert":["x"],"evidence":"E1","license":"WTFPL","provenance":{"source":"x"},"env":{"interpreter":"builtin"}},"files":{"a":"x"}}}')
expect "未知许可证被拒（未知不等于宽松）" "$r" 'license_conflict'
r=$(req '{"t":"skill.publish","v":1,"key":"p3","body":{"manifest":{"skill_id":"bad.path","version":"1","kind":"code","verify_assert":["x"],"evidence":"E1","license":"MIT","provenance":{"source":"x"},"env":{"interpreter":"builtin"}},"files":{"../evil":"x"}}}')
expect "越界载荷路径被拒" "$r" 'bad_request'
r=$(req '{"t":"skill.publish","v":1,"key":"p4","body":{"manifest":{"skill_id":"bad.net","version":"1","kind":"code","verify_assert":["x"],"evidence":"E1","license":"MIT","provenance":{"source":"x"},"env":{"interpreter":"builtin","network":true}},"files":{"a":"x"}}}')
expect "运行期联网被拒（不可复现）" "$r" 'bad_request'
expect "被拒时也带回已经走过的步骤" "$r" '"steps"'
r=$(req '{"t":"skill.resolve","v":1,"body":{"ref":"../../etc/passwd"}}')
expect "非哈希的 ref 被拒" "$r" 'bad_request'

echo
echo "—— 技能店：证据等级是固化闸门 ——"
r=$(req '{"t":"skill.publish","v":1,"key":"p5","body":{"manifest":{"skill_id":"raw.guess","version":"1","kind":"code","verify_assert":["x"],"evidence":"E0","license":"MIT","provenance":{"source":"model"},"env":{"interpreter":"builtin"}},"files":{"a":"x"}}}')
expect "E0 可以进 canary（生成不等于可用）" "$r" '"channel":"canary"'
r=$(req '{"t":"skill.promote","v":1,"key":"pr0","body":{"name":"raw.guess","from":"canary","to":"stable"}}')
expect "E0 不许固化到 stable" "$r" 'bad_request'
expect "拒绝理由点明了证据等级" "$r" 'E0'
r=$(req '{"t":"skill.promote","v":1,"key":"pr1","body":{"name":"read.file","from":"canary","to":"stable"}}')
expect "E1 可以提升到 stable" "$r" '"channel":"stable"'
r=$(req '{"t":"skill.list","v":1,"body":{"name":"read.file"}}')
expect "stable 与 canary 指向同一个 ref" "$r" "\"stable\":\"$skref\""
r=$(req '{"t":"skill.promote","v":1,"key":"pr2","body":{"name":"read.file","from":"canary"}}')
expect "from/to 没有默认值" "$r" 'bad_request'

echo
echo "—— 记忆：写不是编辑，纠正靠取代 ——"
r=$(req '{"t":"memory.write","v":1,"key":"m1","body":{"id":"r1","text":"the kernel keeps four concepts","evidence":"E2","recorded_at":100,"tags":["kernel"]}}')
expect "写入被接受到链上" "$r" '"op":"write"'
r=$(req '{"t":"memory.write","v":1,"key":"m2","body":{"id":"r1","text":"a second write must be refused","evidence":"E2","recorded_at":110}}')
expect "同 id 二次写被拒（写不是编辑）" "$r" 'bad_request'
r=$(req '{"t":"memory.retrieve","v":1,"body":{}}')
expect "默认检索看得见 E2" "$r" '"ids":["r1"]'
expect "★ 被拒的那次一个字节都没进链" "$r" '"total":1'

r=$(req '{"t":"memory.write","v":1,"key":"m3","body":{"id":"r0","text":"a guess with nothing verifying it","evidence":"E0","recorded_at":100}}')
expect "E0 写得下" "$r" '"op":"write"'
r=$(req '{"t":"memory.retrieve","v":1,"body":{}}')
expect "★ 但 E0 默认检索不到（写得下，看不见）" "$r" '"ids":["r1"]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0"}}')
expect "点名 E0 才可见" "$r" '"ids":["r0","r1"]'

r=$(req '{"t":"memory.write","v":1,"key":"m4","body":{"id":"r2","text":"the kernel keeps four concepts and nothing else","evidence":"E3","recorded_at":120,"tags":["kernel","revised"]}}')
expect "第二条记录写入" "$r" '"op":"write"'
r=$(req '{"t":"memory.supersede","v":1,"key":"m5","body":{"id":"r1","superseded_by":"r2","recorded_at":130}}')
expect "取代被接受到链上" "$r" '"op":"supersede"'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0"}}')
expect "取代后旧记录离开当前视图" "$r" '"ids":["r0","r2"]'

echo
echo "—— 记忆：两个时钟，不是一个 ——"
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0","as_of":115}}')
expect "★ t=115 时我们还没学到 r2" "$r" '"ids":["r0","r1"]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0","as_of":135}}')
expect "★ t=135 时 r2 已知且 r1 已被取代" "$r" '"ids":["r0","r2"]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0","tags":["kernel"]}}')
expect "标签命中 r2（r1 已被取代，即使它有同一个标签）" "$r" '"ids":["r2"]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0","tags":["kernel","revised"]}}')
expect "标签是全必需，不是任选" "$r" '"ids":["r2"]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E0","tags":["kernel","nope"]}}')
expect "缺一个标签就不算命中" "$r" '"ids":[]'
r=$(req '{"t":"memory.retrieve","v":1,"body":{"min_evidence":"E9"}}')
expect "未知证据等级被拒" "$r" 'bad_request'

echo
echo "—— 上下文合成器：背包、年表、去重 ——"
r=$(req '{"t":"compose.build","v":1,"body":{"class":"chat","budget":200,"candidates":[{"id":"t1","section":"recent","text":"first turn","similarity":1,"evidence":"E2","recency":1,"task_relevance":1},{"id":"t2","section":"recent","text":"second turn","similarity":1,"evidence":"E2","recency":1,"task_relevance":1},{"id":"t3","section":"recent","text":"third turn","similarity":1,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "★ recent 段保持年表（打包不许重写时间）" "$r" '"kept":["t1","t2","t3"]'
expect "各段预算被报告" "$r" '"recent"'
expect "用量被报告" "$r" '"used"'

r=$(req '{"t":"compose.build","v":1,"body":{"class":"task_exec","budget":1000,"candidates":[{"id":"d1","section":"retrieval","text":"the kernel keeps only four concepts because a concept earns its place by being authoritative or atomic or crossed by every request and nothing else does","similarity":1,"evidence":"E2","recency":1,"task_relevance":1},{"id":"d2","section":"retrieval","text":"the kernel keeps only four concepts because a concept earns its place by being authoritative or atomic or crossed by every reply and nothing else does","similarity":0.9,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "★ 近重复被去掉，留下更强的那条" "$r" '"kept":["d1"]'
expect "被去掉的进了 dropped，不是消失" "$r" '"dropped":["d2"]'

r=$(req '{"t":"compose.build","v":1,"body":{"class":"task_exec","budget":100,"candidates":[  {"id":"big","section":"retrieval","text":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","similarity":1,"evidence":"E2","recency":1,"task_relevance":1},  {"id":"small","section":"retrieval","text":"bbbbbbbbbb","similarity":0.5,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "★ 装不下的长候选不许挡住后面的短候选" "$r" '"kept":["small"]'

r=$(req '{"t":"compose.build","v":1,"body":{"class":"nonsense","budget":10,"candidates":[]}}')
expect "未知任务类被拒" "$r" 'bad_request'

echo
echo "—— 仲裁器：后验是从链上折出来的，不是另存一份 ——"
r=$(req '{"t":"arbiter.observe","v":1,"key":"a1","body":{"attempt":"t1","skill":"read.file","task_class":"task_exec"}}')
expect "意图先上链，尝试处于打开状态" "$r" '"open":1'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a2","body":{"attempt":"t1","skill":"read.file","task_class":"task_exec","outcome":"ok","verified_by":"verifier"}}')
expect "结论落下后尝试关闭" "$r" '"open":0'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a3","body":{"attempt":"t2","skill":"read.file","task_class":"task_exec","outcome":"ok","verified_by":"verifier"}}')
# Assert the reason, not only the code. Two different guards can both answer
# bad_request, and a test that checks the code alone cannot tell which one fired
# — which is how a removed guard survives its own test.
expect "★ 没有意图的结论被拒（链上不能有无源之事）" "$r" 'no open attempt to resolve'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a4a","body":{"attempt":"t3","skill":"read.file","task_class":"task_exec"}}')
expect "先为 t3 写入意图" "$r" '"open":1'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a4","body":{"attempt":"t3","skill":"read.file","task_class":"task_exec","outcome":"ok"}}')
expect "★ 没有核验者的 ok 被拒（执行者不许自证）" "$r" 'needs verified_by'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a5","body":{"skill":"read.file","task_class":"task_exec","outcome":"ok","verified_by":"v"}}')
expect "★ 没有 attempt id 的观测被拒" "$r" 'bad_request'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a6","body":{"attempt":"t4","skill":"read.file","task_class":"task_exec","outcome":"nonsense","verified_by":"v"}}')
expect "未知结果被拒" "$r" 'bad_request'
# t5 is opened first on purpose. Without an open attempt, a refusal here could
# have come from the "no open attempt to resolve" door instead, and a test that
# only checks the code cannot tell the two doors apart.
r=$(req '{"t":"arbiter.observe","v":1,"key":"a6a","body":{"attempt":"t5","skill":"read.file","task_class":"task_exec"}}')
expect "为 t5 写入意图（否则下面那道门认不出是谁拒的）" "$r" '"open":'
r=$(req '{"t":"arbiter.observe","v":1,"key":"a6b","body":{"attempt":"t5","skill":"read.file","task_class":"task_exec","outcome":"unknown","verified_by":"v"}}')
expect "★ unknown 是读出来的，不是写上去的" "$r" 'derived reading, not a written outcome'
r=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"read.file","task_class":"task_exec"}}')
# The unquoted form is the point: a number rendered as text stops being a
# number. One verified ok on a Beta(1,1) prior is 2/3, so 0.6 is also the
# arithmetic, not a magic threshold.
expect "后验被报告，且是数字不是字符串" "$r" '"mean":0.6'
expect "★ 被拒的观测一条都没上链" "$r" '"observations":1'

echo
echo "—— 仲裁器：技能腐烂，没人通知它 ——"
rank_req='{"t":"arbiter.rank","v":1,"body":{"margin":0.2,"offers":[{"action":"skill","skill":"read.file","task_class":"task_exec","value":10,"cost":0.01,"latency":0.05,"latency_weight":0.1},{"action":"escalate","skill":"l3","task_class":"task_exec","value":10,"cost":1.0,"latency":2.0,"latency_weight":0.1,"pinned":0.9,"use_pinned":true}]}}'
i=0
while [ $i -lt 30 ]; do
  i=$((i + 1))
  req "{\"t\":\"arbiter.observe\",\"v\":1,\"key\":\"iok$i\",\"body\":{\"attempt\":\"ok$i\",\"skill\":\"read.file\",\"task_class\":\"task_exec\"}}" >/dev/null
  req "{\"t\":\"arbiter.observe\",\"v\":1,\"key\":\"rok$i\",\"body\":{\"attempt\":\"ok$i\",\"skill\":\"read.file\",\"task_class\":\"task_exec\",\"outcome\":\"ok\",\"verified_by\":\"verifier\"}}" >/dev/null
done
r=$(req "$rank_req")
expect "★ 技能好用时仲裁器选它" "$r" '"chosen":"skill"'

switched=""
i=0
while [ $i -lt 40 ]; do
  i=$((i + 1))
  req "{\"t\":\"arbiter.observe\",\"v\":1,\"key\":\"ibad$i\",\"body\":{\"attempt\":\"bad$i\",\"skill\":\"read.file\",\"task_class\":\"task_exec\"}}" >/dev/null
  req "{\"t\":\"arbiter.observe\",\"v\":1,\"key\":\"rbad$i\",\"body\":{\"attempt\":\"bad$i\",\"skill\":\"read.file\",\"task_class\":\"task_exec\",\"outcome\":\"failed\",\"verified_by\":\"verifier\"}}" >/dev/null
  r=$(req "$rank_req")
  case "$r" in
    *'"chosen":"skill"'*) ;;
    *) switched=$i; break ;;
  esac
done
if [ -n "$switched" ]; then
  printf '  ok    ★ 技能腐烂后第 %s 次失败起被自动弃用（没人通知过仲裁器）\n' "$switched"
else
  printf '  FAIL  技能腐烂了，仲裁器还在选它\n'
  fails=$((fails + 1))
fi
r=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"read.file","task_class":"task_exec"}}')
expect "★ 上界仍高于均值（否则这个技能再也回不来）" "$r" '"upper"'

echo
echo "—— 仲裁器：固化不是看这次省了多少 ——"
r=$(req '{"t":"arbiter.fix","v":1,"body":{"calls":10000,"opex_now":0.01,"opex_after":0.001,"capex":5,"expected_calls":200,"loss":50,"at":"E1"}}')
expect "★ 同样的节省，E1 证据下不该固化" "$r" '"fix":false'
r=$(req '{"t":"arbiter.fix","v":1,"body":{"calls":10000,"opex_now":0.01,"opex_after":0.001,"capex":5,"expected_calls":200,"loss":50,"at":"E4"}}')
expect "★ E4 证据下同一笔账值得固化" "$r" '"fix":true'
r=$(req '{"t":"arbiter.fix","v":1,"body":{"calls":100,"opex_now":0.02,"opex_after":0,"capex":2,"expected_calls":0,"loss":0,"at":"E3"}}')
expect "收支相抵不算理由" "$r" '"fix":false'
r=$(req '{"t":"arbiter.fix","v":1,"body":{"calls":100,"at":"E9"}}')
expect "未知证据等级不给数字" "$r" 'bad_request'

echo
echo "—— 执行器：技能真的被跑起来，成功与否由核验器说 ——"
# 前两个技能的声明一模一样（同样的 budget、同样的两条 verify_assert），
# 只有脚本内容不同：一个真的产出了声明里要的那个词，一个只是自己说自己成功。
xman() { # xman <skill_id> <verify_assert-json> <budget_ms>
  printf '{"skill_id":"%s","version":"1.0.0","kind":"script","verify_assert":%s,"budget":{"ms":%s},"evidence":"E0","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"/bin/sh"}}' "$1" "$2" "$3"
}
xpub() { # xpub <key> <manifest> <files> -> skill_ref
  req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"$1\",\"body\":{\"manifest\":$2,\"files\":$3}}" |
    sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p'
}
xok=$(xpub x1 "$(xman runner.ok '["exit == 0","stdout contains \"RUNNER-OK\""]' 2000)" '{"run.sh":"printf RUNNER-OK\n"}')
xlie=$(xpub x2 "$(xman lie.self '["exit == 0","stdout contains \"RUNNER-OK\""]' 1500)" '{"run.sh":"printf FAIL; exit 0\n"}')
xslow=$(xpub x3 "$(xman slow.one '["exit == 0"]' 300)" '{"run.sh":"sleep 5\n"}')
xvague=$(xpub x4 "$(xman vague.one '["我大概成功了吧"]' 500)" '{"run.sh":"exit 0\n"}')
xnobud=$(xpub x5 "$(xman no.budget '["exit == 0"]' 0)" '{"run.sh":"exit 0\n"}')
xamb=$(xpub x6 "$(xman two.files '["exit == 0"]' 1000)" '{"a.sh":"exit 0\n","b.sh":"exit 0\n"}')
xecho=$(xpub x7 "$(xman echo.stdin '["exit == 0"]' 1000)" '{"run.sh":"cat\n"}')
if [ -n "$xok" ] && [ -n "$xlie" ] && [ -n "$xslow" ] && [ -n "$xvague" ] &&
  [ -n "$xnobud" ] && [ -n "$xamb" ] && [ -n "$xecho" ]; then
  printf '  ok    七个技能已注册（E0，canary）\n'
else
  printf '  FAIL  技能注册没拿到 ref：%s %s %s %s %s %s %s\n' "$xok" "$xlie" "$xslow" "$xvague" "$xnobud" "$xamb" "$xecho"
  fails=$((fails + 1))
fi

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi1\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"x-ok\",\"task_class\":\"task_exec\",\"elevate\":true}}")
expect "★ 注册的技能真的被跑起来，核验器判通过" "$r" '"outcome":"ok"'
expect "判定者是核验器，不是执行器" "$r" '"verified_by":"verify_assert"'
expect "执行器看到的事实被如实报告（退出码）" "$r" '"exit":0'
expect "两条声明被逐条判定为真" "$r" '"assert":"exit == 0","result":"true"'
iseq=$(printf '%s' "$r" | sed -n 's/.*"intent":{"seq":\([0-9]*\).*/\1/p')
rseq=$(printf '%s' "$r" | sed -n 's/.*"resolution":{"seq":\([0-9]*\).*/\1/p')
if [ -n "$iseq" ] && [ -n "$rseq" ] && [ "$rseq" -eq $((iseq + 1)) ]; then
  printf '  ok    ★ intent 先上链、resolution 紧接其后（seq %s→%s），中间没有别的写\n' "$iseq" "$rseq"
else
  printf '  FAIL  intent/resolution 不成对：seq=%s/%s\n' "$iseq" "$rseq"
  fails=$((fails + 1))
fi
xelev=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
expect "★ 一次真实的成功执行把证据从 E0 提到 E1" "$r" '"level":"E1","from":"E0","to":"E1"'

r=$(req '{"t":"skill.promote","v":1,"body":{"name":"runner.ok","from":"canary","to":"stable"}}')
expect "★ 提级后的新 ref 才进得了 stable" "$r" "\"ref\":\"$xelev\""
expect "canary 指针也落在新 ref 上" "$r" '"channel":"stable"'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi2\",\"body\":{\"ref\":\"$xlie\",\"attempt\":\"x-lie\",\"task_class\":\"task_exec\"}}")
expect "★ 执行者自报成功：退出码 0" "$r" '"exit":0'
expect "★ 核验器仍判失败：声明里那条没做到" "$r" '"outcome":"failed"'
expect "失败的理由指名道姓（哪条断言为假）" "$r" 'a declared assertion is false'
expect "失败同样是核验器署名的判定" "$r" '"verified_by":"verify_assert"'
r=$(req '{"t":"skill.promote","v":1,"body":{"name":"lie.self","from":"canary","to":"stable"}}')
expect "★ 自报成功不算证据：它还是 E0" "$r" 'evidence is E0'
expect "★ E0 进不了 stable" "$r" 'unverified work must not be frozen'

rslow=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi3\",\"body\":{\"ref\":\"$xslow\",\"attempt\":\"x-slow\",\"task_class\":\"task_exec\"}}")
expect "★ 超出声明预算的进程被杀掉，判超时" "$rslow" '"outcome":"timeout"'
expect "超时是核验器看时间判的，不是脚本自报的" "$rslow" '"timed_out":true'
# 这一处读的是「当前开着的 intent 数」，用来证明下面那一次没有闭合。
ob1=$(printf '%s' "$rslow" | sed -n 's/.*"open":\([0-9]*\).*/\1/p')

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi4\",\"body\":{\"ref\":\"$xvague\",\"attempt\":\"x-vague\",\"task_class\":\"task_exec\"}}")
expect "★ 声明读不懂时只说判不了，绝不说成功" "$r" '"outcome":"unknown"'
expect "★ 判不了就不写 resolution（链上留空）" "$r" '"resolution":null'
ob2=$(printf '%s' "$r" | sed -n 's/.*"open":\([0-9]*\).*/\1/p')
if [ -n "$ob1" ] && [ -n "$ob2" ] && [ "$ob2" -eq $((ob1 + 1)) ]; then
  printf '  ok    ★ 判不了的那次没写 resolution，开着的 intent 从 %s 变成 %s，等人显式裁决（另一个是仲裁器段故意留下的）\n' "$ob1" "$ob2"
else
  printf '  FAIL  开着的 intent 数没有正好加一：%s → %s\n' "$ob1" "$ob2"
  fails=$((fails + 1))
fi
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi4b\",\"body\":{\"ref\":\"$xvague\",\"attempt\":\"x-vague\",\"task_class\":\"task_exec\"}}")
expect "★ 判不了的 attempt 不许换个说法重跑" "$r" 'has an intent on the chain and no resolution'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi1\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"x-ok\",\"task_class\":\"task_exec\"}}")
expect "★ 同一 attempt 重发：回放既有结论，不跑第二遍" "$r" '"replayed":true'
expect "回放出来的是同一条 resolution" "$r" "\"resolution\":{\"seq\":$rseq"

# 同一个输入两种写法，进程看见的必须是同一串字节（否则「同样的输入」不成立）。
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xe1\",\"body\":{\"ref\":\"$xecho\",\"attempt\":\"x-e1\",\"task_class\":\"task_exec\",\"input\":{\"b\":1,\"a\":2}}}")
o1=$(printf '%s' "$r" | sed -n 's/.*"stdout":"\(.*\)","stderr":.*/\1/p')
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xe2\",\"body\":{\"ref\":\"$xecho\",\"attempt\":\"x-e2\",\"task_class\":\"task_exec\",\"input\":{\"a\":2,\"b\":1}}}")
o2=$(printf '%s' "$r" | sed -n 's/.*"stdout":"\(.*\)","stderr":.*/\1/p')
if [ -n "$o1" ] && [ "$o1" = "$o2" ]; then
  printf '  ok    ★ 同一个输入的两种键序，喂给进程的字节完全相同：%s\n' "$o1"
else
  printf '  FAIL  同一个输入的两种写法喂进去的字节不同：%s vs %s\n' "$o1" "$o2"
  fails=$((fails + 1))
fi

# 五种跑不了的声明：全都是执行之前就被拒，因此链上一个字节都不该动、预算一分不花。
stat1=$(req '{"t":"status","v":1}')
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi5\",\"body\":{\"ref\":\"$skref\",\"attempt\":\"x-code\",\"task_class\":\"task_exec\"}}")
expect "kind=code 不会被假装跑过（没有构建步骤）" "$r" 'needs a build step'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi6\",\"body\":{\"ref\":\"$xnobud\",\"attempt\":\"x-nobud\",\"task_class\":\"task_exec\"}}")
expect "★ 没有声明预算的执行不许开始（A7）" "$r" 'budget.ms must be positive'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi7\",\"body\":{\"ref\":\"$xamb\",\"attempt\":\"x-amb\",\"task_class\":\"task_exec\"}}")
expect "★ 两个文件而没有唯一入口就不许执行" "$r" 'no single entrypoint'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi8\",\"body\":{\"ref\":\"$xok\",\"task_class\":\"task_exec\"}}")
expect "没有 attempt id 的执行被拒（否则永远无法裁决）" "$r" 'bad_request'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xi9\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"x-noclass\",\"task_class\":\"\"}}")
expect "没有 task_class 的执行被拒（按类学习要有类）" "$r" 'bad_request'
stat2=$(req '{"t":"status","v":1}')
if [ "$stat1" = "$stat2" ]; then
  printf '  ok    ★ 被拒的执行在链上一个字节都没写，预算也一分没花\n'
else
  printf '  FAIL  被拒的执行动了链或花了预算：\n        before: %s\n        after:  %s\n' "$stat1" "$stat2"
  fails=$((fails + 1))
fi

echo
echo "—— 组合：串行把上一步的输出接成下一步的输入 ——"
# 三个孩子里，第二个只在真的读到 TAIL 时才印 CHAINED。
# 于是「第二步判 ok」这一条同时证明了接线是通的。
cA=$(xpub c1 "$(xman comb.first '["exit == 0"]' 2000)" '{"run.sh":"cat; echo -n TAIL\n"}')
cB=$(xpub c2 "$(xman comb.second '["exit == 0","stdout contains \"CHAINED\""]' 2000)" '{"run.sh":"grep -q TAIL && echo -n CHAINED || echo -n SOLO\n"}')
cC=$(xpub c3 "$(xman comb.never '["exit == 7"]' 1000)" '{"run.sh":"exit 0\n"}')
if [ -n "$cA" ] && [ -n "$cB" ] && [ -n "$cC" ]; then
  printf '  ok    三个组合用的技能已注册\n'
else
  printf '  FAIL  组合用的技能注册失败：%s %s %s\n' "$cA" "$cB" "$cC"
  fails=$((fails + 1))
fi

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cc1\",\"body\":{\"attempt\":\"c-ser\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"]},\"input\":{\"n\":1}}}")
expect "★ 串行组合跑起来了" "$r" '"op":"serial"'
expect "★ 第一步判定 ok" "$r" "{\"index\":0,\"ref\":\"$cA\",\"attempt\":\"c-ser#0\",\"outcome\":\"ok\""
expect "★ 第二步也 ok —— 这一条同时证明它读到了第一步的输出" "$r" "{\"index\":1,\"ref\":\"$cB\",\"attempt\":\"c-ser#1\",\"outcome\":\"ok\""
expect "总判定由核验器署名（组合不重新求值孩子的断言）" "$r" '"verified_by":"verify_assert"'
expect "组合的意图先上链" "$r" '"intent":{"seq"'
expect "组合的结论也上链了" "$r" '"resolution":{"seq"'

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cc2\",\"body\":{\"attempt\":\"c-par\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"parallel\",\"children\":[\"$cA\",\"$cB\"]},\"input\":{\"n\":1}}}")
expect "★ 并行时第二个孩子拿不到第一个的输出，于是判失败" "$r" "{\"index\":1,\"ref\":\"$cB\",\"attempt\":\"c-par#1\",\"outcome\":\"failed\""
expect "★ 一个孩子确凿失败，组合就是失败（不藏进 unknown）" "$r" '"reason":"child 1'

reasons=""
for op in select conditional retry loop; do
  r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"body\":{\"attempt\":\"c-$op\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"$op\",\"children\":[\"$cA\"]}}}")
  rs=$(printf '%s' "$r" | sed -n 's/.*"reason":"\([^"]*\)".*/\1/p')
  reasons="$reasons$rs
"
done
n=$(printf '%s' "$reasons" | sed '/^$/d' | sort -u | wc -l | tr -d ' ')
if [ "$n" -eq 4 ]; then
  printf '  ok    ★ 四个未实现的算子各给各的理由（不是一句「不支持」）\n'
else
  printf '  FAIL  四个算子的理由只有 %s 种不同\n' "$n"
  fails=$((fails + 1))
fi

before=$(req '{"t":"status","v":1}' | sed -n 's/.*"seq":\([0-9]*\).*/\1/p')
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"body\":{\"attempt\":\"c-miss\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\",\"0000000000000000000000000000000000000000000000000000000000000000\"]}}}")
expect "★ 有孩子解析不了，整个组合在跑之前就被拒" "$r" 'child 1 cannot be resolved'
after=$(req '{"t":"status","v":1}' | sed -n 's/.*"seq":\([0-9]*\).*/\1/p')
expect "★ 一条都没上链：前面那个孩子没被白跑" "$after" "$before"

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"body\":{\"attempt\":\"c-wire\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"],\"wires\":[{\"from\":\"0:out\",\"to\":\"out\"}]}}}")
expect "★ 不去兑现的接线被拒，而不是被忽略" "$r" 'dropping data silently'

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cc3\",\"body\":{\"attempt\":\"c-ser\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"]},\"input\":{\"n\":1}}}")
expect "★ 复用同一个 attempt 是回放，不是跑第二遍" "$r" '"replayed":true'

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cc4\",\"body\":{\"attempt\":\"c-fail\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cC\",\"$cB\"]},\"input\":{\"n\":1}}}")
expect "★ 确凿失败的孩子让整个组合失败" "$r" '"outcome":"failed"'
expect "组合的理由点名是哪一个孩子" "$r" '"reason":"child 0'

echo
echo "—— 组合作为一种技能：它有自己的 ref，所以可以再被组合 ——"
xmanC() { # xmanC <skill_id> <op> <children-json> <deps-json>
  printf '{"skill_id":"%s","version":"1.0.0","kind":"compose","evidence":"E0","license":"MIT","provenance":{"source":"handwritten"},"deps":%s,"compose":{"op":"%s","children":%s}}' "$1" "$4" "$2" "$3"
}
cPair=$(xpub cp1 "$(xmanC comb.pair serial "[\"$cA\",\"$cB\"]" "[\"$cA\",\"$cB\"]")" '{}')
if [ -n "$cPair" ]; then
  printf '  ok    ★ 组合也能被注册，并且拿到一个普通的 ref\n'
else
  printf '  FAIL  组合没能注册\n'; fails=$((fails + 1))
fi

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cp2\",\"body\":{\"ref\":\"$cPair\",\"attempt\":\"c-pair\",\"task_class\":\"task_exec\",\"input\":{\"n\":1}}}")
expect "★ 按 ref 跑一个已发布的组合" "$r" '"outcome":"ok"'
expect "两个叶子各自被独立判定" "$r" '"index":1'
r=$(req '{"t":"arbiter.state","v":1,"body":{}}')
expect "★ 组合的后验按它自己的 ref 分组" "$r" "\"skill\":\"$cPair\""
if printf '%s' "$r" | grep -q '"skill":"compose:'; then
  printf '  FAIL  后验里出现了按形状分组的键\n'
  fails=$((fails + 1))
else
  printf '  ok    后验里没有按形状分组的键（那个键只在给不出 ref 时才会出现）\n'
fi

cChain=$(xpub cp3 "$(xmanC comb.chain serial "[\"$cPair\",\"$cC\"]" "[\"$cPair\",\"$cC\"]")" '{}')
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cp4\",\"body\":{\"ref\":\"$cChain\",\"attempt\":\"c-chain\",\"task_class\":\"task_exec\",\"input\":{\"n\":1}}}")
expect "★ 递归：孩子本身是组合，被展开成三个叶子" "$r" '"index":2'
expect "★ 展开后的第三个叶子是 comb.never，于是整体失败" "$r" '"outcome":"failed"'
expect "★ 失败归因到展开后的位置" "$r" 'child 2'

cPar=$(xpub cp5 "$(xmanC comb.par parallel "[\"$cA\",\"$cB\"]" "[\"$cA\",\"$cB\"]")" '{}')
cBad=$(xpub cp6 "$(xmanC comb.bad serial "[\"$cPar\",\"$cA\"]" "[\"$cPar\",\"$cA\"]")" '{}')
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cp7\",\"body\":{\"ref\":\"$cBad\",\"attempt\":\"c-bad\",\"task_class\":\"task_exec\",\"input\":{\"n\":1}}}")
expect "★ 非串行的组合不能当作单步：集合不是流" "$r" 'silent data loss'
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cp8\",\"body\":{\"ref\":\"$cPair\",\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\"]},\"attempt\":\"c-both\",\"task_class\":\"task_exec\",\"input\":{}}}")
expect "★ ref 与内联组合不能同时给" "$r" 'not both'

echo
echo "—— 组合声明里的三道门 ——"
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"cp9\",\"body\":{\"manifest\":{\"skill_id\":\"comb.owner\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"x\"},\"deps\":[\"$cA\"],\"env\":{\"interpreter\":\"/bin/sh\"},\"compose\":{\"op\":\"serial\",\"children\":[\"$cA\"]}},\"files\":{}}}")
expect "★ 组合不许声明解释器（那是没人读的字段）" "$r" 'declares no interpreter'
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"cp10\",\"body\":{\"manifest\":{\"skill_id\":\"comb.orphan\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"x\"},\"deps\":[\"$cA\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"]}},\"files\":{}}}")
expect "★ 组合必须声明它调用的东西（闭包）" "$r" 'is not in deps'
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"cp11\",\"body\":{\"manifest\":{\"skill_id\":\"comb.files\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"x\"},\"deps\":[\"$cA\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$cA\"]}},\"files\":{\"run.sh\":\"echo hi\n\"}}}")
expect "★ 组合不许带不会被运行的载荷" "$r" 'carries no files'
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"cp12\",\"body\":{\"manifest\":{\"skill_id\":\"comb.liar\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"verify_assert\":[\"exit == 0\"],\"license\":\"MIT\",\"provenance\":{\"source\":\"x\"},\"deps\":[\"$cA\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$cA\"]}},\"files\":{}}}")
expect "★ 组合不许声明自己无从对证的断言" "$r" 'no observation of its own'

echo "—— 预算耗尽：给一个极小的额度 ——"
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
start "$chain" -ms 5 -tokens 5 -money 5
r=$(req '{"t":"spend","v":1,"body":{"ms":5,"tokens":5,"money":5}}')
expect "刚好用完" "$r" '"left"'
r=$(req '{"t":"spend","v":1,"body":{"ms":1,"tokens":0,"money":0}}')
expect "再用一点就拒绝" "$r" 'budget_exhausted'

echo
echo "—— 会话跨重启：从链上重放出来，不是从内存里 ——"
r=$(req '{"t":"session.status","v":1,"body":{"session":"s1"}}')
expect "s1 仍是 done" "$r" '"state":"done"'
expect "s1 轨迹仍完整" "$r" '"ready","routing","executing","verifying","done"'
r=$(req '{"t":"session.status","v":1,"body":{"session":"s2"}}')
expect "s2 仍记得停滞" "$r" '"stalled":true'
r=$(req '{"t":"session.status","v":1,"body":{"session":"s3"}}')
expect "s3 仍记得三次尝试与各自的策略" "$r" '"same","same","other"'
expect "s3 仍停在 retrying" "$r" '"state":"retrying"'

echo
echo "—— 技能店跨重启：指针在盘上，不在内存里 ——"
r=$(req '{"t":"skill.list","v":1,"body":{"name":"read.file"}}')
expect "stable 指针跨重启仍在" "$r" "\"stable\":\"$skref\""
expect "canary 指针跨重启仍在" "$r" "\"canary\":\"$skref\""
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$skref\"}}")
expect "内容仍在 CAS 里可读" "$r" '"skill_id":"read.file"'

echo
echo "—— bye ——"
r=$(req '{"t":"bye","v":1}')
expect "bye 被应答" "$r" '"epoch"'

echo
if [ "$fails" -eq 0 ]; then
  echo "全部通过：只有 curl，全流程可用。"
else
  echo "失败 $fails 项。"
fi
exit "$fails"
