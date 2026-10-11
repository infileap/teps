#!/usr/bin/env bash
# MCP 适配器的自由判据：只用 shell 与 printf 驱动 JSON-RPC over stdio。
#
# 这个脚本要证明的不是「MCP 适配器能转发」——那太容易了。它要证明的是三件
# 翻译器**容易做错**的事：
#
#   1. 内核的拒绝被**原样**交回。把 code / reason / retryable 压成一句话，
#      调用者就看不出哪条拒绝可以重发、哪条不能。
#   2. 重发的 JSON-RPC 请求是**同一次逻辑尝试**，必须复用幂等键；而两个不同的
#      请求不能因为碰巧参数相同就被去重掉。
#   3. 翻译器**不发明词汇**：工具表里只有一个工具，内核的 op 名不在这个进程里。
set -uo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

gobin="$root/tools/go"
if [ ! -x "$gobin" ]; then
  echo "drive_mcp: 工具链不在（先跑 tools/install_go.sh）" >&2
  exit 2
fi

work=$(mktemp -d)
cleanup() { [ -n "${pid:-}" ] && kill "$pid" 2>/dev/null; exec 3>&- 2>/dev/null; rm -rf "$work"; }
trap cleanup EXIT

"$gobin" build -o "$work/shell" ./adapters/shell || exit 1
"$gobin" build -o "$work/mcp" ./adapters/mcp || exit 1

chain="$work/chain"
mkdir -p "$chain"

fifo="$work/in"
mkfifo "$fifo"
out="$work/out"
err="$work/err"
: > "$out"

"$work/mcp" -child "$work/shell" -dir "$chain" < "$fifo" > "$out" 2> "$err" &
pid=$!
exec 3> "$fifo"

pass=0
fails=0

ok()   { printf '  ok    %s\n' "$1"; pass=$((pass + 1)); }
fail() { printf '  FAIL  %s\n        want: %s\n        got:  %s\n' "$1" "$2" "$3"; fails=$((fails + 1)); }

send() { printf '%s\n' "$1" >&3; }

# 等一条**新的**回复，把它放进 $resp。
#
# 刻意不用 `r=$(await)` 那种写法：命令替换在子 shell 里跑，函数里对计数器的
# 修改会丢掉，于是每次都会拿到同一行——第一版的每一条断言都因此读到了
# initialize 的回复。计数器必须活在当前 shell 里。
seen=0
resp=""
await() {
  local i
  for i in $(seq 1 400); do
    if [ "$(wc -l < "$out")" -gt "$seen" ]; then
      break
    fi
    sleep 0.02
  done
  seen=$(wc -l < "$out")
  resp=$(tail -n 1 "$out")
}

expect() { # 描述 实际 期望子串
  case "$2" in
    *"$3"*) ok "$1" ;;
    *) fail "$1" "$3" "$2" ;;
  esac
}

echo "—— MCP：握手与工具表 ——"
send '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"drive","version":"1"}}}'
await
expect "initialize 报告服务名" "$resp" '"name":"teps-mcp"'
expect "initialize 声明 tools 能力" "$resp" '"tools":{}'

send '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
await
expect "工具表只有一个工具" "$resp" '"name":"kernel"'
# 翻译器不许把内核的 op 名抄进自己身上。
case "$resp" in
  *'"t":"session.'*|*'"t":"skill.'*|*'"t":"arbiter.'*)
    fail "工具表里不许出现内核的 op 名" "无 op 名" "$resp" ;;
  *) ok "★ 工具表里没有抄写内核的 op 名" ;;
esac
expect "输入 schema 就是帧信封" "$resp" '"required":["t"]'

echo
echo "—— MCP：内核的回答被原样交回 ——"
send '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"hello"}}}'
await
expect "hello 变成 welcome" "$resp" '\"t\":\"welcome\"'
expect "★ 内核的帧在 text 里是逐字的（没有重新序列化）" "$resp" 'epoch'
expect "附带了 epoch 元信息" "$resp" '"_meta":{"epoch":1'

send '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","key":"k1","body":{"x":1}}}}'
await
expect "put 成功" "$resp" '\"seq\":1'
h1=$(printf '%s' "$resp" | grep -o '[0-9a-f]\{64\}' | head -1)

echo
echo "—— MCP：重发是同一个逻辑尝试，两个不同请求不是 ——"
# 同一个 id、同样的参数：MCP 客户端重发。必须复用幂等键，链头不许动。
send '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","key":"k1","body":{"x":1}}}}'
await
h2=$(printf '%s' "$resp" | grep -o '[0-9a-f]\{64\}' | head -1)
if [ -n "$h1" ] && [ "$h1" = "$h2" ]; then
  ok "★ 重发复用同一个键，链头不动"
else
  fail "★ 重发复用同一个键，链头不动" "$h1" "$h2"
fi

# 不传 key：键从 JSON-RPC id + 参数派生。不同的 id ⇒ 不同的键 ⇒ 真的写第二次。
send '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","body":{"x":2}}}}'
await
expect "不同 id 的调用真的写进去了" "$resp" '\"seq\":2'

# 同一个 id 但参数不同 ⇒ 键不同 ⇒ 不许被误去重。
send '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","body":{"x":3}}}}'
await
expect "★ 同一个 id 换了参数不会被误去重" "$resp" '\"seq\":3'

echo
echo "—— MCP：拒绝必须带着码与可重发标志回来 ——"
send '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","key":"","body":{"x":9}}}}'
await
expect "★ 内核的拒绝被原样交回（不是被压成一句话）" "$resp" 'bad_request'
expect "拒绝带着 reason" "$resp" 'reason'
expect "拒绝带着 retryable" "$resp" 'retryable'
expect "被拒的调用被标成 isError" "$resp" '"isError":true'

send '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"put","key":"k9","seq":99,"body":{"x":1}}}}'
await
expect "★ 客户端自供 seq 仍被拒为 client_authority" "$resp" 'client_authority'

echo
echo "—— MCP：协议层 ——"
send '{"jsonrpc":"2.0","id":8,"method":"no/such"}'
await
expect "未知方法报 -32601" "$resp" '-32601'

send '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"kernel","arguments":{}}}'
await
expect "缺 t 报 -32602" "$resp" '-32602'

send '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"nope","arguments":{}}}'
await
expect "未知工具报 -32602" "$resp" '-32602'

# 通知（没有 id）不许有回复：行数必须停住。
before=$(wc -l < "$out")
send '{"jsonrpc":"2.0","method":"notifications/initialized"}'
sleep 0.3
after=$(wc -l < "$out")
if [ "$before" = "$after" ]; then
  ok "★ 通知（无 id）不产生回复"
else
  fail "★ 通知（无 id）不产生回复" "$before 行" "$after 行"
fi

echo
if [ "$fails" -eq 0 ]; then
  printf '全部通过：%d 项。MCP 客户端可以驱动内核，而翻译器不认识内核。\n' "$pass"
else
  printf '失败 %d 项（通过 %d 项）。\n' "$fails" "$pass"
fi
exit $((fails > 0))
