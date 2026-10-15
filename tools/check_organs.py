#!/usr/bin/env python3
"""器官守卫：`organs/<x>/` 与两份文档的状态表必须一致。

规则
----
1. `organs/` 下每一个含 .go 的目录，都必须在 `organs/README.md` 里被写到，
   并且在根 `README.md` 里被写到。
2. 两份文档里出现的 `organs/<x>/`（反引号内），都必须真的有那个目录。

★ 这台守卫**只管器官**。它原本还管三条与器官无关的文档规则
（`⬜` 不许指向存在的路径 · 实现状态表只许有一处 · `本地复现` 的 `✅` 必须
点名真实产物），那三条现在在 `tools/check_docs.py`。

为什么分：**一台守卫的名字是它失败时你怎么找到它。** 那三条红的时候，输出以
「器官守卫」开头，而问题在一份文档的某一格里——那是贴错了标签的信号。
本项目已经为贴错标签付过一次：根 README 里 `check_clients.py` 那一行的注释
写的是 `check_nomodel.py` 的活。

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


def table(doc):
    """只认**状态行**：以 | 开头，且写出了状态（✅ 或 ⬜）。

    光「在某张表里出现过」还是太弱——根 README 里 `reflex` 出现在一张
    讲守卫漏洞的表里，那一行没有状态，而 reflex 的状态行根本不存在。
    """
    return [l for l in doc.splitlines()
            if l.startswith("|") and ("✅" in l or "⬜" in l)]


# ★ 「这个器官有它自己的一行」的判据。
#
# 原本的判据是「器官名出现在某条状态行里」——而这条守卫自己的注释就写着那太弱。
# 确实弱：名字出现在**任何**一格都算，包括散文格、包括别人那一行的位置格。
# 一条把首格改成 XXXXX 的变异因此漏网：第三格里的 `arbiter/` 还在。
#
# 现在要求的是**位置引用**：反引号里以这个器官目录开头的路径
# （`` `arbiter/` `` 或 `` `executor/verify.go` ``）。位置格才是说「它住在哪」
# 的那一格，而「它住在哪」正是这一行在断言的事。
# 两种写法都要认：`organs/README.md` 里写 `` `session/` ``，
# 根 README 里写 `` `organs/session/` ``。**同一样东西在两份文档里的写法不同，
# 而判据只认一种，就会把另一种判成「缺了」。**（这条守卫在别处正是为了
# 防这种漂移才存在的——它不该自己制造一处。）
def names_organ(row, name):
    pat = r"`(?:organs/)?" + re.escape(name) + r"/[A-Za-z0-9_.\-]*`"
    return re.search(pat, row) is not None


def run():
    organs = discover()
    organ_doc = read(ORGAN_DOC)
    root_doc = read(ROOT_DOC)
    problems = []

    # 方向一：每个真实存在的器官，两份文档的状态表里都要有一行。
    organ_table = table(organ_doc)
    root_table = table(root_doc)
    for name in organs:
        if not any(names_organ(r, name) for r in organ_table):
            problems.append(f"organs/README.md 的状态行里没有器官 {name}/ 这一行")
        if not any(names_organ(r, name) for r in root_table):
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
        return ["organs/ 下一个含 .go 的目录都没有。通过是平凡的真，不构成证据。"], 0
    return problems, len(organs)


def self_test():
    """两半：证明它还看得见，证明看见了会红。

    ★ 用**自己造的行**当样本，不拿真实文档当样本——拿真实文档的自检
    证明的是「那份文档里恰好有一处违规」，不是「这条规则还看得见」。
    """
    out = []
    fake_organ = "checkorgansprobe"
    real_organ = sorted(
        n for n in os.listdir(ORGANS_DIR) if os.path.isdir(os.path.join(ORGANS_DIR, n))
    )[0]

    # 方向一：状态表里有那一行 ⇒ 不许红；没有 ⇒ 必须红。
    if not any(names_organ(r, real_organ) for r in table(f"| x | ✅ | `{real_organ}/` |")):
        out.append("SELF-TEST  方向一认不出位置格里的器官目录")
    if not any(names_organ(r, real_organ) for r in table(f"| x | ✅ | `{real_organ}/a.go` |")):
        out.append("SELF-TEST  方向一认不出器官目录里的文件")
    if not any(names_organ(r, real_organ) for r in table(f"| x | ✅ | `organs/{real_organ}/` |")):
        out.append("SELF-TEST  方向一认不出 `organs/<x>/` 这种写法（根 README 用的就是它）")
    if any(names_organ(r, fake_organ) for r in table(f"| x | ✅ | `{real_organ}/` |")):
        out.append("SELF-TEST  方向一在一个没有它的状态表里找到了器官")
    # ★ 名字出现在**散文格**里不算有自己的一行 —— 那正是旧判据漏掉的那种。
    if any(names_organ(r, real_organ) for r in table(f"| x | ✅ | 见 {real_organ} 那一节 |")):
        out.append("SELF-TEST  方向一把散文格里的名字当成了位置引用")

    # 方向二：`organs/<x>/` 能被抓出来。
    got = set(m.group(1) for m in REF_RE.finditer("见 `organs/session/` 与 `organs/nope/`"))
    if got != {"session", "nope"}:
        out.append(f"SELF-TEST  方向二读不出被引用的器官目录：{got}")
    # ★ 探针里**只能有**裸的 `x/`。第一版写成「见 `session/` 而不是
    # `organs/session/`」——那句话里两个形式都在，于是正则当然匹配，
    # 自检报的是一个我自己写出来的假失败。**样本里混进了被禁的东西，
    # 证明的是样本，不是规则。**
    if REF_RE.search("见 `session/` 与 `store/`"):
        out.append("SELF-TEST  方向二把裸的 `x/` 当成了器官引用（第一版的 3 处假阳性）")

    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


def main():
    if os.environ.get("TEPS_ORGANS_SELF_TEST") == "1":
        bad = self_test()
        if len(bad) != 1:
            for b in bad:
                print("  " + b)
            print(f"器官守卫自检失败：{len(bad)} 处")
            return 1
        print("器官守卫自检：还看得见（两条方向各两个样本），且看见了会红")
        return 1
    problems, n = run()
    if problems:
        print(f"器官守卫：{len(problems)} 处不一致")
        for p in problems:
            print("  " + p)
        return 1
    organs = discover()
    print(f"器官守卫：{n} 个器官，两份文档都写到，且没有指向不存在的器官。")
    print("  " + " ".join(o + "/" for o in organs))
    return 0


if __name__ == "__main__":
    sys.exit(main())
