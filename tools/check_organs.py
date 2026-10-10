#!/usr/bin/env python3
"""器官守卫：organs/<x>/、两份文档的状态表、以及 ⬜ 的断言必须与代码一致。

规则
----
1. `organs/` 下每一个含 .go 的目录，都必须在 `organs/README.md` 里被写到，
   并且在根 `README.md` 里被写到。
2. 两份文档里出现的 `organs/<x>/` 或 `<x>/`（反引号内），都必须真的有那个目录。

为什么这是一条结构性规则
------------------------
这个项目已经为「同一个事实写两遍，只更新了一处」付过五次学费：
`check_contract.py` 的来历、`UnknownLicenseRefusal`、组合与 invoke 的 attempt
键后缀、组合的意图与结论用了不同的身份、以及 `compose.build` 与
`organs/composer` 抢同一个帧名。

器官清单是最容易漂的一处，因为**它写在两个地方，而两处都不报错**：
新建一个器官只需要写代码，文档不会因此变红；反过来删掉一个器官，
文档里那行也照样留着。所以状态表会安静地落后于代码——而那比没有状态表更糟，
因为读者会相信它。

这一台守卫和一个刚建好的器官之间的关系，与 `check_frames.py` 和一次改名之间的关系
是同一种：**它们都只在「两边放在一起看」时才看得见。**
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ORGANS_DIR = os.path.join(ROOT, "organs")
ORGAN_DOC = os.path.join(ORGANS_DIR, "README.md")
ROOT_DOC = os.path.join(ROOT, "README.md")

# 文档里显式提到器官目录的写法。
#
# 只认 `organs/xyz/` 这种**写明是器官**的形式。裸的 `xyz/` 会撞上一大堆
# 同名但不同层的东西（`evidence/` 是顶层包，`store/` 是另一个顶层目录，
# `organs/` 是父目录本身），第一版因此报了 3 处假阳性——
# 而假阳性会淹没真信号，这正是这台守卫要防的那种含糊。
REF_RE = re.compile(r"`organs/([a-z][a-z0-9_]*)/?`")


def discover():
    """organs/ 下每一个含 .go 文件的目录。"""
    found = []
    for name in sorted(os.listdir(ORGANS_DIR)):
        d = os.path.join(ORGANS_DIR, name)
        if not os.path.isdir(d):
            continue
        if any(f.endswith(".go") for f in os.listdir(d)):
            found.append(name)
    return found


def read(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except FileNotFoundError:
        print(f"器官守卫：找不到 {path}")
        sys.exit(2)


def main():
    organs = discover()
    organ_doc = read(ORGAN_DOC)
    root_doc = read(ROOT_DOC)

    problems = []

    # 方向一：每个真实存在的器官，两份文档的状态表里都要有一行。
    #
    # 「被提到过」太弱：根 README 里 `reflex` 出现在一张讲守卫漏洞的表里，
    # 而它的状态行是不存在的。名字出现过 ≠ 它有一行状态。
    # 所以只查**以 | 开头的行**——那才是状态表。
    def table(doc):
        # 只认**状态行**：以 | 开头，且写出了状态（✅ 或 ⬜）。
        # 光「在某张表里出现过」还是太弱——根 README 里 `reflex` 出现在一张
        # 讲守卫漏洞的表里，那一行没有状态，而 reflex 的状态行根本不存在。
        return "\n".join(
            l for l in doc.splitlines()
            if l.startswith("|") and ("✅" in l or "⬜" in l)
        )

    organ_table = table(organ_doc)
    root_table = table(root_doc)

    for name in organs:
        if not re.search(r"\b" + re.escape(name) + r"\b", organ_table):
            problems.append(f"organs/README.md 的状态行里没有器官 {name}/ 这一行")
        if not re.search(r"\b" + re.escape(name) + r"\b", root_table):
            problems.append(f"根 README.md 的状态行里没有器官 {name}/ 这一行")

    # 方向二：文档里写到的器官目录，都必须真的存在。
    referenced = set()
    for doc, label in ((organ_doc, "organs/README.md"), (root_doc, "根 README.md")):
        for m in REF_RE.finditer(doc):
            referenced.add((m.group(1), label))
    for name, label in sorted(referenced):
        if name not in organs:
            problems.append(f"{label} 里写到了 {name}/，但 organs/{name}/ 不存在")

    # 反向的平凡真也要说出来：一个器官都没有时，上面两条都成立而没有意义。
    if not organs:
        print("器官守卫：organs/ 下一个含 .go 的目录都没有。通过是平凡的真，不构成证据。")
        return 0

    # 方向三：⬜ 的断言不许指向一个真实存在的路径。
    #
    # 文档里唯一会主动说谎的地方是「这东西还没写」。代码不会让它变红，
    # 所以一句「⬜ 未写」可以在功能落地之后原封不动地留很久 ——
    # 契约里那张「实现 ⬜ 未写一行」的表就是这么留了十三个包的时间。
    PATH_RE = re.compile(r"`([A-Za-z0-9_./-]+(?:/[A-Za-z0-9_.-]*)?)`")
    docs = []
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in (".git", ".toolchain", ".scratch", "node_modules")]
        for fn in files:
            if fn.endswith(".md"):
                docs.append(os.path.join(base, fn))
    for doc in docs:
        rel = os.path.relpath(doc, ROOT)
        for i, line in enumerate(read(doc).splitlines(), 1):
            if "⬜" not in line:
                continue
            for tok in PATH_RE.findall(line):
                if not (("/" in tok) or tok.endswith((".go", ".py", ".sh"))):
                    continue
                if os.path.exists(os.path.join(ROOT, tok.rstrip("/"))):
                    problems.append(
                        f"{rel}:{i} 说某样东西还没做，但它提到的 {tok} 是存在的")

    # 方向四：实现状态表只许有一处。
    #
    # 这条规则针对的是一次真实事故：契约里那张表在实现完成之后一个字都没变。
    # 同一个事实写两遍，就会有一个地方没被改到。
    for doc in docs:
        rel = os.path.relpath(doc, ROOT)
        if rel == "README.md":
            continue
        for i, line in enumerate(read(doc).splitlines(), 1):
            if not (line.startswith("|") and "⬜" in line):
                continue
            cells = [c.strip() for c in line.strip().strip("|").split("|")]
            if not cells:
                continue
            # 首格以「实现」结尾 = 在声明这个组件的实现状态。
            # 契约里 `escalation.quote` 那种「某个帧尚未实现」是设计说明，
            # 不是项目状态，不该被这条规则误伤。
            if cells[0].endswith("实现"):
                problems.append(
                    f"{rel}:{i} 在维护一份实现状态表：状态只许有一处（README.md）")

    if problems:
        print(f"器官守卫：{len(problems)} 处不一致")
        for p in problems:
            print("  " + p)
        return 1

    print(f"器官守卫：{len(organs)} 个器官，两份文档都写到，且没有指向不存在的器官。")
    print("  " + " ".join(o + "/" for o in organs))
    return 0


if __name__ == "__main__":
    sys.exit(main())
