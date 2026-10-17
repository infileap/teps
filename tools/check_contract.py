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


# ★★★ 契约点名过的每一个清单字段，要么**有人执行**，要么**标明没人执行**。
#
# 这一条是被 `idempotent` / `effects` / `preconditions` / `postconditions` /
# `on_failure` 逼出来的：它们都在 `docs/CONTRACT.md` 的 Skill ABI 那一行里，
# 而实测（非测试代码里的读次数）是 **0**。
#
# 一个字段被点名却没人执行，只有**写下来**才算「已声明的缺口」，否则它读起来
# 像一条承诺 —— 与帧表那条（「契约写了但没人分派 = 文档在承诺一个不存在的
# 入口」）是同一条，只是对象换成了字段。
#
# 判据是**可算的**：
#   1. 从清单结构体里取每一个 `json:"名字"` 标签；
#   2. 把它转成 Go 字段名，数非测试代码里的读（`.Name` 后面不是 `=`）；
#   3. 读次数为 0 时，契约的 ABI 段里必须有一个带 `⬜` 的行提到那个 json 名字。
MANIFEST_TAG = re.compile(r'^\t([A-Z]\w*)\s+[^`]*`json:"([^",]+)')


def manifest_fields():
    """清单结构体里的 (Go 字段名, json 名字) 对。"""
    src = read("store/manifest.go")
    out = []
    inside = False
    for line in src.splitlines():
        if re.match(r'^type Manifest struct \{', line):
            inside = True
            continue
        if inside and line.startswith("}"):
            break
        if not inside:
            continue
        m = MANIFEST_TAG.match(line)
        if m:
            out.append((m.group(1), m.group(2)))
    return out


def unenforced_manifest_fields():
    contract = read("docs/CONTRACT.md")
    # ABI 那一段：从 `### Skill ABI` 到下一个 `###`
    start = contract.find("### Skill ABI")
    end = contract.find("###", start + 1)
    abi = contract[start:end] if start >= 0 else ""
    # ★★★ 「标了」必须是**那一行带 ⬜**，不是「那个名字在这一段里出现过」。
    #
    # 第一版取的是整个 ABI 段里的反引号名字 —— 而字段清单本身就写着
    # `effects[]`，于是**去掉表格里那一行的 ⬜ 之后守卫照样绿**：
    # 判据被字段名自己满足了。实测确认过（删掉 effects 那一行的 ⬜，退出码仍是 0）。
    #
    # **一个被「名字出现过」满足的判据，检查的是存在性，不是那件事。**
    # 与 Round 43 的「认领了却没处理」是同一条：claimed-vs-unclaimed
    # 变成了 claimed-and-handled-vs-claimed-and-empty。
    marked = set()
    for line in abi.splitlines():
        if "\u2b1c" not in line:
            continue
        marked |= set(re.findall(r'`([a-z_]+)[^`]*`', line))
    problems = []
    for go_name, json_name in manifest_fields():
        reads = 0
        # ★ 定义它的那个文件也要算 —— `Manifest.Validate` 就住在
        # `store/manifest.go` 里，而 `m.Version == ""` 是一次**读**。
        # 第一版把整个文件排除掉了，于是 `Version` 被误报。
        # **这与 Round 55 的 `check_exported_functions_have_callers` 是同一个坑**：
        # 排除定义文件会把「同一个文件里的使用者」当成不存在。
        # 结构体声明那一段要抹掉，否则每个字段都会「用到」自己。
        for path in go_files():
            body = open(path, encoding="utf-8").read()
            if path.endswith("store/manifest.go"):
                body = re.sub(r'^type Manifest struct \{.*?^\}', '', body, flags=re.M | re.S)
            for m in re.finditer(r'\.' + go_name + r'\b', body):
                tail = body[m.end():m.end() + 3].lstrip()
                if tail.startswith("=") and not tail.startswith("=="):
                    continue
                reads += 1
        if reads > 0:
            continue
        if json_name in marked:
            continue
        problems.append(
            f"清单字段 {go_name}（json:{json_name}）在非测试代码里一次都没被读，"
            f"而契约的 Skill ABI 那一段没有用 `\u2b1c` 标出这件事")
    return problems


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

    # ★★★ 契约点名过的每一个清单字段，要么有人执行，要么标明没人执行。
    unenforced = unenforced_manifest_fields()
    if unenforced:
        print("这些清单字段被契约点名、而没有任何地方读它们（要么接上，要么在 ABI 段里标 ⬜）：")
        for u in unenforced:
            print(f"  {u}")
        bad += len(unenforced)

    print(f"  拒绝码 {len(codes)} 个，文档列出 {len(doc_codes)} 个。")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
