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

# ★ 一条不存在的命令只会在 stderr 上留一行 `command not found`，而脚本照旧报「全部通过」。
# 这个 handler 把它变成一条 FAIL —— **通过不等于全跑过**：那一行如果本该是个 req，
# 后面的 expect 就会拿上一次的响应去比，然后因为错误的原因通过。
command_not_found_handle() {
  printf '  FAIL  驱动脚本调用了一个不存在的命令：%s —— 那一行根本没跑\n' "$1" >&2
  printf '%s\n' "$1" >> "$tmp/notfound"
  return 127
}

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

reg_check() { # reg_check <label> <ref>
  # 注册成功 = 拿到一个 64 位十六进制的 ref。用 'ok' 当判据是不对的：
  # xpub 回的是 ref，不是状态词。
  if printf '%s' "$2" | grep -qE '^[0-9a-f]{64}$'; then
    printf '  ok    %s注册得上\n' "$1"
  else
    printf '  FAIL  %s没拿到 ref：%s\n' "$1" "$2"
    fails=$((fails + 1))
  fi
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
start "$chain" -ms 180000
r=$(req '{"t":"hello","v":1}')
expect "应答类型是 welcome" "$r" '"t":"welcome"'
expect "epoch 从 1 开始" "$r" '"epoch":1'

# ★ 最早的一次度量：什么都还没跑过。
# 「每次成功花多少钱」此刻没有定义 —— 一个没有成功的每次成功成本，
# 是一个装扮成数字的除零。这一条只能在这里问，因为一旦有东西跑成功过，
# 那个状态就再也回不来了。
r=$(req '{"t":"metrics.get","v":1}')
expect "★ 还没跑过任何东西时，每次成功成本没有定义" "$r" 'no cost per success'
expect "★ 而且说清是分母为零，不是数字很大" "$r" 'absent one'
expect "★ 花费本身照报（它是有定义的）" "$r" '"successes":0'

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
start "$chain" -ms 180000
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
echo "—— 内容哈希是一个三元组：算法 / 版本 / 归一化 ——"
# ★ 把算法写在名字里。裸 64 位十六进制是磁盘与线上的形式（＝sha256），
# 带前缀的形式说的是同一件事，只是说出来。
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"sha256:$skref\"}}")
expect "★ 带算法前缀的 ref 与裸 ref 指的是同一个 blob" "$r" '"skill_id":"read.file"'
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"blake3:$skref\"}}")
expect "★ 本店没有的算法被拒" "$r" 'blake3'
expect "★ 而且说清「不是损坏」——否则读者会去找一个不存在的损伤" "$r" 'not corrupt'
expect "★ 它是一个调用方的错，不是一个恢复事件" "$r" '"code":"bad_request"'
r=$(req '{"t":"skill.resolve","v":1,"body":{"ref":"sha256:nothex"}}')
expect "★ 前缀合法但十六进制畸形，是另一个答案" "$r" 'not a content hash'

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

echo
echo "—— 必需的材料：缺了它就该拒绝，不是交出一份更小的上下文 ——"
# task_exec 的 system 段占 15%，预算 100 ⇒ 15 token。
r=$(req '{"t":"compose.build","v":1,"body":{"class":"task_exec","budget":100,"candidates":[{"id":"sys","section":"system","text":"the standing instructions for every turn in this session","required":true,"similarity":1,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "★ 必需的材料装不下就拒绝（缺了必需品不会报错，只会答得像对的）" "$r" 'budget_exhausted'
expect "★ 拒绝要点名是谁装不下（在哪个段）" "$r" 'sys in section system'
expect "★ 拒绝要给出那条预算有多大" "$r" 'that section'
expect "★ 拒绝要说清替代办法（拆任务，不是扩窗）" "$r" 'Decompose the task'

r=$(req '{"t":"compose.build","v":1,"body":{"class":"task_exec","budget":100,"candidates":[{"id":"sys","section":"system","text":"standing rule","required":true,"similarity":0.1,"evidence":"E2","recency":1,"task_relevance":1},{"id":"extra","section":"system","text":"a helpful extra note","similarity":1,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "★ 必需的材料分数更低，仍然占住位置" "$r" '"kept":["sys"]'

r=$(req '{"t":"compose.build","v":1,"body":{"class":"task_exec","budget":100,"candidates":[{"id":"extra","section":"system","text":"a helpful extra note that is far too long for the share","similarity":1,"evidence":"E2","recency":1,"task_relevance":1}]}}')
expect "负对照：非必需的材料装不下仍然只是被丢掉" "$r" '"dropped":["extra"]'

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
xman() { # xman <skill_id> <verify_assert-json> <budget_ms> [verify_refute-json]
  # 第四个参数是可选的必败用例。省略它得到的是一份**单边**声明 ——
  # 那种技能仍然注册得上，但到不了 E1，因为「通过」和「总是通过」它分不出来。
  # ${4:-} 而不是 $4：脚本开了 set -u，而第四个参数**按设计是可选的** ——
  # 省略它的调用（只有必过用例的声明）必须能跑，不能因为「读了一个没给的参数」而死。
  refute=""
  [ -n "${4:-}" ] && refute=",\"verify_refute\":$4"
  printf '{"skill_id":"%s","version":"1.0.0","kind":"script","verify_assert":%s%s,"budget":{"ms":%s},"evidence":"E0","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"/bin/sh"}}' "$1" "$2" "$refute" "$3"
}
xpub() { # xpub <key> <manifest> <files> -> skill_ref
  req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"$1\",\"body\":{\"manifest\":$2,\"files\":$3}}" |
    sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p'
}
# runner.ok 现在**分辨输入**：喂它 REFUTE 它就拒绝。这是它配得上 E1 的前提 ——
# 一个永远返回 0 的脚本被证明的是「它通过了」，而不是「它有能力失败」。
RUNNER_OK_SH='read -r line\ncase $line in *REFUTE*) echo REFUSED-INPUT; exit 3;; esac\nprintf RUNNER-OK\n'
xok=$(xpub x1 "$(xman runner.ok '["exit == 0","stdout contains \"RUNNER-OK\""]' 2000 '[{"input":{"marker":"REFUTE"},"assert":"exit == 0"}]')" "{\"run.sh\":\"$RUNNER_OK_SH\"}")
xlie=$(xpub x2 "$(xman lie.self '["exit == 0","stdout contains \"RUNNER-OK\""]' 1500)" '{"run.sh":"printf FAIL; exit 0\n"}')
xslow=$(xpub x3 "$(xman slow.one '["exit == 0"]' 300)" '{"run.sh":"sleep 5\n"}')
xvague=$(xpub x4 "$(xman vague.one '["我大概成功了吧"]' 500)" '{"run.sh":"exit 0\n"}')
xnobud=$(xpub x5 "$(xman no.budget '["exit == 0"]' 0)" '{"run.sh":"exit 0\n"}')
xamb=$(xpub x6 "$(xman two.files '["exit == 0"]' 1000)" '{"a.sh":"exit 0\n","b.sh":"exit 0\n"}')
xecho=$(xpub x7 "$(xman echo.stdin '["exit == 0"]' 1000)" '{"run.sh":"cat\n"}')
# 单边声明：只有必过用例。它注册得上，但到不了 E1。
xone=$(xpub x8 "$(xman runner.one '["exit == 0","stdout contains \"RUNNER-OK\""]' 2000)" "{\"run.sh\":\"$RUNNER_OK_SH\"}")
# ★ 必败用例其实**会通过**：喂它空输入，`exit == 0` 照样成立。
# 这不是「驳不倒」，这是「这条断言不分辨任何东西」—— 而那正是最该拦下的一种。
xweak=$(xpub x9 "$(xman runner.weak '["exit == 0","stdout contains \"RUNNER-OK\""]' 2000 '[{"input":{},"assert":"exit == 0"}]')" "{\"run.sh\":\"$RUNNER_OK_SH\"}")
# ★ 必败用例**超时**：喂 REFUTE 它就睡死过去。超时是关于时间的事实，
# 不是「这条断言被证明为假」—— 算作驳倒就是让一个坏掉的技能靠「跑不完」拿到 E1。
RUNNER_HANG_SH='read -r line\ncase $line in *REFUTE*) sleep 5; exit 0;; esac\nprintf RUNNER-OK\n'
xtime=$(xpub x10 "$(xman runner.hangs '["exit == 0","stdout contains \"RUNNER-OK\""]' 300 '[{"input":{"marker":"REFUTE"},"assert":"exit == 0"}]')" "{\"run.sh\":\"$RUNNER_HANG_SH\"}")
# 一条必败用例必须说清**哪条断言**应该失败：没有断言的必败用例没有内容，
# 它只会把「我们试过」变成一句没有宾语的话。
r=$(req '{"t":"skill.publish","v":1,"key":"xr0","body":{"manifest":{"skill_id":"empty.refute","version":"1.0.0","kind":"script","verify_assert":["exit == 0"],"verify_refute":[{"input":{},"assert":""}],"budget":{"ms":1000},"evidence":"E0","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"/bin/sh"}},"files":{"run.sh":"exit 0\n"}}}')
expect "★ 必败用例必须说清哪条断言应该失败" "$r" 'has no assert'
expect "★ 理由说清缺的是什么" "$r" 'nothing to fail has no content'
r=$(req '{"t":"skill.publish","v":1,"key":"xr1","body":{"manifest":{"skill_id":"compose.refute","version":"1.0.0","kind":"compose","verify_refute":[{"input":{},"assert":"exit == 0"}],"budget":{"ms":1000},"evidence":"E0","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"compose":{"op":"serial","children":[]}},"files":{}}}')
expect "★ 组合不许声明必败用例（它自己什么都不产出）" "$r" 'compose declares no verify_refute'

if [ -n "$xok" ] && [ -n "$xlie" ] && [ -n "$xslow" ] && [ -n "$xvague" ] &&
  [ -n "$xnobud" ] && [ -n "$xamb" ] && [ -n "$xecho" ] && [ -n "$xone" ] && [ -n "$xweak" ] && [ -n "$xtime" ]; then
  printf '  ok    十个技能已注册（E0，canary）\n'
else
  printf '  FAIL  技能注册没拿到 ref：%s %s %s %s %s %s %s %s %s %s\n' "$xok" "$xlie" "$xslow" "$xvague" "$xnobud" "$xamb" "$xecho" "$xone" "$xweak" "$xtime"
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
# ★ 提级之所以成立，是因为它**真的被驳倒过**：那条必败用例跑起来断言不成立。
# 报出来的是「我们试过驳倒它」这件事本身，不是一句「已提级」。
expect "★ 必败用例的结果随提级一起报出来" "$r" '"refutations"'
expect "★ 那条断言确实没成立（held）" "$r" '"held":true'
expect "★ 并且指名是哪条断言" "$r" '"assert":"exit == 0"'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xe1\",\"body\":{\"ref\":\"$xone\",\"attempt\":\"x-one\",\"task_class\":\"task_exec\",\"elevate\":true}}")
expect "★ 单边证据不是证据：只有必过用例的技能到不了 E1" "$r" 'one-sided'
expect "★ 而且理由说清缺的是什么" "$r" 'has not been shown to be able to fail'
expect "★ 它仍然是一次成功的执行（判定没变，变的只是证据等级）" "$r" '"outcome":"ok"'
expect "★ 它停在 E0" "$r" '"level":""'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xe3\",\"body\":{\"ref\":\"$xtime\",\"attempt\":\"x-time\",\"task_class\":\"task_exec\",\"elevate\":true}}")
expect "★ 必败用例超时：超时是关于时间的事实，不是被驳倒" "$r" '"held":false'
expect "★ 那条必败用例的判定如实报成 timeout" "$r" '"verdict":"timeout"'
expect "★ 于是它也停在 E0（跑不完不等于被证明为假）" "$r" '"level":""'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"xe2\",\"body\":{\"ref\":\"$xweak\",\"attempt\":\"x-weak\",\"task_class\":\"task_exec\",\"elevate\":true}}")
expect "★ 必败用例其实通过了 ⇒ 这条断言不分辨任何东西" "$r" 'does not discriminate'
expect "★ 被驳倒的是「它分不出来」，不是「它做不到」" "$r" '"held":false'
expect "★ 它也停在 E0" "$r" '"level":""'

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
# 互斥的另一面：被杀掉的进程确实起来了，所以它不是「宿主拒绝启动」。
expect "★ 超时的那个确实起来了（互斥的另一面）" "$rslow" '"refused":false'
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
expect "★ 组合报出它的等级天花板（孩子里最弱的那个）" "$r" '"ceiling":"E0"'

