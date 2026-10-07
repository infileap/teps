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
echo "—— 预算耗尽：给一个极小的额度 ——"
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
start "$chain" -ms 5 -tokens 5 -money 5
r=$(req '{"t":"spend","v":1,"body":{"ms":5,"tokens":5,"money":5}}')
expect "刚好用完" "$r" '"left"'
r=$(req '{"t":"spend","v":1,"body":{"ms":1,"tokens":0,"money":0}}')
expect "再用一点就拒绝" "$r" 'budget_exhausted'

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
