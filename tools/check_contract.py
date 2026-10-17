#!/usr/bin/env python3
"""契约守卫：文档里的拒绝码表必须与 core/refusal.go 逐字一致。

这条守卫是被一次真实漂移逼出来的——
drive.sh 断言 `unknown_session`，内核返回 `no_session`，而文档也写着 `unknown_session`。
两边分头演化时谁都不会报错，除非有人把它们放在一起比。
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

CONST_RE = re.compile(r'(Code\w+)\s*=\s*"([a-z_]+)"')
RETRY_RE = re.compile(r"(Code\w+):\s*(true|false)")
ROW_RE = re.compile(r"^\|\s*`([a-z_]+)`\s*\|([^|]*)\|\s*(✅|❌)", re.M)


def read(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
        return f.read()


def go_files():
    """仓库里所有非测试的 .go 文件，跳过 .toolchain 与 tools/。"""
    out = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in (".git", ".toolchain")]
        for fn in filenames:
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            out.append(os.path.join(dirpath, fn))
    return sorted(out)


# ★★★ 一个没有生产点的拒绝码是一个承诺。
#
# 这张表的用法是「适配器作者据此写循环」。于是 `backlog_full` 那一个
# `✅ 退避后重试` 在承诺一次**永远不会到来**的重试 —— 因为没有任何地方会
# 返回这个码。实测：14 个码里有 **5 个**生产点是 0（`stale_epoch`
# `backlog_full` `tainted_input` `quota_exhausted` `shadow_nested`）。
#
# 这与帧表里「写进契约但没人分派 = 文档在承诺一个不存在的入口」是同一条。
# 处置也同一条：**明确标出来的缺口不是静默漂移** —— 契约那一行带上
# `未实现` 或 `⬜` 就不算违规，守卫防的是**静默**，不是禁止诚实。
#
# 判据是**可算的**：`CodeXxx` 这个标识符在 core/refusal.go 之外出现过吗。
# 一个只出现在常量块与 retryable 表里的码，没有任何地方会返回它。
PENDING_MARK = ("未实现", "⬜")


def producers(names):
    """每个码名在 core/refusal.go 之外被引用的次数。"""
    counts = {name: 0 for name in names}
    refusal = os.path.join(ROOT, "core", "refusal.go")
    for path in go_files():
        if os.path.abspath(path) == os.path.abspath(refusal):
            continue
        body = open(path, encoding="utf-8").read()
        for name in names:
            counts[name] += body.count(name)
    return counts


def main():
    src = read("core/refusal.go")

    # The const block gives name -> code; the retryable map keys by name,
    # so it has to be joined through that mapping rather than read directly.
    names = dict(CONST_RE.findall(src))
    codes = set(names.values())

    retry = {}
    for name, val in RETRY_RE.findall(src):
        if name in names:
            retry[names[name]] = (val == "true")

    contract = read("docs/CONTRACT.md")
    rows = ROW_RE.findall(contract)
    doc_codes = {c for c, _, _ in rows}
    doc_retry = {c: (mark == "✅") for c, _, mark in rows}

    # 每一行原文，用来判断它有没有被标成已声明的缺口。
    row_line = {}
    for line in contract.splitlines():
        m = ROW_RE.match(line)
        if m:
            row_line[m.group(1)] = line

    bad = 0

    for c in sorted(codes - doc_codes):
        print(f"  文档缺拒绝码：{c}")
        bad += 1
    for c in sorted(doc_codes - codes):
        print(f"  文档多拒绝码：{c}（core/refusal.go 里没有）")
        bad += 1

    for c in sorted(codes & doc_codes):
        if c not in retry:
            print(f"  core 的 retryable 表里没有 {c}")
            bad += 1
            continue
        if doc_retry[c] != retry[c]:
            print(f"  可重试列不一致：{c} 文档={doc_retry[c]} core={retry[c]}")
            bad += 1

    # ★★★ 每个码要么有生产点，要么在契约里被标成已声明的缺口。
    name_of = {v: k for k, v in names.items()}
    counts = producers(name_of.values())
    orphan = []
    for c in sorted(codes):
        name = name_of[c]
        if counts.get(name, 0) > 0:
            continue
        if any(mark in row_line.get(c, "") for mark in PENDING_MARK):
            continue
        orphan.append(c)
    if orphan:
        print("  这些拒绝码没有任何地方会返回（要么加生产点，要么在契约里标 ⬜ 未实现）：")
        for c in orphan:
            print(f"    {c}")
        bad += len(orphan)

    print(f"  拒绝码 {len(codes)} 个，文档列出 {len(doc_codes)} 个。")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