expect "★ 串行组合跑起来了" "$r" '"op":"serial"'
expect "★ 第一步判定 ok" "$r" "{\"index\":0,\"ref\":\"$cA\",\"attempt\":\"c-ser#0\",\"outcome\":\"ok\""
expect "★ 第二步也 ok —— 这一条同时证明它读到了第一步的输出" "$r" "{\"index\":1,\"ref\":\"$cB\",\"attempt\":\"c-ser#1\",\"outcome\":\"ok\""
expect "总判定由核验器署名（组合不重新求值孩子的断言）" "$r" '"verified_by":"verify_assert"'
expect "组合的意图先上链" "$r" '"intent":{"seq"'

expect "组合的结论也上链了" "$r" '"resolution":{"seq"'

# ★ elevate 曾经在这里被接受然后**静默忽略**：composeBody 有 Elevate 字段，
#   而 composeResult 里没有 elevation，代码里也没有任何地方读它 —— 传了
#   elevate: true，拿到 200，什么都没发生。那是最坏的一种 API 谎言：
#   不是拒绝，也不是做到，是**看起来做到了**。
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"ce1\",\"body\":{\"attempt\":\"c-elev\",\"task_class\":\"task_exec\",\"elevate\":true,\"composition\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"]},\"input\":{\"n\":1}}}")
expect "★ 内联组合没有清单可重新注册 —— 它是一个传进来的图，不是一个有名字的东西" "$r" 'an inline composition has no manifest'
expect "★ 提级失败不把这次执行说成失败（执行确实成功了）" "$r" '"outcome":"ok"'
expect "★ 而且它说清真正的路：先发布，再提级那个 ref" "$r" 'Publish it first' 

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
expect "★ 按 ref 跑的组合，后验按它的 skill_id 分组（不是 ref：升级会换 ref）" "$r" '"skill":"comb.pair"' 

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
echo
echo "—— 规划器：让系统提议一条没人写过的链 ——"
# ★ 一个孩子是两边的，当且仅当它**有能力失败**。
#
# 规划器找到的这两个孩子原先只有 `exit == 0`，而它们的载荷永远退出 0 ——
# 那份声明**不可能被证伪**，所以它们到不了 E1，整条链也就到不了天花板。
#
# 所以这一轮让它们真的能失败：载荷分辨一个标记，喂 REFUSE 就退出非零。
# 于是「必过用例通过 + 必败用例被驳倒」两件事都成立，链才第一次真的能提级。
xtyp() { # xtyp <skill_id> <in-json> <out-json> [verify_refute-json]
  refute=""
  [ -n "${4:-}" ] && refute=",\"verify_refute\":$4"
  printf '{"skill_id":"%s","version":"1.0.0","kind":"script","input_schema":%s,"output_schema":%s,"verify_assert":["exit == 0"],"budget":{"ms":2000},"evidence":"E1","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"/bin/sh"}%s}' "$1" "$2" "$3" "$refute"
}
tA=$(xpub t1 "$(xtyp typ.split '["doc"]' '["lines"]' '[{"input":{"input":"REFUSE"},"assert":"exit == 0"}]')" '{"run.sh":"in=$(cat)\ncase $in in *REFUSE*) exit 3;; esac\nprintf %s \"$in\"; printf TAIL\n"}')
tB=$(xpub t2 "$(xtyp typ.tag '["lines"]' '["tagged"]' '[{"input":{"input":"REFUSE"},"assert":"exit == 0"}]')" '{"run.sh":"in=$(cat)\ncase $in in *REFUSE*) exit 5;; esac\ncase $in in *TAIL*) printf CHAINED;; *) printf SOLO;; esac\n"}')

# ★ 天花板跟着孩子走，不是恒为 E0。
#
# 这一条是补一条漏网的变异写的：上面 compose 段里孩子全是 E0，于是
# 「天花板恒为 E0（根本不数孩子）」与真实现给出同一个答案，变异活了下来。
# **用所有答案都一样的输入，测不出一个会分辨答案的检查** —— 这是本项目
# 同一个形状第三次出现（前两次是证据上限取 min 还是 first、以及变异脚本
# 自己把活下来的变异报成被抓住）。
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cce1\",\"body\":{\"attempt\":\"c-ceil\",\"task_class\":\"task_exec\",\"composition\":{\"op\":\"serial\",\"children\":[\"$tA\",\"$tB\"]},\"input\":{\"n\":1}}}")
expect "★ 两个 E1 孩子 ⇒ 天花板是 E1，不是恒为 E0" "$r" '"ceiling":"E1"'
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"cce2\",\"body\":{\"attempt\":\"c-ceil2\",\"task_class\":\"task_exec\",\"elevate\":true,\"composition\":{\"op\":\"serial\",\"children\":[\"$tA\",\"$tB\"]},\"input\":{\"n\":1}}}")
expect "★ 提级响应里的天花板也跟着孩子走" "$r" '"ceiling":"E1"' 
tC=$(xpub t3 "$(xtyp typ.wrap '["tagged"]' '["final"]')" '{"run.sh":"cat; echo -n WRAPPED\n"}')

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"tp0\",\"body\":{\"ref\":\"$tB\",\"attempt\":\"tp-alone\",\"task_class\":\"task_exec\",\"input\":{}}}")
expect "★ typ.tag 单独跑只会印 SOLO：它需要上游" "$r" '"stdout":"SOLO"'

r=$(req '{"t":"compose.propose","v":1,"body":{"from":["doc"],"to":["tagged"],"task_class":"task_exec","max_depth":4,"floor":0.1,"max_proposals":5}}')
expect "★ 规划器从类型里找到了这条链（没人告诉它）" "$r" '"names":["typ.split","typ.tag"]'
expect "★ 提议带上了它自己的身份：形状的哈希" "$r" '"shape_id":"'
shapeFromProposal=$(printf '%s' "$r" | python3 -c 'import json,sys
ps=json.load(sys.stdin)["body"]["proposals"]
print(ps[0]["shape_id"] if ps else "")' 2>/dev/null || true)
if printf '%s' "$shapeFromProposal" | grep -qE '^[0-9a-f]{64}$'; then
  printf '  ok    ★ 形状 id 是 64 位内容哈希\n'
else
  printf '  FAIL  提议没有带上合法的形状 id\n'; fails=$((fails + 1))
fi
expect "★ 提议自述它为什么够得着" "$r" '"why"'
expect "★ 不声明类型的技能被报告为无法参与规划" "$r" 'will not guess what a skill accepts'

r=$(req '{"t":"compose.propose","v":1,"body":{"from":["doc"],"to":["nowhere"],"task_class":"task_exec","max_depth":4,"floor":0.1}}')
expect "★ 够不着的目标不提议" "$r" '"proposals":[]'
expect "★ 但它报告自己探索过多少状态" "$r" '"states"'

r=$(req '{"t":"compose.propose","v":1,"body":{"from":["doc"],"to":["tagged"],"task_class":"task_exec","max_depth":4,"floor":0.99}}')
expect "★ 可靠性地板把链剪掉，并说明理由" "$r" 'below the floor'

r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"tp1\",\"body\":{\"manifest\":{\"skill_id\":\"auto.tag\",\"version\":\"1.0.0\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"planner\"},\"deps\":[\"$tA\",\"$tB\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$tA\",\"$tB\"]}},\"files\":{}}}")
cTag=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')

