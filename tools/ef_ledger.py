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
        out.append("SELF-TEST  游标非空时没被认出来「还没取完」")
    # ★ 游标非空**不等于**还有下一页：实测第二页的游标取回来是 0 条。
    #   所以这个函数的名字是 says_more（「还没取完」），不是 has_more（「还有东西」）。
    if cur_says_more({"conversations": [], "next_cursor_v2": "abc"}) is not True:
        out.append("SELF-TEST  空的一页带着游标时，没被认出来「还没取完」")
    # ★ 取完的那一页：`next_cursor_v2` 空、遗留字段是哨兵 "0"。
    #   第一版的回退把它读成了「还有」——一条回退就是一处会漂移的判据。
    if cur_says_more({"conversations": [], "next_cursor": "0", "next_cursor_v2": ""}):
        out.append("SELF-TEST  取完的那一页（next_cursor 是哨兵 \"0\"）被读成了「还有」")
    if not cur_says_more({"conversations": [], "next_cursor": "1791635754373"}):
        out.append("SELF-TEST  没有 v2 键而遗留游标是真游标时，没被认出来")
    if cur_says_more({"conversations": [{"needs_reply": True, "last_sender_id": "x"}]}):
        out.append("SELF-TEST  没有游标时被说成「还有下一页」")
    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


# ★ `next_cursor` 是**遗留字段**，而它的「没有下一页」值是字符串 `"0"`——
#   不是空串。实测取完的那一页长这样：
#
#       {"conversations": [], "next_cursor": "0", "next_cursor_v2": ""}
#
#   第一版写的是 `d.get("next_cursor_v2") or d.get("next_cursor")`，
#   于是**取完的那一页被判成「还没取完」**：`next_cursor_v2` 是空的，
#   回退到 `next_cursor`，而 `"0"` 是真值。
#   ⇒ 一个回退，把「取完了」读成了「还有」。**两个字段说的是同一件事，
#   而它们的「没有」写法不同；只认一种写法就会把另一种判成「有」。**
STOP_SENTINELS = ("", "0")


def cur_says_more(d):
    """响应里还有**下一页游标**吗。**一个分账必须知道自己是不是全部。**

    只看 `next_cursor_v2`（真正的分页游标）。`next_cursor` 是遗留字段，
    它的「没有下一页」值是 `"0"`，所以它不能用来判「还有」。
    """
    v2 = d.get("next_cursor_v2")
    if v2 is not None:
        return v2 not in STOP_SENTINELS
    # 没有 v2 这个键时才看遗留字段，并且认出它的停止哨兵。
    return d.get("next_cursor") not in (None,) + STOP_SENTINELS


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
    # ★ 收数由人做（EigenFlux 的命令必须一条一条直接跑），
    #   而**合并**由这个工具做：把几页喂进来，它报并集。
    #   只喂一页时，输出必须仍然说清「这是这一页」——所以这里两种都支持。
    files = [a for a in sys.argv[1:] if not a.startswith("-")]
    docs = []
    if files:
        for f in files:
            try:
                docs.append(json.load(open(f, encoding="utf-8")))
            except (OSError, json.JSONDecodeError) as e:
                print("读不了 %s：%s" % (f, e), file=sys.stderr)
                return 1
    else:
        raw = sys.stdin.read()
        if not raw.strip():
            print("没有输入。取数：eigenflux msg conversations --limit 20 --runtime-model <模型>",
                  file=sys.stderr)
            return 1
        try:
            docs.append(json.loads(raw))
        except json.JSONDecodeError as e:
            print("输入不是 JSON：%s" % e, file=sys.stderr)
            return 1

    # ★ 并集按 `conv_id` 去重。同一个会话出现在两页里是可能的（服务端在翻页之间动了），
    #   而**同一个会话数两次会让分账比收件箱大**——那种错看起来只是数字偏大。
    seen = {}
    pages = 0
    for d in docs:
        cs = d.get("conversations")
        if not isinstance(cs, list):
            print("输入里没有 conversations 数组。", file=sys.stderr)
            return 1
        if not cs:
            continue
        pages += 1
        for c in cs:
            cid = c.get("conv_id")
            if cid:
                seen[cid] = c
    if not seen:
        # ★ 一个空的分账与一个没取到数的分账，输出必须不同。
        print("conversations 是空的 —— 这不是「没有待办」，这是「没取到数」。")
        return 1
    cs = list(seen.values())

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
    # 游标取**最后一份**输入（它是这一串里最新的那一页）。
    last = docs[-1]
    cur = last.get("next_cursor_v2") or ""
    if not cur and last.get("next_cursor") not in (None,) + STOP_SENTINELS:
        cur = last.get("next_cursor")
    assert cur_says_more(last) == bool(cur)
    if len(docs) > 1:
        print("收件箱分账：%d 页、去重后 %d 个会话" % (pages, total))
    else:
        print("收件箱分账：这一页 %d 个会话" % total)
    if cur:
        # ★★ 一个游标非空**不等于**还有下一页。
        #
        # 这里第一版印的是「⇒ 至少还有下一页」，而那比它核过的更强。
        # 实测：第二页带游标，用那个游标取第三页拿到的是 **0 条、无游标**。
        # 游标许可的是「你可以再问一次」，不是「还有东西」。
        # 一条把自己的话说过头的输出，与一条不检查的输出是同一种失败：
        # 读的人无从分辨。
        print("★ 还有游标 ⇒ 请用它再问一次。**游标非空不等于还有下一页**——")
        print("  实测第二页的游标取回来是 0 条、且 `next_cursor_v2` 变空。")
        print("  **「取全了」的判据是 `next_cursor_v2` 为空**，不是「游标非空」的反面。")
        print("  再问一次：eigenflux msg conversations --limit 20 --cursor %s" % cur)
    else:
        print("（没有游标 ⇒ 这一页就是全部。）")
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
