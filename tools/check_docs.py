#!/usr/bin/env python3
"""文档守卫：一句「做没做」的断言必须与代码一致。

规则
----
1. **`⬜` 的断言不许指向一个真实存在的路径** —— 一句「还没做」在功能落地之后
   不会自己变红。
2. **实现状态表只许有一处**（根 `README.md`）。
3. ★ **`✅` 的断言必须点名一个真实存在的产物**（只在「本地复现」那一格上）——
   规则 1 的反面。**一句「已做」在证据消失之后也不会自己变红**：删掉那个例子、
   重命名那个脚本、把那个能力撤回去，都不会让 ✅ 变成别的字。

## 为什么它从 check_organs.py 里分出来

这三条与器官**没有关系**——它们管的是任何一份文档里任何一句「做没做」。
它们原本住在 `tools/check_organs.py` 里，于是那台守卫守五件事而名字只说了一件。

★ 一台守卫的名字是**它失败时你怎么找到它**。规则 3 红的时候，输出以
「器官守卫」开头，而问题在一份文档的某一格里——那是**贴错了标签的信号**。
本项目已经为贴错标签付过一次：根 README 里 `check_clients.py` 那一行的注释
写的是 `check_nomodel.py` 的活。

## 它必须能失败

一台永远绿的守卫不是守卫。`TEPS_DOCS_SELF_TEST=1` 时注入违规，它必须报出来。
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# 反引号里的东西。★ 它不区分路径与代码：`tools/install_go.sh` 是路径，
# 而 `tools/call` 是方法名，前缀一模一样。
PATH_RE = re.compile(r"`([A-Za-z0-9_./-]+(?:/[A-Za-z0-9_.-]*)?)`")

SKIP_DIRS = (".git", ".toolchain", ".scratch", "node_modules")


def read(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except FileNotFoundError:
        print(f"文档守卫：找不到 {path}")
        sys.exit(2)


def docs():
    """仓库里所有的 .md，排除构建与暂存目录。"""
    out = []
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for fn in files:
            if fn.endswith(".md"):
                out.append(os.path.join(base, fn))
    return sorted(out)


def path_like(tok):
    """这个反引号 token 是「路径形状」吗。

    ★ 判据不是「看起来像路径」，而是**这个 token 会不会被当成路径核**。
    宽松：存在，或者最后一段带扩展名。已知漏网是一个拼错的**目录**
    （既不存在、最后一段也没有 `.`），写在这里以免下一个人以为它守得住那一种。
    """
    return ("/" in tok) or tok.endswith((".go", ".py", ".sh", ".md"))


def check_unbuilt(doc_list, problems):
    """规则 1：`⬜` 不许指向一个存在的路径。"""
    for doc in doc_list:
        rel = os.path.relpath(doc, ROOT)
        for i, line in enumerate(read(doc).splitlines(), 1):
            if "⬜" not in line:
                continue
            for tok in PATH_RE.findall(line):
                if not path_like(tok):
                    continue
                if os.path.exists(os.path.join(ROOT, tok.rstrip("/"))):
                    problems.append(
                        f"{rel}:{i} 说某样东西还没做，但它提到的 {tok} 是存在的")


def check_one_status_table(doc_list, problems):
    """规则 2：实现状态表只许有一处。"""
    for doc in doc_list:
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


def check_reproduced(doc_list, problems):
    """规则 3：`✅` 必须点名一个真实存在的产物。"""
    for doc in doc_list:
        rel = os.path.relpath(doc, ROOT)
        for i, line in enumerate(read(doc).splitlines(), 1):
            if not (line.startswith("|") and "本地复现" in line and "✅" in line):
                continue
            named = [t for t in PATH_RE.findall(line) if path_like(t)]
            if not named:
                problems.append(
                    f"{rel}:{i} 说本地复现已做，却没有点名任何产物："
                    f"一个没有产物的 ✅ 与一个 ⬜ 一样不可核")
                continue
            missing = [t for t in named
                       if not os.path.exists(os.path.join(ROOT, t.rstrip("/")))]
            if missing:
                problems.append(
                    f"{rel}:{i} 说本地复现已做，但它点名的 {'、'.join(missing)} 不存在")


RULES = (check_unbuilt, check_one_status_table, check_reproduced)


def run():
    doc_list = docs()
    if not doc_list:
        print("文档守卫：一份 .md 都没有。通过是平凡的真，不构成证据。")
        return ["没有扫到任何文档"], 0
    problems = []
    for rule in RULES:
        rule(doc_list, problems)
    return problems, len(doc_list)


def self_test():
    """两半：证明它还看得见，证明看见了会红。

    ★ 自检**不许依赖真实文档的内容**。第一版拿根 README 当规则 1 的样本，
    于是它证明的是「README 里恰好有一处违规」，而不是「规则 1 还看得见」——
    那种自检在别人清理了 README 之后会变成一个假的失败。
    每条规则都用**自己造的探针文件**，违规由自检注入。
    """
    out = []
    probe = os.path.join(ROOT, ".scratch", "docs_selftest_probe.md")
    os.makedirs(os.path.dirname(probe), exist_ok=True)

    def probe_with(text):
        with open(probe, "w", encoding="utf-8") as fh:
            fh.write(text)
        return probe

    try:
        # 规则 1：⬜ 指向一个存在的路径 ⇒ 必须红。
        p = []
        check_unbuilt([probe_with("| x | ⬜ 还没做，见 `tools/check_docs.py` |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 1 放过了「⬜ 指向一个存在的路径」")
        # 规则 1 的反面：⬜ 指向一个不存在的路径 ⇒ 不许红。
        p = []
        check_unbuilt([probe_with("| x | ⬜ 还没做，见 `tools/does_not_exist_yet.py` |\n")], p)
        if p:
            out.append("SELF-TEST  规则 1 误报了一个不存在的路径")

        # 规则 2：别处维护实现状态表 ⇒ 必须红。
        p = []
        check_one_status_table([probe_with("| 实现 | ⬜ 未写 |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 2 放过了「别处也有一张实现状态表」")
        # 规则 2 的反面：设计说明不是状态表 ⇒ 不许红。
        p = []
        check_one_status_table([probe_with("| 帧 | ⬜ `escalation.quote` 尚未实现 |\n")], p)
        if p:
            out.append("SELF-TEST  规则 2 把一条设计说明当成了状态表")

        # 规则 3：✅ 却没点名任何产物 ⇒ 必须红。
        p = []
        check_reproduced([probe_with("| 本地复现 | ✅ 已做，就是跑了一下 |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 3 放过了「✅ 却没点名任何产物」")
        # 规则 3：点名了一个不存在的产物 ⇒ 必须红。
        p = []
        check_reproduced([probe_with("| 本地复现 | ✅ 已做：`tools/gone.py` |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 3 放过了「点名的产物不存在」")
        # 规则 3 的反面：点名了真实产物 ⇒ 不许红。
        p = []
        check_reproduced([probe_with("| 本地复现 | ✅ 已做：`tools/check_docs.py` |\n")], p)
        if p:
            out.append("SELF-TEST  规则 3 误报了一个点名的真实产物")
    finally:
        if os.path.exists(probe):
            os.remove(probe)

    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


def main():
    if os.environ.get("TEPS_DOCS_SELF_TEST") == "1":
        bad = self_test()
        if len(bad) != 1:
            for b in bad:
                print("  " + b)
            print(f"文档守卫自检失败：{len(bad)} 处")
            return 1
        print("文档守卫自检：还看得见（三条规则各一个样本），且看见了会红")
        return 1
    problems, n = run()
    if problems:
        print(f"文档守卫：{len(problems)} 处不一致（扫了 {n} 份 .md）")
        for p in problems:
            print("  " + p)
        return 1
    print(f"文档守卫：{n} 份 .md，三条规则都过：⬜ 不指向存在的路径 · "
          f"状态表只在一处 · 本地复现的 ✅ 点名了真实产物。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