autoRef=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
expect "★ 搜索的结果被注册成了一个技能" "$r" '"channel":"canary"'
expect "★ 它从未运行过，所以是 E0 —— 不是默认值，是唯一为真的答案" "$r" '"evidence":"E0"'

# ★ 组合的两边性继承自孩子。
#
# 这一条是这一轮的核心：组合没有自己的 verify_assert，所以「它有没有能力失败」
# 只能来自孩子。任何一个孩子是单边的，整条链就是单边的 —— 它被证明能通过，
# 没有被证明有能力失败。而 $tA/$tB 正是单边的（xtyp 只声明了 verify_assert）。
#
# 于是旗舰组合 auto.tag 提不了级。这不是拒绝的借口，是一个真实的能力缺口：
# **要让组合能提级，先让每个孩子能证明自己会失败。**
r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"ce2\",\"body\":{\"ref\":\"$cTag\",\"attempt\":\"c-elev2\",\"task_class\":\"task_exec\",\"elevate\":true,\"input\":{\"n\":1}}}")
expect "★ 已发布的组合可以尝试提级（内联的不行）" "$r" '"elevation":{'
expect "★★ 每个孩子都能证明自己会失败 ⇒ 组合第一次真的到了天花板" "$r" '"to":"E1"'
expect "★ 提级是一次真的写：它报出了新的 ref" "$r" '"ref":"'
expect "★ 并说清依据：每个孩子都是两边的，且链核验通过" "$r" 'every child is two-sided and the chain verified'
cElev=$(printf '%s' "$r" | sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$cElev\"}}")
expect "★★ 提级后的制品解析出来就是 E1 —— 组合第一次不只是 E0" "$r" '"evidence":"E1"'
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$cTag\"}}")
expect "★ 而它提级前那一份仍然是 E0（提级是新增一份，不是改写旧的）" "$r" '"evidence":"E0"' 

# ★ 提级到 E1 只是拿到了进 stable 的资格；资格要**用掉**才算走完。
#
# 这是整条路第一次走到终点：规划器找到一条链 → 发布 → 跑通 → 提级 → 冻结成 stable。
r=$(req '{"t":"skill.promote","v":1,"body":{"name":"auto.tag","from":"canary","to":"stable"}}')
expect "★★ 提级后的组合才进得了 stable" "$r" "\"ref\":\"$cElev\""
expect "★ 冻结的是 stable 指针" "$r" '"channel":"stable"'
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$cElev\"}}")
expect "★★ 被冻结的那份是 kind=compose 的 E1 制品 —— 规划器找到的链第一次真的走完全程" "$r" '"kind":"compose"'
expect "★★ 它的证据等级就是 E1" "$r" '"evidence":"E1"'


# ★ 组合不许洗白孩子的证据等级。
#   组合本身不产生任何观测——它没有自己的进程、没有自己的断言。所以它没有
#   自己的证据等级：它的等级是孩子们的最小值。这一条在文档里写了很久，
#   而在代码里从来没被检查过。
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"cp13\",\"body\":{\"manifest\":{\"skill_id\":\"comb.launder\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E1\",\"license\":\"MIT\",\"provenance\":{\"source\":\"x\"},\"deps\":[\"$cA\",\"$cB\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$cA\",\"$cB\"]}},\"files\":{}}}")
expect "★ 两个 E0 孩子组成的组合不许声明 E1" "$r" 'cannot claim more trust'
expect "★ 拒绝说出孩子里最弱的那一个是谁" "$r" 'minimum is E0'
expect "★ 也说出它自己声称的是什么" "$r" 'declares E1'
cLaunder=$(xpub cp14 "$(xmanC comb.honest serial "[\"$cA\",\"$cB\"]" "[\"$cA\",\"$cB\"]")" '{}')
reg_check "诚实的组合（声明 E0）" "$cLaunder"
r=$(req "{\"t\":\"skill.resolve\",\"v\":1,\"body\":{\"ref\":\"$cLaunder\"}}")
expect "★ 它的等级就是孩子里最弱的那个" "$r" '"evidence":"E0"'
if [ -n "$autoRef" ] && [ "$autoRef" != "$tA" ] && [ "$autoRef" != "$tB" ]; then
  printf '  ok    ★ 它是一个新的制品：ref 与任何孩子都不同\n'
else
  printf '  FAIL  自动建出来的组合没有自己的 ref\n'; fails=$((fails + 1))
fi
# ★★ 形状 id 必须能从**已发布的清单**重算出来。那正是「提议 → 运行」这条链接的
# 全部依据：不另存一份身份，而是让身份可以从制品本身再算一遍。
shapeFromManifest=$(python3 - "$tA" "$tB" <<'PYINNER'
import hashlib, sys
h = hashlib.sha256()
h.update(b"serial"); h.update(b"\x00")
for c in sys.argv[1:]:
    h.update(c.encode()); h.update(b"\x00")
print(h.hexdigest())
PYINNER
)
if [ -n "$shapeFromProposal" ] && [ "$shapeFromProposal" = "$shapeFromManifest" ]; then
  printf '  ok    ★★ 形状 id 从已发布的清单能重算出来（链接不靠另存一份身份）\n'
else
  printf '  FAIL  提议的形状 id 与清单重算出来的不一致\n'; fails=$((fails + 1))
fi

# 一条发布了却从没跑过的规划器链：没有它，「未跑过」这条路径就没有样本。
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"tp5\",\"body\":{\"manifest\":{\"skill_id\":\"auto.never\",\"version\":\"1.0.0\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"planner\"},\"deps\":[\"$tA\",\"$tB\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$tA\",\"$tB\"]}},\"files\":{}}}")
expect "★ 一条发布了却从没跑过的规划器链" "$r" '"channel":"canary"'

r=$(req "{\"t\":\"skill.compose\",\"v\":1,\"key\":\"tp2\",\"body\":{\"ref\":\"$autoRef\",\"attempt\":\"tp-auto\",\"task_class\":\"task_exec\",\"input\":{}}}")
expect "★★ 没人写过这条链，它跑通了" "$r" '"outcome":"ok"'
expect "★★ 它的行为不是任何单个技能的行为（同一个输入，单跑是 SOLO）" "$r" '"index":1'

r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"tp3\",\"body\":{\"manifest\":{\"skill_id\":\"auto.bad\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"planner\"},\"deps\":[],\"compose\":{\"op\":\"serial\",\"children\":[]}},\"files\":{}}}")
expect "空组合不许被注册" "$r" 'needs at least one child'
r=$(req "{\"t\":\"skill.publish\",\"v\":1,\"key\":\"tp4\",\"body\":{\"manifest\":{\"skill_id\":\"auto.orphan\",\"version\":\"1\",\"kind\":\"compose\",\"evidence\":\"E0\",\"license\":\"MIT\",\"provenance\":{\"source\":\"planner\"},\"deps\":[\"$tA\"],\"compose\":{\"op\":\"serial\",\"children\":[\"$tA\",\"$tB\"]}},\"files\":{}}}")
expect "★ 组合必须声明它调用的东西" "$r" 'is not in deps'

echo
echo
echo "—— 反射层：唯一一层零上下文的回答 ——"
r=$(req '{"t":"reflex.observe","v":1,"key":"f1","body":{"rule_id":"time.now","stimulus":"What time is it?","action":"clock.now","outcome":"ok"}}')
expect "第一次观测上链" "$r" '"rules":1'
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"What  TIME is it?"}}')
expect "★ 一次观测还不够回答（建立）" "$r" '"matched":false'
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 零上下文：这条路不带任何预算" "$r" '"context_tokens":0'
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it in tokyo"}}')
expect "★ 相近但不是同一个刺激，不触发" "$r" '"matched":false'

r=$(req '{"t":"reflex.observe","v":1,"key":"f2","body":{"rule_id":"time.now","outcome":"ok"}}')
expect "第二次成功上链" "$r" '"rules":1'
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 第二次成功之后开始回答" "$r" '"matched":true'
expect "★ 命中的是那条规则与它要做的动作" "$r" '"action":"clock.now"'

r=$(req '{"t":"reflex.observe","v":1,"key":"f3","body":{"rule_id":"time.now","outcome":"failed"}}')
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 一次失败就让它停（误触发比漏触发贵）" "$r" '"matched":false'

r=$(req '{"t":"reflex.observe","v":1,"key":"f4","body":{"rule_id":"time.now","outcome":"ok"}}')
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 一次成功补不回一次失败（0.3+0.2=0.5 仍在阈值下）" "$r" '"matched":false'
r=$(req '{"t":"reflex.observe","v":1,"key":"f5","body":{"rule_id":"time.now","outcome":"ok"}}')
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 再一次成功才回来" "$r" '"matched":true'

r=$(req '{"t":"reflex.observe","v":1,"key":"f6","body":{"rule_id":"time.now","outcome":"idle"}}')
r=$(req '{"t":"reflex.observe","v":1,"key":"f7","body":{"rule_id":"time.now","outcome":"idle"}}')
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 两个空闲周期之后消退（不用删除）" "$r" '"matched":false'
r=$(req '{"t":"reflex.state","v":1,"body":{}}')
expect "★ 消退被记成「消退过」而不是「从没触发过」" "$r" '"extinguished":1'
r=$(req '{"t":"reflex.observe","v":1,"key":"f8","body":{"rule_id":"time.now","outcome":"ok"}}')
r=$(req '{"t":"reflex.match","v":1,"body":{"stimulus":"what time is it"}}')
expect "★ 消退不是遗忘：一次成功就回来" "$r" '"matched":true'

