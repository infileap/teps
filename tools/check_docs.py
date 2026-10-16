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
4. ★★ **文档里的「N 个测试」必须与实测一致**，而且那个数必须点名它数的是哪个包。
   **规则 1–3 管的是「做没做」，没有一条管数字。** 而一个写在文档里的数
   在代码往前走之后不会自己变红——实测发现 **8 条里有 6 条是旧的**
   （`composer` 说 21 实测 25 · `memory` 说 15 实测 20 · `pool` 说 15 实测 16 ·
   `reflex` 说 16 实测 17 · `planner` 说 15 实测 16 · `executor` 说 21 实测 56）。
   ★ 一个数必须**点名**它数的是什么，而且只能点名一个：配不起来时报错，
   不猜。**一个不知道自己在数什么的数，比一个没有数更坏。**

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
import subprocess
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


# ★ 规则 4：文档里的「N 个测试」。
#
# 判据是**可算的**：那个包真的跑一遍，数 `=== RUN`。
# 不猜、不查表——一张「器官 → 测试数」的对照表就是被检查之物的第二份拷贝，
# 而第二份拷贝就是会烂掉的那一份（这条本项目写过很多次了）。
TEST_CLAIM_RE = re.compile(r"(\d+)\s*个测试")
_counts_cache = {}


def organ_packages():
    """`organs/` 下每个含 .go 的目录 → Go 包路径。"""
    base = os.path.join(ROOT, "organs")
    out = {}
    if not os.path.isdir(base):
        return out
    for name in sorted(os.listdir(base)):
        d = os.path.join(base, name)
        if os.path.isdir(d) and any(f.endswith(".go") for f in os.listdir(d)):
            out[name] = "./organs/" + name
    return out


def count_tests(pkg):
    if pkg in _counts_cache:
        return _counts_cache[pkg]
    env = dict(os.environ)
    env["PATH"] = os.path.join(ROOT, "tools") + os.pathsep + env.get("PATH", "")
    try:
        r = subprocess.run(["go", "test", pkg, "-count=1", "-v"], cwd=ROOT, env=env,
                           capture_output=True, text=True, timeout=900)
    except (FileNotFoundError, subprocess.TimeoutExpired) as exc:
        raise RuntimeError("跑不了 go test（%s）：一个跑不起来的守卫不是守卫" % exc)
    n = sum(1 for line in r.stdout.splitlines() if line.startswith("=== RUN"))
    _counts_cache[pkg] = n
    return n


def check_test_counts(doc_list, problems):
    """规则 4：`N 个测试` 必须与实测一致，且必须点名一个包。"""
    pkgs = organ_packages()
    for doc in doc_list:
        rel = os.path.relpath(doc, ROOT)
        for i, line in enumerate(read(doc).splitlines(), 1):
            claims = TEST_CLAIM_RE.findall(line)
            if not claims:
                continue
            named = [o for o in pkgs
                     if re.search(r"(?<![A-Za-z0-9_])" + o + r"(?![A-Za-z0-9_])", line)]
            if len(claims) != 1 or len(named) != 1:
                problems.append(
                    "%s:%d 这一行有 %d 个「个测试」的数和 %d 个器官名，配不起来："
                    "一个数必须点名它数的是什么，而且只能点名一个"
                    % (rel, i, len(claims), len(named)))
                continue
            organ, want = named[0], int(claims[0])
            got = count_tests(pkgs[organ])
            if want != got:
                problems.append(
                    "%s:%d 说 `%s/` 有 %d 个测试，实测 %d 个"
                    % (rel, i, organ, want, got))


RULES = (check_unbuilt, check_one_status_table, check_reproduced, check_test_counts)

# ★ 外部参照。规则**名字**在这里，函数在 RULES 里，两边必须一样长。
#
# 为什么需要它：自检里那句 `applied != len(RULES)` 拿 RULES 跟 RULES 自己比——
# **与自己一致不等于正确**。把一条规则从 RULES 里拿掉，`applied` 与 `len(RULES)`
# 一起变成 3，等式照样成立，那条规则就这样消失了而没有任何东西会红。
# 这与本项目在 decay_gap 上栽的是同一条：**要抓住它需要外部参照。**
RULE_NAMES = (
    "⬜ 不指向存在的路径",
    "状态表只在一处",
    "本地复现的 ✅ 点名了真实产物",
    "「N 个测试」与实测一致",
)
if len(RULES) != len(RULE_NAMES):
    raise SystemExit(
        "文档守卫：RULES 里有 %d 条而 RULE_NAMES 里有 %d 条 —— "
        "一条规则被拿掉了，或者一条规则没被写下来。"
        % (len(RULES), len(RULE_NAMES)))


def run():
    doc_list = docs()
    if not doc_list:
        print("文档守卫：一份 .md 都没有。通过是平凡的真，不构成证据。")
        return ["没有扫到任何文档"], 0, 0
    problems = []
    applied = 0
    for rule in RULES:
        rule(doc_list, problems)
        applied += 1
    # ★ 报出**真的跑了几条**。一条从 RULES 里被拿掉的规则，与一条通过的规则，
    # 输出完全一样——本项目在 check_all.sh 里已经为同一件事栽过一次
    # （「一个只验第一台的自检循环会让第二台的自检永远不被跑，而它坏掉的样子
    # 与「通过」一模一样：它什么都不打印」）。所以这个数是承重的。
    return problems, len(doc_list), applied


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

        # 规则 4：数与实测不符 ⇒ 必须红。★ 用 `breaker/`（实测 8 个）当样本，
        # 写一个显然不对的数——这条自检不依赖任何真实文档的当前内容。
        p = []
        check_test_counts([probe_with("| `breaker/` | ✅ 纯函数，999 个测试 |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 4 放过了「N 个测试与实测不符」")
        # 规则 4：数没有点名任何器官 ⇒ 必须红（不猜它数的是什么）。
        p = []
        check_test_counts([probe_with("| 全仓库 | ✅ 一共 123 个测试 |\n")], p)
        if not p:
            out.append("SELF-TEST  规则 4 放过了一个没点名任何器官的数")
        # 规则 4 的反面：数与实测一致 ⇒ 不许红。
        p = []
        check_test_counts([probe_with("| `breaker/` | ✅ 纯函数，8 个测试 |\n")], p)
        if p:
            out.append("SELF-TEST  规则 4 误报了一个正确的数")
    finally:
        if os.path.exists(probe):
            os.remove(probe)

    # ★★ 证明每一条规则**真的被调用**。
    #
    # 「从 RULES 里拿掉一条」与「那条通过」的输出完全一样——两半都要有：
    # 上面那些探针证明「规则还看得见」，这一条证明「规则还在跑」。
    _, _, applied = run()
    if applied != len(RULE_NAMES):
        out.append("SELF-TEST  只跑了 %d 条规则，而它声称有 %d 条："
                   "没被调用的规则与通过的规则输出一样" % (applied, len(RULE_NAMES)))

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
        print("SELF-TEST OK  文档守卫：还看得见（四条规则各一个样本），且看见了会红")
        return 1
    problems, n, applied = run()
    if problems:
        print(f"文档守卫：{len(problems)} 处不一致（扫了 {n} 份 .md，跑了 {applied} 条规则）")
        for p in problems:
            print("  " + p)
        return 1
    print(f"文档守卫：{n} 份 .md，{applied} 条规则都过：⬜ 不指向存在的路径 · "
          f"状态表只在一处 · 本地复现的 ✅ 点名了真实产物 · "
          f"「N 个测试」与实测一致")
    return 0


if __name__ == "__main__":
    sys.exit(main())
