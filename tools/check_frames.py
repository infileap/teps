#!/usr/bin/env python3
"""帧守卫：适配器分派的每一个帧类型，契约里都必须有；反之亦然。

这条守卫的来历和 check_contract.py 是同一条，只是换了对象。
本项目已经四次栽在「同一个事实写了两遍、只有一处被更新」上：

  1. 文档的拒绝码表与 core/refusal.go 漂移        → 逼出了 check_contract.py
  2. Manifest.Validate 与 SolveLicense 对同一未知许可证给两个码
  3. 组合与 skill.invoke 的 attempt 键后缀不一致  → 回放守卫有个看不见的洞
  4. 我新加的 compose.build 与 organs/composer 抢同一个帧名 → composer 段七条断言全红

第 4 条就是这条守卫要防的：**帧类型是全局命名空间**。同一个词在同一个系统里指
两件事，代价总会有人付。而且它比前三条更难发现——两边都能跑，只有把两边放在
一起看才知道撞了。

只查「存在性」，不查语义：分派了但没写进契约 = 别人没法知道有这个 op；
写进契约但没人分派 = 文档在承诺一个不存在的入口。
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ADAPTERS = os.path.join(ROOT, "adapters")
CONTRACT = os.path.join(ROOT, "docs", "CONTRACT.md")

# strings.HasPrefix(f.T, "session.") —— 前缀族
PREFIX_RE = re.compile(r'HasPrefix\(f\.T,\s*"([a-z]+\.)"\)')
# case "session.open": 以及 case "memory.write", "memory.supersede": —— 具体帧。
# 多值 case 必须一起抓：只抓单值形式会漏掉 memory.write/supersede，
# 而漏掉的那两个正好是「写入不可变」这条公理的入口。
CASE_RE = re.compile(r'case\s+((?:"[a-z]+\.[a-z_]+"\s*,?\s*)+):')
CASE_TOK = re.compile(r'"([a-z]+\.[a-z_]+)"')
# if f.T != "skill.invoke" { —— 也是认领，只是写法不同
EQ_RE = re.compile(r'f\.T\s*[!=]=\s*"([a-z]+\.[a-z_]+)"')
# 契约里的 `x.y` 形式的帧名。
# 排除文件名：`drive.sh`、`check_kernel.py` 与帧名长得一模一样，
# 而守卫的第一次运行确实把它当成了一个帧 —— 假阳性会让真信号被淹没。
DOC_RE = re.compile(r"`([a-z]+\.[a-z_]+)`")
# 裸前缀 `session.` 也是在文档一个前缀族。
DOC_FAM_RE = re.compile(r"`([a-z]+\.)`")
FILE_EXT = (".sh", ".py", ".md", ".go", ".json", ".yml", ".yaml", ".txt")

# 一条明确标了「未实现」的帧不是静默漂移，是**已声明的缺口**。
# 守卫的职责是防静默，不是禁止诚实。
PENDING_MARK = ("未实现", "⬜")


def dispatched():
    """每一个被适配器认领的帧类型，以及它在哪个文件里被认领。"""
    found = {}
    for dirpath, _, files in os.walk(ADAPTERS):
        for fn in sorted(files):
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            path = os.path.join(dirpath, fn)
            rel = os.path.relpath(path, ROOT)
            body = open(path, encoding="utf-8").read()
            for pre in PREFIX_RE.findall(body):
                # 前缀族本身不是帧，但它承诺了「这个前缀下的帧归我」，
                # 所以契约里至少要提到这个前缀。
                found.setdefault(pre + "*", rel)
            for group in CASE_RE.findall(body):
                for frame in CASE_TOK.findall(group):
                    found.setdefault(frame, rel)
            for frame in EQ_RE.findall(body):
                found.setdefault(frame, rel)
    return found


def documented():
    """返回 (文档提到过的帧, 明确标为未实现的帧)。"""
    if not os.path.exists(CONTRACT):
        return set(), set()
    body = open(CONTRACT, encoding="utf-8").read()
    seen, pending = set(), set()
    for line in body.splitlines():
        fams = set(DOC_FAM_RE.findall(line))
        seen |= fams
        for t in DOC_RE.findall(line):
            if t.endswith(FILE_EXT):
                continue
            seen.add(t)
            if any(mark in line for mark in PENDING_MARK):
                pending.add(t)
    return seen, pending


def main():
    live = dispatched()
    doc, pending = documented()

    families = {f[:-1] for f in live if f.endswith("*")}
    concrete = {f for f in live if not f.endswith("*")}

    # 具体帧必须**按名字**出现在契约里。
    # 第一版让「前缀族被文档提过」替它下面的每一个帧背书，于是新增一个
    # reflex.teleport 照样通过 —— 注入测试证明那条捷径把守卫的牙拔了。
    # 前缀族只用于另一个方向：认领了整个前缀却一个字都没提。
    missing_doc = sorted(f for f in concrete if f not in doc)
    missing_family = sorted(f for f in families if f not in doc and f.rstrip(".") not in doc)

    # 反向：契约里写了、但没有任何适配器认领它。
    # 明确标了未实现的帧不算「承诺一个不存在的入口」——它承诺的是「这里还没有」。
    undocumented_live = sorted(
        f for f in doc
        if f not in concrete
        and f not in pending
        and not f.endswith(".")
    )

    bad = 0
    if missing_doc:
        print("契约缺这些帧（适配器分派了，但文档没写）：")
        for f in missing_doc:
            print(f"  {f}   ({live[f]})")
        bad += len(missing_doc)
    if missing_family:
        print("契约缺这些前缀族（适配器认领了一整个前缀，但文档没提）：")
        for f in missing_family:
            print(f"  {f}")
        bad += len(missing_family)
    if undocumented_live:
        print("契约写了但没人分派（文档在承诺一个不存在的入口）：")
        for f in undocumented_live:
            print(f"  {f}")
        bad += len(undocumented_live)

    if bad:
        print(f"\n共 {bad} 处不一致。帧类型是全局命名空间：撞了名两边都能跑，只有放在一起看才知道。")
        return 1
    print(f"帧类型一致：适配器分派 {len(concrete)} 个具体帧 + {len(families)} 个前缀族，契约里都有。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