r=$(req '{"t":"reflex.observe","v":1,"key":"f9","body":{"rule_id":"new.rule","stimulus":"   ","action":"x","outcome":"ok"}}')
expect "空刺激不是通用规则，是打字错误" "$r" 'typo'
r=$(req '{"t":"reflex.observe","v":1,"key":"f10","body":{"outcome":"ok"}}')
expect "没有 rule_id 的观测被拒" "$r" 'rule_id'
r=$(req '{"t":"reflex.observe","v":1,"key":"f11","body":{"rule_id":"time.now","outcome":"nonsense"}}')
expect "未知结果被拒" "$r" 'unknown reflex outcome'

echo "—— 反射层跨重启：规则表是链的投影 ——"
r=$(req '{"t":"reflex.state","v":1,"body":{}}')
expect "跨重启规则仍在" "$r" '"id":"time.now"'
expect "跨重启强度不变（它是链折出来的）" "$r" '"strength":0.7'
expect "★ 跨重启仍然会回答" "$r" '"firing":1'
echo
echo
echo "—— 度量：这个数到底有没有定义 ——"
r=$(req '{"t":"metrics.get","v":1}')
expect "★ 还没收过任务时，覆盖率不是 0 而是「未定义」" "$r" '"defined":false'
expect "★ 未定义时给出理由：分母不存在" "$r" 'the denominator does not exist'
expect "★ 能力是乘积，一个因子未定义则未定义" "$r" 'coverage is undefined'
# ★ 反直觉但正确：任务入口一个都没收过，准确率却**已经有定义** ——
# 因为规划器那条链是直接跑起来的，没有经过任务入口。三个因子里最晚有定义的那个，
# 恰恰是唯一不依赖任务流的那一个。
expect "★ 准确率此刻已经有定义：链跑过，而它没经过任务入口" "$r" '"accuracy":{"chains"'
expect "★ 但它数的只是「声明由规划器产出的链」，不是「提议过的链」" "$r" '"provenance":"planner"'
expect "★ 未跑过的链不许被当成失败计入（unrun 单独报）" "$r" '"unrun"'
expect "★ 它也报了这条链是在哪个任务类下跑的" "$r" '"task_classes"'
expect "★ 一个价格只有放在它买到的东西旁边才有意义" "$r" '"cost":{'
before=$(req '{"t":"status","v":1}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["seq"])')
req '{"t":"metrics.get","v":1}' >/dev/null
after=$(req '{"t":"status","v":1}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["seq"])')
if [ "$before" = "$after" ]; then printf '  ok    ★★ 读度量不写链（seq 不变）\n'; else printf '  FAIL  读度量把链改动了\n'; fails=$((fails+1)); fi

echo
echo "—— 任务入口：率现在有分母了 ——"
r=$(req '{"t":"task.intake","v":1,"key":"tk1","body":{"task_id":"t1","stimulus":"What  TIME is it?"}}')
expect "第一次到达被记录" "$r" '"task_id":"t1"'
expect "★ 反射层接下了它（零上下文）" "$r" '"layer":"reflex"'
expect "★ 零上下文的代价是 0" "$r" '"context_tokens":0'
expect "★ 记下了是哪一层答的" "$r" '"by":"reflex"'
expect "★ 记下了它要做什么" "$r" '"action":"clock.now"'

r=$(req '{"t":"task.intake","v":1,"key":"tk2","body":{"task_id":"t2","stimulus":"what time is it"}}')
expect "第二次到达也走反射层" "$r" '"layer":"reflex"'

r=$(req '{"t":"task.intake","v":1,"key":"tk3","body":{"task_id":"t3","stimulus":"summarise this repository"}}')
expect "★ 接不下的时候落到完整路径" "$r" '"fall_through":true'
expect "★ 落下去的理由说清了它为什么要上下文" "$r" 'needs a composed context'

r=$(req '{"t":"task.intake","v":1,"key":"tk4","body":{"stimulus":"x"}}')
expect "没有 task_id 的到达被拒" "$r" 'bad_request'
expect "★ 拒绝说清了为什么不能重复计数" "$r" 'count it twice'
r=$(req '{"t":"task.intake","v":1,"key":"tk5","body":{"task_id":"t5"}}')
expect "没有刺激的到达被拒" "$r" 'nothing to route'

r=$(req '{"t":"task.stats","v":1,"body":{}}')
expect "★ 总数是三次到达" "$r" '"total":3'
expect "★ 反射层两次" "$r" '"reflex":2'
expect "★ 完整路径一次" "$r" '"full":1'
expect "★★ 拦截率现在是一个真的数（2/3）" "$r" '"intercept_rate":0.6667'
expect "★★ 率带着它的作用域（没有分母的率就是一句没人能核对的话）" "$r" '"scope":"tasks that arrived at this intake"'

echo
echo "—— 任务入口跨重启：统计是链的投影 ——"
r=$(req '{"t":"task.stats","v":1,"body":{}}')
expect "跨重启总数仍在" "$r" '"total":3'
expect "跨重启率不变" "$r" '"intercept_rate":0.6667'
r=$(req '{"t":"task.intake","v":1,"key":"tk6","body":{"task_id":"t6","stimulus":"what time is it"}}')
expect "重启后新的到达仍然走反射层" "$r" '"layer":"reflex"'
expect "★ 分母跟着涨到 4" "$r" '"total":4'

echo
echo "—— 蒸馏：把一次成功变成一条规则（没人写过它）——"
r=$(req '{"t":"task.intake","v":1,"key":"ds1","body":{"task_id":"d1","stimulus":"translate this paragraph"}}')
expect "一个从没见过的刺激：先落到完整路径" "$r" '"layer":"full"'

r=$(req '{"t":"distill.apply","v":1,"key":"ds2","body":{"task_id":"d1","stimulus":"translate this paragraph","layer":"full","outcome":"ok","action":"translate.run"}}')
drule=$(printf '%s' "$r" | sed -n 's/.*"rule_id":"\(d-[0-9a-f]*\)".*/\1/p')
if [ -n "$drule" ]; then printf '  ok    规则 id 由规范化后的刺激派生\n'; else printf '  FAIL  没拿到 rule_id\n'; fails=$((fails + 1)); fi
expect "★ 成功的经验可以被蒸馏" "$r" '"kind":"reflex"'
expect "★ 蒸馏出来的是一条**候选**，不是一条规则" "$r" '"fires":false'
expect "★ 候选的强度是 0.5，还在阈值之下" "$r" '"strength":0.5'
expect "★ 它明说自己只是被建立、没有被确认" "$r" 'established, not confirmed'

r=$(req '{"t":"task.intake","v":1,"key":"ds3","body":{"task_id":"d2","stimulus":"translate this paragraph"}}')
expect "★ 第一次蒸馏之后，同一个刺激仍然落下去（一次成功不够）" "$r" '"layer":"full"'

r=$(req '{"t":"distill.apply","v":1,"key":"ds4","body":{"task_id":"d2","stimulus":"translate this paragraph","layer":"full","outcome":"ok","action":"translate.run"}}')
expect "★ 第二次确认之后它才够强" "$r" '"strength":0.7'
expect "★ 现在它会回答了" "$r" '"fires":true'

r=$(req '{"t":"task.intake","v":1,"key":"ds5","body":{"task_id":"d3","stimulus":"translate this paragraph"}}')
expect "★★ 同一个刺激，这一次是反射层答的" "$r" '"layer":"reflex"'
expect "★★ 零上下文，而这条规则没有任何人写过" "$r" '"context_tokens":0'
expect "★★ 它记下了这条规则的来源" "$r" '"action":"translate.run"'

r=$(req '{"t":"distill.apply","v":1,"key":"ds9","body":{"task_id":"d6","stimulus":"Translate  THIS paragraph?","layer":"full","outcome":"ok","action":"translate.run"}}')
expect "★★ 同一个刺激的两种写法落到同一行（否则一次经验被拆成两半强度）" "$r" "\"rule_id\":\"$drule\""

r=$(req '{"t":"distill.apply","v":1,"key":"ds6","body":{"task_id":"d3","stimulus":"translate this paragraph","layer":"full","outcome":"failed","action":"translate.run"}}')
expect "★ 失败不能被蒸馏（失败教的是回避）" "$r" 'only a verified success'
r=$(req '{"t":"distill.apply","v":1,"key":"ds7","body":{"task_id":"d4","stimulus":"translate this paragraph","layer":"reflex","outcome":"ok","action":"translate.run"}}')
expect "★ 已经零上下文答过的不能再蒸馏（会把同一次成功数两遍）" "$r" 'twice'
r=$(req '{"t":"distill.apply","v":1,"key":"ds8","body":{"task_id":"d5","stimulus":"x","layer":"full","outcome":"","action":"a"}}')
expect "★ 没人记录过它成没成 —— 那也要说出来" "$r" 'nobody recorded'

r=$(req '{"t":"task.stats","v":1,"body":{}}')
expect "★ 新的刺激先让率下降（学会之前要先付学费）" "$r" '"intercept_rate":0.5714'
expect "★ 分母是 7 次到达" "$r" '"total":7'
expect "★ 反射层 4 次" "$r" '"reflex":4'

