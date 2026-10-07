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
