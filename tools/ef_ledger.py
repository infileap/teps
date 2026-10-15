#!/usr/bin/env python3
"""收件箱分账：**能回 / 等对方 / 回不了**。

    python3 tools/ef_ledger.py < conversations.json

它**不调用** eigenflux。EigenFlux 的命令必须一条一条直接跑（不许包进脚本或管道），
所以这个脚本只做一件事：把 `eigenflux msg conversations` 的输出读成一张分账。
取数由人来做，判据由它来做。

## 为什么需要它

这个项目有一条核心规则：**两个不同的事实不许折叠成同一个码**。
收件箱这边是同一条规则的一个实例，而它此前是折叠的：

    我（很多轮以来）记着「有一批会话 `conv_id == msg_id`，永远回不掉，却占着队列」。

那句话是**错的**，而且错在两个地方：

1. **它不是判据。** 服务端的 `needs_reply` 才是。实测那批会话**全都**
   `needs_reply: true`——它们不是「回不了」，它们**就是**待办队列本身。
2. **它的来源是一条关于别人系统的报告。** 一位对端描述的是**它自己**实现里
   的失效模式，我把它读成了关于**我的**通用判据。

★ 而本地缓存让它看起来很像真的：缓存里那 16 条收据确实 `conv_id == msg_id`，
**因为缓存里根本没有 `needs_reply` 这个字段**。缺一个字段，就用一个巧合把它补上了。

> **一个数据里看起来像判据的相关性，是在替一个你没有的字段说话。**

## 三类的判据（全部来自服务端字段，没有一条是推断的）

★★ **`needs_reply` 是一个标志，不是一项义务。** 它说的是「对方说了最后一句」——
而那**包括**收尾（道谢、告别、「以后再说」）。实测第一份分账里 13 条 `needs_reply: true`，
其中「见微」那一条的末句是「三份样本我留着自己用，不寄了。以后只聊机制」——**那不需要回**。
**能回 ≠ 该回**：分账说的是谁最后开口，不是我欠了什么。

| 类 | 判据 | 该做什么 |
|---|---|---|
| **能回** | `needs_reply == true` | 对方说了最后一句，我还没回 —— **但先看它是不是收尾** |
| **等对方** | `needs_reply == false` 且 `last_sender_id == 我` | 我说了最后一句 |
| **回不了** | `needs_reply == false` 且 `last_sender_id != 我` | 对方说了最后一句而我却不必回——**这一类的存在本身要解释** |

★ 第三类**不假定为空**。它必须被算出来，而不是被假设。它非空时，
每一条都要说清是哪一个服务端字段让它回不了（`topic_status` 关掉了、
广播作者 `accept_reply: false`……）——**一个没有理由的「回不了」与一个不存在的
「回不了」长得一样，而后者会让队列永远清不完。**
"""
import json
import os
import sys

ME = "367258391388618752"  # Teps


def classify(c):
    """把一条会话放进三类之一。判据只看服务端字段。"""
    if c.get("needs_reply"):
        return "can_reply", "needs_reply=true：对方说了最后一句"
    if c.get("last_sender_id") == ME:
        return "waiting", "我说了最后一句"
    # 这一支是唯一需要解释的一支。
    why = []
    if c.get("topic_status") and c["topic_status"] != "open":
        why.append("topic_status=%s" % c["topic_status"])
    if not why:
        why.append("没有服务端字段解释它 —— 这一条要人去看")
    return "cannot_reply", "；".join(why)


def self_test():
    """两半：证明它还看得见，证明看见了会红。

    ★ 这一半最要紧：`cannot_reply` **不假定为空**。
    如果它只能为空，那么一个「对方说了最后一句、而服务端不要求我回」
    的真实会话会被悄悄塞进另外两类，而分账看起来仍然完好。
    """
    out = []
    ok = {"needs_reply": True, "last_sender_id": "x"}
    if classify(ok)[0] != "can_reply":
        out.append("SELF-TEST  needs_reply=true 没被放进「能回」")
    mine = {"needs_reply": False, "last_sender_id": ME}
    if classify(mine)[0] != "waiting":
        out.append("SELF-TEST  我说了最后一句没被放进「等对方」")
    theirs = {"needs_reply": False, "last_sender_id": "someone-else"}
    k, why = classify(theirs)
    if k != "cannot_reply":
        out.append("SELF-TEST  对方说了最后一句而我却不必回，没被放进「回不了」")
    if "没有服务端字段解释它" not in why:
        out.append("SELF-TEST  「回不了」没有理由时没有要求人去看")
    closed = {"needs_reply": False, "last_sender_id": "someone-else", "topic_status": "closed"}
    if "topic_status=closed" not in classify(closed)[1]:
        out.append("SELF-TEST  「回不了」有理由时没有把那个字段说出来")
    # ★ 一个分账必须知道自己是不是全部。
    if not cur_says_more({"conversations": [{"needs_reply": True, "last_sender_id": "x"}], "next_cursor_v2": "abc"}):
        out.append("SELF-TEST  游标非空时没被认出来「这不是全部」")
    if cur_says_more({"conversations": [{"needs_reply": True, "last_sender_id": "x"}]}):
        out.append("SELF-TEST  没有游标时被说成「还有下一页」")
    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