echo
echo "—— 升级定价：问价不消耗，付价要凭那张报价 ——"
off='[{"action":"skill","skill":"read.file","task_class":"task_exec","value":10,"cost":0.01,"latency":5,"latency_weight":0.1},{"action":"escalate","skill":"l3","task_class":"task_exec","value":10,"cost":1.5,"latency":200,"latency_weight":0.1,"pinned":0.9,"use_pinned":true}]'

before=$(req '{"t":"status","v":1,"id":"e0"}' | sed -n 's/.*"seq":\([0-9]*\).*/\1/p')
r=$(req "{\"t\":\"escalation.quote\",\"v\":1,\"id\":\"e1\",\"body\":{\"task_class\":\"task_exec\",\"offers\":$off}}")
expect "估价成功，给出排名" "$r" '"ranked"'
expect "★ 估价不花钱（spent 显式为 0）" "$r" '"spent":0'
expect "估价给出一个报价 id" "$r" '"quote_id"'
qid=$(printf '%s' "$r" | sed -n 's/.*"quote_id":"\([0-9a-f]\{64\}\)".*/\1/p')
if [ -n "$qid" ]; then printf '  ok    报价 id 是 64 位内容哈希\n'; else printf '  FAIL  没拿到 quote_id\n'; fails=$((fails+1)); fi
expect "估价选出更划算的那条" "$r" '"chosen":"skill"'
after=$(req '{"t":"status","v":1,"id":"e2"}' | sed -n 's/.*"seq":\([0-9]*\).*/\1/p')
if [ "$before" = "$after" ]; then
  printf '  ok    ★★ 估价一个字节都没写进链（seq 仍是 %s）\n' "$after"
else
  printf '  FAIL  估价动了链：%s -> %s\n' "$before" "$after"; fails=$((fails+1))
fi

echo
echo "—— 付价：必须凭一张还成立的报价 ——"
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"key\":\"d1\",\"id\":\"e3\",\"body\":{\"quote_id\":\"$qid\",\"action\":\"skill\",\"attempt\":\"esc1\",\"skill\":\"read.file\",\"task_class\":\"task_exec\",\"offers\":$off}}")
expect "凭报价付价成功" "$r" '"grant_ms"'
expect "付价写进链（有链头）" "$r" '"head"'
expect "★ 报告扣了多少" "$r" '"grant_cost"'
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"id\":\"e4\",\"body\":{\"action\":\"skill\",\"attempt\":\"esc2\",\"offers\":$off}}")
expect "★ 没有报价 id 的决定被拒" "$r" 'needs the quote_id'
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"id\":\"e5\",\"body\":{\"quote_id\":\"$qid\",\"action\":\"teleport\",\"attempt\":\"esc3\",\"offers\":$off}}")
expect "★ 没被报价过的动作被拒" "$r" 'was not among the quoted offers'

echo
echo "—— ★★ 价格一动，那张报价就作废 ——"
# open 是全局未结尝试数，前面几段已经留了几个，所以只能断言增量。
o0=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"read.file","task_class":"task_exec"}}' | sed -n 's/.*"open":\([0-9]*\).*/\1/p')
r=$(req '{"t":"arbiter.observe","v":1,"key":"e6","body":{"attempt":"esc-obs","skill":"read.file","task_class":"task_exec"}}')
o1=$(printf '%s' "$r" | sed -n 's/.*"open":\([0-9]*\).*/\1/p')
if [ "$o1" = "$((o0 + 1))" ]; then
  printf '  ok    先写意图（只有意图没有结论也是一种事实，未结 %s -> %s）\n' "$o0" "$o1"
else
  printf '  FAIL  意图没有让未结数 +1：%s -> %s\n' "$o0" "$o1"; fails=$((fails+1))
fi
r=$(req '{"t":"arbiter.observe","v":1,"key":"e7","body":{"attempt":"esc-obs","skill":"read.file","task_class":"task_exec","outcome":"failed","verified_by":"verifier"}}')
o2=$(printf '%s' "$r" | sed -n 's/.*"open":\([0-9]*\).*/\1/p')
if [ "$o2" = "$o0" ]; then
  printf '  ok    结论落下，未结数回到 %s（后验动了）\n' "$o2"
else
  printf '  FAIL  结论没有让未结数回到 %s：%s\n' "$o0" "$o2"; fails=$((fails+1))
fi 
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"key\":\"d2\",\"id\":\"e8\",\"body\":{\"quote_id\":\"$qid\",\"action\":\"skill\",\"attempt\":\"esc4\",\"skill\":\"read.file\",\"task_class\":\"task_exec\",\"offers\":$off}}")
expect "★★ 按旧价格行动被拒（报价作废）" "$r" 'the quote is stale'
r=$(req "{\"t\":\"escalation.quote\",\"v\":1,\"id\":\"e9\",\"body\":{\"task_class\":\"task_exec\",\"offers\":$off}}")
qid2=$(printf '%s' "$r" | sed -n 's/.*"quote_id":"\([0-9a-f]\{64\}\)".*/\1/p')
if [ -n "$qid2" ] && [ "$qid2" != "$qid" ]; then
  printf '  ok    ★★ 重新问价得到一个新的 id（价格真的变了）\n'
else
  printf '  FAIL  重新问价的 id 没变：%s\n' "$qid2"; fails=$((fails+1))
fi
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"key\":\"d3\",\"id\":\"e10\",\"body\":{\"quote_id\":\"$qid2\",\"action\":\"skill\",\"attempt\":\"esc5\",\"skill\":\"read.file\",\"task_class\":\"task_exec\",\"offers\":$off}}")
expect "凭新报价付价成功" "$r" '"grant_ms"'

# 定价策略也是价格的一部分：同一组 offers 换一个 margin，就是另一张报价。
# 少了这条，两个不同的策略会给出同一个 id，而在一个策略下做的决定
# 会被另一个策略认下来。
r=$(req "{\"t\":\"escalation.quote\",\"v\":1,\"id\":\"e11\",\"body\":{\"task_class\":\"task_exec\",\"margin\":5,\"offers\":$off}}")
qidm=$(printf '%s' "$r" | sed -n 's/.*"quote_id\":\"\([0-9a-f]\{64\}\)\".*/\1/p')
if [ -n "$qidm" ] && [ "$qidm" != "$qid2" ]; then
  printf '  ok    ★★ 换一个 margin 就是另一张报价（定价策略也是价格的一部分）\n'
else
  printf '  FAIL  margin 变了报价 id 没变\n'; fails=$((fails+1))
fi
r=$(req "{\"t\":\"escalation.decide\",\"v\":1,\"id\":\"e12\",\"body\":{\"quote_id\":\"$qid2\",\"action\":\"skill\",\"attempt\":\"esc6\",\"margin\":5,\"offers\":$off}}")
expect "★ 拿零 margin 的报价去按 margin=5 行动被拒" "$r" 'the quote is stale'


echo
echo "—— audit.tail：链本身，不是关于链的摘要 ——"
jlen() { python3 -c 'import json,sys; print(len(json.load(sys.stdin)["body"]["entries"]))'; }
jlast() { python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["entries"][-1]["head"])'; }
jhead() { python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["head"])'; }
jmoney() { python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["left"]["money"])'; }
jfirst() { python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["ranked"][0]["action"])'; }
jspent() { python3 -c 'import json,sys; b=json.load(sys.stdin)["body"]; print(b.get("spent", b.get("reliability",{}).get("spent","")))'; }

r=$(req '{"t":"audit.tail","v":1,"body":{"limit":3}}')
n=$(printf '%s' "$r" | jlen)
if [ "$n" = "3" ]; then printf '  ok    尾部按要求的条数返回\n'; else printf '  FAIL  尾部返回 %s 条，要 3 条\n' "$n"; fails=$((fails+1)); fi
r2=$(req '{"t":"status","v":1}')
lh=$(printf '%s' "$r" | jlast)
sh=$(printf '%s' "$r2" | jhead)
if [ -n "$lh" ] && [ "$lh" = "$sh" ]; then
  printf '  ok    ★ 尾部最后一条的链头就是 status 报的链头（同一个事实，同一个答案）\n'
else
  printf '  FAIL  尾部链头 %s != status 链头 %s\n' "$lh" "$sh"; fails=$((fails+1))
fi
r=$(req '{"t":"audit.tail","v":1}')
n=$(printf '%s' "$r" | jlen)
if [ "$n" = "20" ]; then printf '  ok    没给 limit 时用默认值，不是无声地全给\n'; else printf '  FAIL  默认返回 %s 条\n' "$n"; fails=$((fails+1)); fi
expect "负数 limit 被拒" "$(req '{"t":"audit.tail","v":1,"body":{"limit":-1}}')" 'bad_request'
expect "超过上限被拒（不是悄悄截断）" "$(req '{"t":"audit.tail","v":1,"body":{"limit":1001}}')" 'bad_request'
before=$(req '{"t":"status","v":1}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["seq"])')
req '{"t":"audit.tail","v":1,"body":{"limit":0}}' >/dev/null
after=$(req '{"t":"status","v":1}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["seq"])')
if [ "$before" = "$after" ]; then printf '  ok    ★★ 读链不写链（seq 不变）\n'; else printf '  FAIL  读链把链改动了\n'; fails=$((fails+1)); fi

