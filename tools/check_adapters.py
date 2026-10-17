#!/usr/bin/env python3
"""适配器守卫：只有参考传输可以 import 内核。

一条 import 了 `teps/...` 的适配器就不再是适配器，而是内核的第二个入口。
第二个入口必然与第一个漂移——这个项目已经为此付过三次学费：

  1. docs/CONTRACT.md 的拒绝码表与 core/refusal.go 不一致（逼出 tools/check_contract.py）
  2. Manifest.Validate 与 SolveLicense 对同一个未知许可证给了两个码
  3. 适配器允许执行者自证 outcome（verified_by 那条守卫）

所以规则是结构性的，不是风格偏好：

  adapters/shell/   参考传输。它拥有内核，所以它**必须** import。
  adapters/<其他>/  翻译器。它们**不许** import 项目的任何包，只能走 NDJSON。

一个翻译器如果 import 了 store 或 organs，它就把内核的一部分复制到了自己身上；
再换一个宿主时，那部分不会跟着走。

退出码：0 通过，1 有违规。
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ADAPTERS = os.path.join(ROOT, "adapters")
REFERENCE = "shell"
MODULE = "teps/"

IMPORT_LINE = re.compile(r'^\s*(?:[A-Za-z_.]+\s+)?"([^"]+)"', re.M)


def go_files(d):
    out = []
    for dirpath, dirnames, filenames in os.walk(d):
        dirnames[:] = [x for x in dirnames if x != ".git"]
        for fn in filenames:
            if fn.endswith(".go"):
                out.append(os.path.join(dirpath, fn))
    return sorted(out)


def imports_of(path):
    src = open(path, encoding="utf-8").read()
    found = []
    for m in re.finditer(r'^import\s*\(([^)]*)\)', src, re.M | re.S):
        found += IMPORT_LINE.findall(m.group(1))
    for m in re.finditer(r'^import\s+(\S+)', src, re.M):
        token = m.group(1)
        if token.startswith('"') and token.endswith('"'):
            found.append(token[1:-1])
    return found


# ★★★ 证据阶梯只有一种读法。
#
# `evidence.Rank` 返回 `(秩, 在梯子上吗)`，而它的注释逐字写着为什么第二个值
# 不能丢：
#
#   An unknown level is deliberately **not** mapped to zero. Zero is a real level
#   (E0), and folding "E9" onto it would turn a typo into a trust decision.
#
# 这个项目里它被丢了**两次**（`store/manifest.go` 的 `evidenceRank`、
# `adapters/shell/compose.go` 的 `rankOf`），两次都是 `r, _ := evidence.Rank(l)`，
# 而两次的注释都声称自己读的是**唯一那扇门**：
#
#   rankOf:       "reads the ladder through one door. The store has its own
#                  forwarder for the same reason"
#   evidenceRank: "the store reads the ladder through exactly one door (manifest.go)"
#
# 两句都说只有一扇，而当时有三扇。**一句「只有一扇门」的注释不会让门变成一扇。**
# 所以这条规则不写进注释，写进守卫：第二个返回值不许被丢掉。
DISCARDED_OK = re.compile(r'\w+\s*,\s*_\s*:?=\s*evidence\.Rank\(')


def strip_comments(src):
    """去掉 Go 源码里的注释，保留行号（注释行变成空行）。

    ★★★ 这条剥离是**必须**的，而第一版没有它 —— 于是守卫在它自己的
    文档上报警：`store/manifest.go` 里那句「它此前丢掉了 Rank 的第二个
    返回值：`r, _ := evidence.Rank(l)`」是**注释**，而守卫把它当成了代码。
    `adapters/shell/compose.go` 里那段解释 `rankOf` 的注释同样中招。

    **一个读注释的守卫报的是散文，不是代码。** 这与
    `check_assertions.py` 第一版是同一类：它用截断来判定，于是报的是它
    截断的方式。判据必须先落在「这是不是代码」上。

    字符串字面量里的 `//` 不算注释，所以要真的扫一遍，不能用正则切。
    """
    n = len(src)
    # 逐行重建，注释处留空行，行号因此不变。
    kept = []
    i, start = 0, 0
    buf = []
    while i < n:
        c = src[i]
        if c == '"':
            j = i + 1
            while j < n and src[j] != '"':
                if src[j] == "\\":
                    j += 1
                j += 1
            buf.append(src[i:j + 1])
            i = j + 1
            continue
        if c == "`":
            j = i + 1
            while j < n and src[j] != "`":
                j += 1
            buf.append(src[i:j + 1])
            i = j + 1
            continue
        if c == "/" and i + 1 < n and src[i + 1] == "/":
            while i < n and src[i] != "\n":
                i += 1
            continue
        if c == "/" and i + 1 < n and src[i + 1] == "*":
            i += 2
            while i + 1 < n and not (src[i] == "*" and src[i + 1] == "/"):
                if src[i] == "\n":
                    buf.append("\n")
                i += 1
            i += 2
            continue
        if c == "\n":
            kept.append("".join(buf))
            buf = []
            i += 1
            continue
        buf.append(c)
        i += 1
    kept.append("".join(buf))
    return "\n".join(kept)


def check_rank_is_read_once():
    """`evidence.Rank` 的第二个返回值不许被丢掉。

    ★ 这条判据是**可算的**：一个 `_` 就是一个被丢掉的事实，不需要读懂语义。
    ★ 但它必须先在**代码**上算 —— 见 strip_comments 的注释。
    """
    problems = []
    for path in go_files(ROOT):
        rel = os.path.relpath(path, ROOT)
        if rel.endswith("_test.go") or rel.startswith("tools/"):
            continue
        if "/.toolchain/" in rel or rel.startswith(".toolchain/"):
            continue
        code = strip_comments(open(path, encoding="utf-8").read())
        for i, line in enumerate(code.splitlines(), 1):
            if DISCARDED_OK.search(line):
                problems.append(
                    f"{rel}:{i} 丢掉了 evidence.Rank 的第二个返回值（在梯子上吗）——"
                    " 未知等级会因此被当成 E0")
    return problems


# ★★★ 自检：这一条判据必须**还看得见**。
#
# 而它有两个前提，都是**纯的**（只吃字符串），所以自检不需要碰盘：
#   ① `strip_comments(src)` 必须真的剥掉注释 —— **但字符串字面量里的 `//` 不算注释**；
#   ② `DISCARDED_OK` 必须真的匹配 `r, _ := evidence.Rank(l)`，
#      而**不许**匹配 `r, ok := evidence.Rank(l)`（那是正确写法）。
#
# ★★ 而 ① 不是可选的：这个守卫的**第一版在它自己的文档上报警** ——
# `store/manifest.go` 里那句「它此前丢掉了 `Rank` 的第二个返回值：
# `r, _ := evidence.Rank(l)`」是**注释**。**一个读注释的守卫报的是散文，不是代码。**
# 所以自检必须把「注释里的那个形状」也钉住。
def self_test():
    bad = []

    # ① strip_comments：注释要没，字符串里的 `//` 要留。
    src = 'x := 1 // r, _ := evidence.Rank(l)\n' \
          'y := "http://example.com"\n'
    out = strip_comments(src)
    if "evidence.Rank" in out:
        bad.append("自检：strip_comments 没有剥掉行尾注释 —— 守卫会去报散文")
    if "http://example.com" not in out:
        bad.append("自检：strip_comments 把字符串字面量里的 // 也当成了注释")

    # ② DISCARDED_OK：丢掉第二个返回值的要匹配，没丢的不许匹配。
    if not DISCARDED_OK.search('r, _ := evidence.Rank(l)'):
        bad.append("自检：DISCARDED_OK 没匹配 `r, _ := evidence.Rank(l)` —— 它看不见了")
    if DISCARDED_OK.search('r, ok := evidence.Rank(l)'):
        bad.append("自检：DISCARDED_OK 匹配了 `r, ok := ...` —— 那是正确写法，不是违规")

    # ③ imports_of：吃路径，用临时文件喂。
    import tempfile
    from pathlib import Path
    with tempfile.TemporaryDirectory() as td:
        f = Path(td) / "a.go"
        f.write_text('package a\n\nimport (\n\t"teps/core"\n)\n\nimport "os"\n', encoding="utf-8")
        got = imports_of(str(f))
        for want in ("teps/core", "os"):
            if want not in got:
                bad.append(f"自检：imports_of 没看见 {want}")
    return bad


def main():
    if os.environ.get("TEPS_ADAPTERS_SELF_TEST") == "1":
        bad = self_test()
        if bad:
            for b in bad:
                print("  " + b)
            print(f"适配器守卫自检失败：{len(bad)} 处")
            return 1
        print("SELF-TEST OK  适配器守卫：剥注释与丢返回值两条前提都还看得见")
        return 1
    if not os.path.isdir(ADAPTERS):
        print("check_adapters: 没有 adapters/ 目录")
        return 1

    names = sorted(
        d for d in os.listdir(ADAPTERS)
        if os.path.isdir(os.path.join(ADAPTERS, d)) and not d.startswith(".")
    )
    if REFERENCE not in names:
        print(f"check_adapters: 找不到参考传输 adapters/{REFERENCE}/")
        return 1

    violations = []
    for name in names:
        d = os.path.join(ADAPTERS, name)
        project = []
        for f in go_files(d):
            for imp in imports_of(f):
                if imp.startswith(MODULE):
                    project.append(f"{os.path.relpath(f, ROOT)}: {imp}")

        if name == REFERENCE:
            # 参考传输必须真的拥有内核；否则「只有它能 import」这条规则是空的。
            if not project:
                violations.append(
                    f"adapters/{REFERENCE}/ 没有 import 任何 {MODULE}* —— "
                    "参考传输应当拥有内核，这条规则否则是平凡的真"
                )
            continue

        for line in project:
            violations.append(
                f"{line}\n"
                f"      adapters/{name}/ 是翻译器，不许 import 项目的包。\n"
                "      翻译器只许走 NDJSON；一旦它认识内核的概念，换宿主时那部分不会跟着走。"
            )

    print(f"适配器守卫   adapters/ = {', '.join(names)}")
    print()
    print(f"  参考传输：{REFERENCE}（拥有内核，必须 import）")
    others = [n for n in names if n != REFERENCE]
    if others:
        print(f"  翻译器：  {', '.join(others)}（不许 import 项目的任何包）")
    print()

    if violations:
        print(f"发现 {len(violations)} 处违规：")
        for v in violations:
            print(f"  ✗ {v}")
        return 1

    discarded = check_rank_is_read_once()
    if discarded:
        print("证据阶梯被读错（evidence.Rank 的第二个返回值被丢掉）：")
        for d in discarded:
            print(f"  ✗ {d}")
        return 1

    print("适配器边界通过。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