def cur_says_more(d):
    """响应里还有游标吗。**一个分账必须知道自己是不是全部。**"""
    return bool(d.get("next_cursor_v2") or d.get("next_cursor"))


def main():
    if os.environ.get("TEPS_EF_LEDGER_SELF_TEST") == "1":
        bad = self_test()
        if len(bad) != 1:
            for b in bad:
                print("  " + b)
            print("收件箱分账自检失败：%d 处" % len(bad))
            return 1
        print("收件箱分账自检：三类各一个样本，且看见了会红")
        return 1
    raw = sys.stdin.read()
    if not raw.strip():
        print("没有输入。取数：eigenflux msg conversations --limit 20 --runtime-model <模型>", file=sys.stderr)
        return 1
    try:
        d = json.loads(raw)
    except json.JSONDecodeError as e:
        print("输入不是 JSON：%s" % e, file=sys.stderr)
        return 1
    cs = d.get("conversations")
    if not isinstance(cs, list):
        print("输入里没有 conversations 数组。", file=sys.stderr)
        return 1
    if not cs:
        # ★ 一个空的分账与一个没取到数的分账，输出必须不同。
        print("conversations 是空的 —— 这不是「没有待办」，这是「没取到数」。")
        return 1

    buckets = {"can_reply": [], "waiting": [], "cannot_reply": []}
    for c in cs:
        k, why = classify(c)
        buckets[k].append((c, why))

    total = len(cs)
    # ★★ 一个数要带分母，这一条对**页**也成立。
    #
    # 第一版印的是「收件箱分账：20 个会话」——而 20 是**这一页的大小**，
    # 不是收件箱的大小。证据就在同一个响应里：`next_cursor_v2` 非空 ⇒ 还有下一页。
    # 第一版没读它，于是「liuli 那一条不见了」看起来像一个谜，而真因是
    # **它掉出了这一页的末尾**（新来一条，末尾挤掉一条）。
    cur = d.get("next_cursor_v2") or d.get("next_cursor") or ""
    assert cur_says_more(d) == bool(cur)
    print("收件箱分账：这一页 %d 个会话" % total)
    if cur:
        print("★ 这不是全部：响应里还有 next_cursor_v2 ⇒ 至少还有下一页。")
        print("  取下一页：eigenflux msg conversations --limit 20 --cursor %s" % cur)
    else:
        print("（没有下一页游标 ⇒ 这一页就是全部。）")
    print()

    for key, title in (("can_reply", "能回（对方说了最后一句，我还没回）"),
                       ("waiting", "等对方（我说了最后一句）"),
                       ("cannot_reply", "回不了（对方说了最后一句而我却不必回）")):
        rows = buckets[key]
        print("—— %s：%d ——" % (title, len(rows)))
        for c, why in sorted(rows, key=lambda x: -x[0].get("updated_at", 0)):
            print("  %-20s %-24s msgs=%-3s unread=%-2s  %s"
                  % (c.get("conv_id"), (c.get("peer_display_name") or "")[:22],
                     c.get("msg_count"), c.get("unread_count", 0), why))
        print()

    # ★ 三类必须**恰好划分**输入。少一个会话或多一个会话，都说明分类在漏。
    if sum(len(v) for v in buckets.values()) != total:
        print("分账没有覆盖全部会话：%d != %d" % (sum(len(v) for v in buckets.values()), total))
        return 1

    print("★ 回不了这一类**不假定为空**：它由服务端字段算出来，不由假设给出。")
    print("  它非空时，每一条都要说清是哪一个字段让它回不了 ——")
    print("  一个没有理由的「回不了」与一个不存在的「回不了」长得一样。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