echo
echo
echo "—— 度量：现在覆盖率有分母了 ——"
r=$(req '{"t":"metrics.get","v":1}')
expect "收过任务之后覆盖率有定义" "$r" '"intercept_rate"'
expect "★ 分母跟着率一起走" "$r" '"tasks"'
expect "可靠性有定义（后验已有观测）" "$r" '"weakest"'
expect "★ 最弱的一环单独报出来（平均会把它盖住）" "$r" '"mean"'
expect "★★ 乘积第一次有了值（覆盖率 × 准确率 × 可靠性）" "$r" '"capability":{"defined":true'
expect "★ 准确率数的是「声明由规划器产出的链」" "$r" '"observed":1'
expect "★ 没跑过的那条不许被当成失败计入（traced 2 / unrun 1）" "$r" '"traced":2'
expect "★ unrun 单独报出来" "$r" '"unrun":1'
expect "★ 跑过的那条算 ok" "$r" '"ok":1'
expect "★ 准确率的值直接报出来" "$r" '"value":1}'
expect "★★ 准确率按任务类求和（后验是按 (skill, task_class) 分组的）" "$r" '"auto.tag":["task_exec"]'
# ★★★ 最强的一条：那个值必须**就是**三个因子的乘积，而不是另算出来的一个数。
# 只断言「有值」，一个把三个数相加再取平均的实现也会通过。
if printf '%s' "$r" | python3 -c '
import json,sys
b=json.load(sys.stdin)["body"]
want=b["coverage"]["intercept_rate"]*b["accuracy"]["value"]*b["reliability"]["mean"]
got=b["capability"]["value"]
sys.exit(0 if abs(want-got) < 1e-3 else 1)'; then
  printf '  ok    ★★★ 那个值真的就是三个因子的乘积（不是另算的一个数）\n'
else
  printf '  FAIL  乘积的值与三个因子算不出来\n'
  fails=$((fails + 1))
fi
# 最弱的一环必须真的比平均低：只断言字段存在，就分不出「算了」和「没算」
if printf '%s' "$r" | python3 -c '
import json,sys
b=json.load(sys.stdin)["body"]
w,m=b["reliability"]["weakest"],b["reliability"]["mean"]
sys.exit(0 if w < m else 1)'; then
  printf '  ok    ★★ 最弱的一环确实低于平均（一个技能腐烂了，平均会把它盖住）\n'
else
  printf '  FAIL  weakest 没有真的算出来（等于 mean 或更高）\n'
  fails=$((fails + 1))
fi

# ★ 一个响应里不能自相矛盾：乘积的判据必须和三个因子各自的判据一致。
# 只断言「乘积未定义」会漏掉「准确率谎报有定义」——那时两句话在同一份响应里互相否认。
if printf '%s' "$r" | python3 -c '
import json,sys
b=json.load(sys.stdin)["body"]
factors=[b[k]["defined"] for k in ("coverage","accuracy","reliability")]
sys.exit(0 if b["capability"]["defined"] == all(factors) else 1)'; then
  printf '  ok    ★★ 乘积的判据与三个因子各自的判据一致（同一份响应不许自相矛盾）\n'
else
  printf '  FAIL  乘积的判据与因子的判据不一致\n'
  fails=$((fails + 1))
fi

# ★ 同一个事实两个帧必须给同一个答案：metrics 的覆盖率就是 task.stats 的拦截率。
ts=$(req '{"t":"task.stats","v":1,"body":{}}')
if python3 - "$r" "$ts" <<'PYINNER'
import json,sys
m=json.loads(sys.argv[1])["body"]["coverage"]["intercept_rate"]
t=json.loads(sys.argv[2])["body"]["stats"]["intercept_rate"]
sys.exit(0 if abs(m-t) < 1e-9 else 1)
PYINNER
then
  printf '  ok    ★★ metrics 的覆盖率与 task.stats 的拦截率是同一个数\n'
else
  printf '  FAIL  同一个事实两个帧给了两个答案\n'
  fails=$((fails + 1))
fi

echo
echo "—— 算力池：容量是租约不是计数 ——"
r=$(req '{"t":"pool.register","v":1,"key":"pl1","body":{"id":"core.0","kind":"local","capacity":1,"note":"this machine"}}')
expect "免费的本机核登记成功" "$r" '"id":"core.0"'
r=$(req '{"t":"pool.register","v":1,"key":"pl2","body":{"id":"gpu.far","kind":"remote","money_per_ms":0.002,"capacity":2}}')
expect "远端 GPU 按毫秒计价" "$r" '"id":"gpu.far"'
r=$(req '{"t":"pool.register","v":1,"key":"pl3","body":{"id":"core.0","kind":"local","capacity":4}}')
expect "重复 id 被拒（替换会让租约指着一个已经不是它的资源）" "$r" 'already registered'
r=$(req '{"t":"pool.register","v":1,"key":"pl4","body":{"id":"zero.cap","kind":"local","capacity":0}}')
expect "容量为零被拒（登记表会显得比实际大）" "$r" 'look bigger'
r=$(req '{"t":"pool.register","v":1,"key":"pl5","body":{"id":"neg.price","kind":"remote","money_per_ms":-0.5,"capacity":1}}')
expect "负价格被拒" "$r" 'pays the caller'
r=$(req '{"t":"pool.quote","v":1,"body":{"ms":1000,"tokens":100,"available_money":100}}')
expect "★ 报价选免费的（同价时免费的就是最便宜的）" "$r" '"id":"core.0"'
expect "报价标出它是免费的" "$r" '"free":true'

r=$(req '{"t":"pool.lease","v":1,"key":"pl6","body":{"id":"core.0","holder":"job.a","ttl_ms":60000}}')
expect "租到槽" "$r" '"holder":"job.a"'
expect "报告用了几个槽" "$r" '"used":1'
r=$(req '{"t":"pool.quote","v":1,"body":{"ms":1000,"tokens":100,"available_money":100}}')
expect "★ 免费的被占了，于是选付费的（付不起的价格不是更便宜的价格）" "$r" '"id":"gpu.far"'
expect "付费的报价算出钱" "$r" '"money":2'
r=$(req '{"t":"pool.lease","v":1,"key":"pl7","body":{"id":"core.0","holder":"job.b","ttl_ms":60000}}')
expect "★ 容量满了被拒" "$r" 'lease_held'
expect "★ 拒绝点名持有者（「池满了」不是可执行的信息）" "$r" 'job.a'
r=$(req '{"t":"pool.state","v":1}')
expect "状态里列出持有者" "$r" '"held_by":["job.a"]'
r=$(req '{"t":"pool.lease","v":1,"key":"pl8","body":{"id":"core.0","ttl_ms":60000}}')
expect "匿名租约被拒（它永远释放不了）" "$r" 'anonymous lease'
r=$(req '{"t":"pool.lease","v":1,"key":"pl9","body":{"id":"core.0","holder":"job.c","ttl_ms":0}}')
expect "零 ttl 被拒（持有者一死就永久漏容量）" "$r" 'leaks capacity forever'

echo
echo "—— 算力池：租约过期，容量自己回来 ——"
r=$(req '{"t":"pool.lease","v":1,"key":"pl10","body":{"id":"gpu.far","holder":"job.doomed","ttl_ms":50}}')
expect "短租约拿到槽" "$r" '"holder":"job.doomed"'
r=$(req '{"t":"pool.state","v":1}')
expect "活着时它占着槽" "$r" '"job.doomed"'
expect "★ 聚合 used 把活着的那条算进去" "$r" '"used":2'
sleep 0.15
r=$(req '{"t":"pool.state","v":1}')
case "$r" in
  *'job.doomed'*) printf '  FAIL  ★ 过期租约必须把容量还回来\n'; fails=$((fails + 1)) ;;
  *) printf '  ok    ★ 过期租约把容量还回来了（持有者死了没人释放，池不会因此变小）\n' ;;
esac
r=$(req '{"t":"pool.state","v":1}')
expect "★ 聚合 used 不再数过期的那条" "$r" '"used":1'
r=$(req '{"t":"pool.release","v":1,"key":"pl11","body":{"holder":"job.a"}}')
expect "释放按持有者，不按资源" "$r" '"holder":"job.a"'
r=$(req '{"t":"pool.state","v":1}')
expect "释放后槽空出来" "$r" '"held_by":[]'

echo
echo "—— 准入：池第一次决定一次执行能不能跑 ——"
r=$(req '{"t":"pool.register","v":1,"key":"pa1","body":{"id":"core.paid","kind":"local","money_per_ms":0.001,"capacity":2,"note":"a local core with a meter on it"}}')
expect "第二个本机核（收费的）登记成功" "$r" '"id":"core.paid"'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa2\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-remote\",\"task_class\":\"task_exec\",\"resource\":\"gpu.far\"}}")
expect "★ 点名远端资源被拒（这个组件只在本机 spawn）" "$r" 'cannot dispatch'
expect "拒绝说清是哪一个做不到" "$r" 'gpu.far'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa3\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-nope\",\"task_class\":\"task_exec\",\"resource\":\"ghost.0\"}}")
expect "点名不存在的资源被拒" "$r" 'no such resource'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa4\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-both\",\"task_class\":\"task_exec\",\"resource\":\"core.0\",\"pick\":true}}")
expect "★ 又点名又让池挑，是两句矛盾的话" "$r" 'not both'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa5\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-none\",\"task_class\":\"task_exec\"}}")
expect "不点名就照旧：没有容量被预留" "$r" '"resource":""'
expect "并说明它跑在本来就在的机器上" "$r" 'no capacity was reserved'

r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa6\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-pick\",\"task_class\":\"task_exec\",\"pick\":true}}")
expect "★ 让池挑：挑到免费的那个" "$r" '"resource":"core.0"'
expect "★ 并报告它花了多少钱" "$r" '"cost_money":0'
expect "执行仍然被核验器判定" "$r" '"outcome":"ok"'
r=$(req '{"t":"pool.state","v":1}')
expect "★ 跑完槽就还回去了（不靠调用方记得释放）" "$r" '"held_by":[]'

r=$(req '{"t":"pool.lease","v":1,"key":"pa7","body":{"id":"core.0","holder":"blocker","ttl_ms":60000}}')
expect "另一个持有者占住免费的那个" "$r" '"holder":"blocker"'
r=$(req '{"t":"status","v":1}')
money_before=$(jmoney <<<"$r")
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa8\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-busy\",\"task_class\":\"task_exec\",\"pick\":true}}")
expect "★ 免费的那个忙了，于是挑到收费的那个" "$r" '"resource":"core.paid"'
expect "★ 而且钱是从价格算出来的，不是调用方填的" "$r" '"cost_money":2'
r=$(req '{"t":"status","v":1}')
money_after=$(jmoney <<<"$r")
if [ -n "$money_before" ] && [ -n "$money_after" ] && [ "$((money_before - money_after))" -eq 2 ]; then
  printf '  ok    ★ 报告的花费真的被扣了（报一个价不等于付那个价）\n'
else
  printf '  FAIL  ★ 报告的花费没有被扣：before=%s after=%s\n' "$money_before" "$money_after"
  fails=$((fails + 1))
fi
r=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"runner.ok","task_class":"task_exec"}}')
expect "★ 链上记着这次执行花了多少钱（否则链会声称每次执行都免费）" "$r" '"spent":2'
r=$(req '{"t":"metrics.get","v":1}')
expect "★ 可靠性报告与花费一起给（便宜但不可靠和可靠但付不起是两个答案）" "$r" '"spent":2'

# ★ 后验的 mean 与原始成功率是同一批记录的两个读数：一个折过，一个没折。
#   两者不同是设计使然（遗忘是故意的），但在此之前没有任何地方报过这个差 ——
#   「这个数是折过的」那句话只活在注释里。
expect "★ 原始成功率与折过的均值一起报出来" "$r" '"raw_rate":'
expect "★ 并报出它们差多少（遗忘做了多少工作，是可以说出来的）" "$r" '"decay_gap":'
r2=$(req '{"t":"arbiter.state","v":1,"body":{}}')
chk=$(python3 -c '
import json,sys
b=json.load(sys.stdin)["body"]
n=b["observations"]; ok=b["successes"]; raw=b["raw_rate"]; mean=b["mean"]; gap=b["decay_gap"]
print("ok" if n and abs(raw-ok/n)<1e-4 and abs(gap-(mean-raw))<1e-4 else f"BAD n={n} ok={ok} raw={raw} mean={mean} gap={gap}")
' 2>&1 <<<"$r2")
# ★ 同一个数在两处必须是同一个数：metrics 与 arbiter.state 各报一次 raw_rate，
#   而两条路径各自算一遍 —— 只断言「它出现过」正是「存在性判据冒充一致性判据」。
arb_raw=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["raw_rate"])' <<<"$r2")
r3=$(req '{"t":"metrics.get","v":1}')
met_raw=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["reliability"]["raw_rate"])' <<<"$r3")
if [ -n "$arb_raw" ] && [ "$arb_raw" = "$met_raw" ]; then
  printf '  ok    ★★ metrics 与 arbiter.state 报的 raw_rate 是同一个数（两条路径各算一遍）\n'
else
  printf '  FAIL  ★★ 两处 raw_rate 不一致：metrics=%s arbiter=%s\n' "$met_raw" "$arb_raw"
  fails=$((fails + 1))
fi
# ★ 与自己一致不等于正确。
#   `gap == mean − raw` 只验了算术：把 meanOf 改成只数第一个键，mean 与 gap
#   一起偏移，那条等式照样成立。要抓住它，需要**外部参照**——
#   同一个响应里那些 posteriors 行的平均值。
avg=$(python3 -c '
import json,sys
b=json.load(sys.stdin)["body"]
rows=b["posteriors"]
want=sum(r["mean"] for r in rows)/len(rows) if rows else 0.0
print("ok" if abs(want-b["mean"])<1e-4 else "BAD rows=%d mean=%s avg=%s" % (len(rows), b["mean"], want))
' 2>&1 <<<"$r2")
if [ "$avg" = "ok" ]; then
  printf '  ok    ★★ mean 确实等于那些 posterior 行的平均值（不是只数了第一个）\n'
else
  printf '  FAIL  ★★ mean 与它自己报出来的行对不上：%s\n' "$avg"
  fails=$((fails + 1))
fi
if [ "$chk" = "ok" ]; then
  printf '  ok    ★★ 两个读数出自同一批记录：raw = ok/n，且 decay_gap = mean − raw\n'
else
  printf '  FAIL  ★★ 两个读数对不上：%s\n' "$chk"
  fails=$((fails + 1))
fi
spent_metrics=$(jspent <<<"$r")
r2=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"runner.ok","task_class":"task_exec"}}')
spent_arbiter=$(jspent <<<"$r2")
if [ -n "$spent_metrics" ] && [ "$spent_metrics" = "$spent_arbiter" ]; then
  printf '  ok    ★ 同一个数在两处必须是同一个数（metrics=%s arbiter=%s）\n' "$spent_metrics" "$spent_arbiter"
else
  printf '  FAIL  ★ 两处报的花费不一致：metrics=%s arbiter=%s\n' "$spent_metrics" "$spent_arbiter"
  fails=$((fails + 1))
fi
r=$(req '{"t":"metrics.get","v":1}')
expect "★ 有成功过之后，每次成功成本有定义" "$r" '"per_success"'
cps=$(python3 -c 'import json,sys; b=json.load(sys.stdin)["body"]["cost"]; print(b["spent"], b["successes"], b["per_success"])' <<<"$r")
set -- $cps
if [ "$3" = "$(python3 -c "print(round($1/$2, 4))")" ]; then
  printf '  ok    ★★ 每次成功成本真的就是花费除以成功数（spent=%s ok=%s per=%s）\n' "$1" "$2" "$3"
else
  printf '  FAIL  ★★ 每次成功成本对不上：spent=%s ok=%s per=%s\n' "$1" "$2" "$3"
  fails=$((fails + 1))
fi
r=$(req '{"t":"audit.tail","v":1,"body":{"limit":400}}')
expect "★ 那次执行的 resolution 记录里带着它的花费" "$r" '"cost":2' 
r=$(req '{"t":"pool.release","v":1,"key":"pa9","body":{"holder":"blocker"}}')
expect "释放 blocker" "$r" '"holder":"blocker"'

r=$(req '{"t":"pool.lease","v":1,"key":"pa10","body":{"id":"core.0","holder":"hog","ttl_ms":60000}}')
expect "把免费的那个占住" "$r" '"holder":"hog"'
r=$(req '{"t":"pool.lease","v":1,"key":"pa11","body":{"id":"core.paid","holder":"hog","ttl_ms":60000}}')
expect "再把收费的两个槽都占住" "$r" '"holder":"hog"'
r=$(req '{"t":"pool.lease","v":1,"key":"pa12","body":{"id":"core.paid","holder":"hog","ttl_ms":60000}}')
expect "占满" "$r" '"holder":"hog"'
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"pa13\",\"body\":{\"ref\":\"$xok\",\"attempt\":\"p-full\",\"task_class\":\"task_exec\",\"pick\":true}}")
expect "★ 一个槽都没有了，于是这次执行不发生" "$r" 'budget_exhausted'
expect "★ 被拒的执行没有在链上留下已付费的痕迹" "$r" 'no resource can do this work'
r=$(req '{"t":"pool.release","v":1,"key":"pa14","body":{"holder":"hog"}}')
expect "释放 hog" "$r" '"holder":"hog"'

echo
echo "—— 准入的租约在链上，不在内存里 ——"
r=$(req '{"t":"audit.tail","v":1,"body":{"limit":400}}')
expect "★ 链上有这次尝试占槽的记录" "$r" '"holder":"p-pick"'
expect "★ 也有它释放的记录" "$r" '"op":"release"'
expect "租约记录里是绝对过期时间，不是 ttl" "$r" '"expires"'

echo
echo "—— 报价的钱从池里取，不由调用方填 ——"
r=$(req '{"t":"pool.register","v":1,"key":"pp1","body":{"id":"core.dear","kind":"local","money_per_ms":0.01,"capacity":1,"note":"a local core with an expensive meter"}}')
expect "登记一个贵的本机核" "$r" '"id":"core.dear"'

rank_two='{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","skill":"runner.ok","task_class":"task_exec","value":10,"latency":0,"pinned":1.0,"use_pinned":true,"resource":"core.paid","ms":1000},{"action":"skill","skill":"runner.ok","task_class":"task_exec","value":10,"latency":0,"pinned":1.0,"use_pinned":true,"resource":"core.dear","ms":1000}]}}'
r=$(req "$rank_two")
expect "★ 钱从池里取，不从调用方取" "$r" '"cost_from":"pool"'
expect "★ 便宜的那个：utility = 10 − 1" "$r" '"utility":9'
expect "★ 贵的那个：utility = 10 − 10" "$r" '"utility":0'
first=$(jfirst <<<"$r")
if [ "$first" = "skill" ]; then
  printf '  ok    ★ 排在前面的确实是便宜的那个（价格真的进了排序）\n'
else
  printf '  FAIL  排序没跟着价格走：first=%s\n' "$first"
  fails=$((fails + 1))
fi

r=$(req '{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","value":10,"resource":"core.paid","cost":3,"ms":1000}]}}')
expect "★ 一个数两个来源被拒（池定价，调用方定猜测）" "$r" 'one number, one source'
r=$(req '{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","value":10,"resource":"ghost.9","ms":1000}]}}')
expect "点名没登记的资源的报价被拒" "$r" 'not registered'
r=$(req '{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","value":10,"latency":0,"pinned":1.0,"use_pinned":true,"cost":0},{"action":"skill","value":10,"resource":"core.paid","ms":1000}]}}')
expect "不点名资源时，调用方填的钱照旧管用" "$r" '"cost_from":"caller"'

r=$(req '{"t":"pool.lease","v":1,"key":"pp2","body":{"id":"core.dear","holder":"occupier","ttl_ms":60000}}')
expect "把贵的那个占住" "$r" '"holder":"occupier"'
r=$(req "$rank_two")
expect "★ 占不到的报价被排除出排序" "$r" '"available":false'
expect "★ 但它照样被报出来，并说明为什么" "$r" 'a slot you cannot take is not a cheaper option'
expect "★ 于是只剩下便宜的那个" "$r" '"utility":9'
r=$(req '{"t":"pool.release","v":1,"key":"pp3","body":{"holder":"occupier"}}')
expect "释放 occupier" "$r" '"holder":"occupier"'

echo
echo "—— 报价回答的是哪个问题：一次尝试，还是成功一次 ——"
# ★ 两张报价，两种排法。
#   A: p=0.99, 一次尝试 0.9     B: p=0.50, 一次尝试 0.1     value=1.5
#   按「一次尝试多少钱」： A = 0.585   B = 0.650  -> B 赢
#   按「成功一次多少钱」： A = 0.576   B = 0.550  -> A 赢
# 两个数都不是错的。不先问清是哪一个，答案就没法读。
two_offers='{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","skill":"steady.one","task_class":"task_exec","value":1.5,"cost":0.9,"pinned":0.99,"use_pinned":true,"expectation":"EXP"},{"action":"shadow","skill":"cheap.one","task_class":"task_exec","value":1.5,"cost":0.1,"pinned":0.5,"use_pinned":true,"expectation":"EXP"}]}}'

r=$(req "$(printf '%s' "$two_offers" | sed 's/EXP/attempt/g')")
first=$(jfirst <<<"$r")
if [ "$first" = "shadow" ]; then
  printf '  ok    ★ 按「一次尝试」计价时，便宜的那个赢（B 0.650 > A 0.585）\n'
else
  printf '  FAIL  按一次尝试计价时排序不对：first=%s\n' "$first"
  fails=$((fails + 1))
fi
expect "★ 响应说清它按哪个问题算的" "$r" '"expectation":"attempt"'
expect "★ 两个数都报出来，但只有一个是进了效用函数的" "$r" '"cost_per_attempt":0.9'
expect "★ 成功一次的成本也一并报出（这里 p=0.99 ⇒ 0.9091）" "$r" '"cost_per_success":0.9091'

r=$(req "$(printf '%s' "$two_offers" | sed 's/EXP/success/g')")
first=$(jfirst <<<"$r")
if [ "$first" = "skill" ]; then
  printf '  ok    ★★ 换成「成功一次」计价，赢家换了人（A 0.576 > B 0.550）\n'
else
  printf '  FAIL  按成功一次计价时排序不对：first=%s\n' "$first"
  fails=$((fails + 1))
fi
expect "★ 响应说清换了问题" "$r" '"expectation":"success"'
expect "★ 进了效用函数的是那个除过的数，不是原始报价" "$r" '"cost":0.9091'

r=$(req '{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","value":10,"cost":1,"expectation":"whenever"}]}}')
expect "未知的计价口径被拒，而不是当成默认值" "$r" 'the two questions are'
r=$(req '{"t":"arbiter.rank","v":1,"body":{"offers":[{"action":"skill","skill":"never.works","task_class":"task_exec","value":10,"cost":1,"pinned":0,"use_pinned":true,"expectation":"success"}]}}')
expect "★ 一次也不成功的动作没有价格（它什么都买不到）" "$r" 'has no price'
expect "★ 它被排除出排序，但照样报出来" "$r" '"skipped"'

echo
echo
echo "—— 判定规则的身份：一个后验不能在两套规则上折 ——"
# ★ 同一句 skill_id，两套不同的断言，两次运行。
# 后验按 (skill_id, task_class) 分组，而 skill_id 与版本无关 ⇒
# 不做任何事的话，两次运行会被折进同一个键，报出来的数是**两个不同问题的平均值**。
rv1=$(xpub rv1 "$(xman runner.rules '["exit == 0"]' 1000)" '{"run.sh":"printf RUNNER-OK\n"}')
rv2=$(xpub rv2 "$(xman runner.rules '["exit == 0","stdout contains \"RUNNER-OK\""]' 1000)" '{"run.sh":"printf RUNNER-OK\n"}')
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"rv3\",\"body\":{\"ref\":\"$rv1\",\"attempt\":\"rv-a\",\"task_class\":\"task_exec\"}}")
expect "同一句 skill_id 的第一版跑通" "$r" '"outcome":"ok"'
expect "★ 链上记着这次是被哪组断言判的" "$r" '"rules":"'
r1=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["rules"])' <<<"$r")
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"rv4\",\"body\":{\"ref\":\"$rv2\",\"attempt\":\"rv-b\",\"task_class\":\"task_exec\"}}")
expect "换了断言重新发布之后也跑通" "$r" '"outcome":"ok"'
r2=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["body"]["rules"])' <<<"$r")
if [ -n "$r1" ] && [ -n "$r2" ] && [ "$r1" != "$r2" ]; then
  printf '  ok    ★ 两组不同的断言算出两个不同的身份\n'
else
  printf '  FAIL  断言换过，身份却没变：r1=%s r2=%s\n' "$r1" "$r2"
  fails=$((fails + 1))
fi

r=$(req '{"t":"arbiter.state","v":1,"body":{"skill":"runner.rules","task_class":"task_exec"}}')
expect "★ 这个后验报出它折过几套规则" "$r" '"mixed":true'
expect "★ 两套规则的身份都报出来，不是只报一个数" "$r" "$r1"
expect "★ 另一个身份也在" "$r" "$r2"

r=$(req '{"t":"metrics.get","v":1}')

expect "★ 顶层报出有多少个技能的后验是混的" "$r" '"mixed_skills":1'
expect "★★ 能力乘积此刻未定义：可靠性这一项是不同问题的平均值" "$r" '"capability":{"defined":false'
expect "★★ 并说清为什么——一个把这件事藏起来的乘积是没人会去查的数" "$r" 'average of different questions'

echo
echo "—— 探针沉默 ≠ 拿到但是空 ——"
# ★ 两件事的 stdout 都是空字符串。一个只读 stdout 的读者看到的完全一样，
#   所以判定结果必须不同 —— 那是读者唯一有的东西。
xnil=$(xpub nil1 "$(xman empty.out '["stdout == \"\""]' 1000)" '{"run.sh":":"}')
reg_check "什么都不打印的技能" "$xnil" 
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"ns1\",\"body\":{\"ref\":\"$xnil\",\"attempt\":\"n-empty\",\"task_class\":\"task_exec\"}}")
expect "★「它跑了，什么都没打印」是一个成立的声明" "$r" '"outcome":"ok"'
expect "★ 而它的 stdout 确实是空的" "$r" '"stdout":""'
expect "★ 它不是被拒绝启动的" "$r" '"refused":false'

# 一条**不可能失败的**断言不是一条检查：空针匹配一切。
xvac=$(xpub vac1 "$(xman vacuous.one '["stdout contains \"\""]' 1000)" '{"run.sh":":"}')
reg_check "空洞断言的技能" "$xvac" 
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"ns2\",\"body\":{\"ref\":\"$xvac\",\"attempt\":\"n-vacuous\",\"task_class\":\"task_exec\"}}")
expect "★ 空针匹配一切，所以它什么都没断言" "$r" 'asserts nothing'
expect "★ 判不了，不是判真" "$r" '"outcome":"unknown"'
expect "★ 逐条断言报的是 unevaluable，不是 true" "$r" '"result":"unevaluable"'
expect "★ 它因此拿不到任何证据等级" "$r" '"level":""'

# 探针沉默：解释器根本不存在，进程没有起来。
xdead=$(xpub dead1 '{"skill_id":"dead.probe","version":"1.0.0","kind":"script","verify_assert":["stdout == \"\""],"budget":{"ms":1000},"evidence":"E0","idempotent":true,"license":"MIT","provenance":{"source":"handwritten"},"env":{"interpreter":"/nonexistent/teps-probe"}}' '{"run.sh":":"}')
reg_check "解释器不存在的技能" "$xdead" 
r=$(req "{\"t\":\"skill.invoke\",\"v\":1,\"key\":\"ns3\",\"body\":{\"ref\":\"$xdead\",\"attempt\":\"n-silent\",\"task_class\":\"task_exec\"}}")
expect "★ 进程根本没起来，所以这不是一个执行结果" "$r" 'the execution did not happen'
expect "★ 它是一个独立的判定，不是 failed" "$r" '"outcome":"refused"'
expect "★ 机器可读的那一位也要置起来" "$r" '"refused":true'
expect "★ 退出码是 -1，不是 0（空的 stdout 旁边那个 0 才是「跑了」）" "$r" '"exit":-1'
# ★ 一个根本没起来的进程不可能超时。这条互斥是 Judge 里两条 if 顺序不承重的前提，
#   而一个没有断言过的不变量，下一个人改代码时会当作不存在。
expect "★ 沉默与超时是互斥的（没起来就不可能超时）" "$r" '"timed_out":false'
expect "★ 它同样拿不到证据等级" "$r" '"level":""'

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
# 这道检查必须在裁决之前跑。放进 `fails -eq 0` 的分支里，它就只在「本来就没问题」时执行 ——
# **一个只在没事时才跑的检查，报不出有事。**
if [ -s "$tmp/notfound" ]; then
  printf '  FAIL  驱动脚本有 %s 个不存在的命令（见上）\n' "$(wc -l < "$tmp/notfound")"
  fails=$((fails + 1))
fi

echo
if [ "$fails" -eq 0 ]; then
  echo "全部通过：只有 curl，全流程可用。"
else
  echo "失败 $fails 项。"
fi
exit "$fails"
