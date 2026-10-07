#!/usr/bin/env python3
"""内核守卫 —— 把"内核必须微小"从承诺变成读数。

用法:
    python3 tools/check_kernel.py            # 跑四条守卫
    python3 tools/check_kernel.py --report   # 额外打印判据本身
    python3 tools/check_kernel.py --strict   # 内核尚未声明四个概念即判失败（CI 用）

为什么是这四条:

  真正的判据是**状态量**——升级内核的痛点全在迁移它持有的状态。
  但"状态量"无法机械测量。所以这里守四个可测量的代理量,
  外加一条直指病因的线。

  第 4 条最强: **行数超标是症状, 领域词泄漏是病因。**
  如果"技能""策略""增益"出现在内核里, 说明外设的职责漏进来了——
  那比行数超了严重得多, 而且出现得更早。
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys
import unicodedata


def _width(s: str) -> int:
    """显示宽度: CJK 占两列。不做这个, 表头和数据行会错开。"""
    return sum(2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1 for ch in s)


def pad(s: str, n: int) -> str:
    return s + " " * max(0, n - _width(s))


ROOT = pathlib.Path(__file__).resolve().parent.parent
CORE = ROOT / "core"
GOMOD = ROOT / "go.mod"

# 参与扫描的源文件扩展名。**刻意不含 .md**:
# 说明文档必须能自由写出被禁的词来解释禁令, 否则没法写文档。
# 但 Go 的注释在源文件里, 注释中出现被禁的词同样算泄漏 —— 这是有意的。
SRC_EXT = {".go"}

LIMITS = {
    "lines": 3000,
    "deps": 0,
    "public_types": 12,
}

# 领域词词干。subword 以这些前缀开头即算命中。
# 刻意用前缀: "escalat" 命中 escalate/escalation, "compos" 命中 compose/composition。
# 刻意不用无边界子串: "gain" 会误伤 "bargain"/"against"/"explain"。
# 增删词条需要论证 —— 想在内核里用某个词, 先回答"这个概念为什么必须唯一权威/原子/必经"。
DOMAIN_WORDS = [
    "skill",
    "strateg",
    "gain",
    "escalat",
    "arbitrat",
    "arbiter",
    "compos",
    "eviden",
    "memor",
    "verif",
]

# 宿主词。内核不许知道自己在谁家里运行。
HOST_WORDS = ["dsh", "cordis", "harness", "plugin"]

# camelCase / snake_case 切分: TotalGain -> total,gain;  bargain -> bargain
_CAMEL = re.compile(r"[A-Z]+(?![a-z])|[A-Z][a-z0-9]*|[a-z0-9]+")
_RUN = re.compile(r"[A-Za-z0-9_]+")


def subwords(text: str):
    """把文本切成小写子词, 保留 camelCase 边界。

    这是第 4 条守卫能真正工作的前提: 天真的子串匹配看不见 `TotalGain`。
    """
    for run in _RUN.findall(text):
        for part in re.split(r"[_0-9]+", run):
            for word in _CAMEL.findall(part):
                yield word.lower()


def source_files():
    if not CORE.is_dir():
        return []
    return sorted(
        p
        for p in CORE.rglob("*")
        if p.is_file() and p.suffix in SRC_EXT
    )


def count_lines(files):
    total = 0
    for path in files:
        if path.name.endswith("_test.go"):
            continue
        for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
            if line.strip():
                total += 1
    return total


def go_direct_requires(text: str):
    """返回 go.mod 里的直接依赖 + replace 目标。

    require 和 replace 都算耦合: replace 能把一个依赖换成本地目录,
    等于把外部代码搬进内核。
    """
    found = []
    in_require = False
    in_replace = False
    for raw in text.splitlines():
        line = raw.split("//")[0].strip()
        if not line:
            continue
        if in_require:
            if line == ")":
                in_require = False
                continue
            found.append(line.split()[0])
            continue
        if in_replace:
            if line == ")":
                in_replace = False
                continue
            found.append("replace " + line.split()[0])
            continue
        if line.startswith("require"):
            rest = line[len("require"):].strip()
            if rest.startswith("("):
                in_require = True
            elif rest:
                found.append(rest.split()[0])
            continue
        if line.startswith("replace"):
            rest = line[len("replace"):].strip()
            if rest.startswith("("):
                in_replace = True
            elif rest:
                found.append("replace " + rest.split()[0])
    return found


_TYPE_DECL = re.compile(r"^type\s+([A-Za-z_]\w*)\b")
_TYPE_SPEC = re.compile(r"^([A-Za-z_]\w*)\b")


def public_types(files):
    """收集公开（大写开头）的类型名, 处理 `type (` 块。"""
    names = []
    for path in files:
        in_block = False
        for raw in path.read_text(encoding="utf-8", errors="replace").splitlines():
            line = raw.split("//")[0].rstrip()
            if in_block:
                if line.strip().startswith(")"):
                    in_block = False
                    continue
                m = _TYPE_SPEC.match(line.strip())
                if m:
                    names.append((m.group(1), path))
                continue
            if re.match(r"^type\s*\(\s*$", line):
                in_block = True
                continue
            m = _TYPE_DECL.match(line)
            if m:
                names.append((m.group(1), path))
    return [(n, p) for n, p in names if n[:1].isupper()]


def word_hits(files, stems):
    """返回 (文件, 行号, 子词, 原行) 命中列表。"""
    hits = []
    for path in files:
        for lineno, raw in enumerate(
            path.read_text(encoding="utf-8", errors="replace").splitlines(), 1
        ):
            for word in subwords(raw):
                for stem in stems:
                    if word.startswith(stem):
                        hits.append((path, lineno, word, raw.strip()))
                        break
    return hits


def rel(path):
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path)


def main() -> int:
    ap = argparse.ArgumentParser(description="内核四条守卫")
    ap.add_argument("--report", action="store_true", help="额外打印判据本身")
    ap.add_argument("--strict", action="store_true", help="内核尚未落地即判失败（CI 用）")
    args = ap.parse_args()

    files = source_files()
    failures = []

    if args.report:
        print("判据: 只有『必须唯一权威 · 必须原子 · 必须每次经过』三种性质配得上进 core/。")
        print("      一切纯函数都是 organs/ 里的外设。详见 docs/KERNEL.md。")
        print()

    print(f"内核守卫   core/ = {rel(CORE)}")
    print()

    # ---- 1 行数 ------------------------------------------------------------
    lines = count_lines(files)
    ok1 = lines <= LIMITS["lines"]
    if not ok1:
        failures.append(f"源码行数 {lines} > {LIMITS['lines']}")

    # ---- 2 依赖 ------------------------------------------------------------
    deps = []
    if GOMOD.is_file():
        deps = go_direct_requires(GOMOD.read_text(encoding="utf-8", errors="replace"))
    ok2 = len(deps) <= LIMITS["deps"]
    if not ok2:
        failures.append(f"go.mod 直接依赖 {len(deps)} 个 > 0: {', '.join(deps)}")

    # ---- 3 公开类型 --------------------------------------------------------
    types = public_types(files)
    ok3 = len(types) <= LIMITS["public_types"]
    if not ok3:
        failures.append(f"公开类型 {len(types)} > {LIMITS['public_types']}")

    # ---- 4 词汇 ------------------------------------------------------------
    domain = word_hits(files, DOMAIN_WORDS)
    host = word_hits(files, HOST_WORDS)
    ok4 = not domain and not host
    if not ok4:
        failures.append(f"内核出现领域词/宿主词 {len(domain) + len(host)} 处")

    rows = [
        ("1", "源码行数（非空, 不含 _test.go）", str(lines), str(LIMITS["lines"]), ok1),
        ("2", "Go 直接依赖（require + replace）", str(len(deps)), "0", ok2),
        ("3", "公开类型", str(len(types)), str(LIMITS["public_types"]), ok3),
        ("4", "领域词 / 宿主词", str(len(domain) + len(host)), "0", ok4),
    ]
    print(f"  {pad('#', 3)}{pad('守项', 34)}{pad('读数', 8)}{pad('上限', 8)}判定")
    for n, name, reading, limit, ok in rows:
        print(f"  {pad(n, 3)}{pad(name, 34)}{pad(reading, 8)}{pad(limit, 8)}{'ok' if ok else 'FAIL'}")

    if domain or host:
        print()
        print("  词汇命中（病因, 不是症状）:")
        for kind, (path, lineno, word, raw) in (
            [("领域", h) for h in domain] + [("宿主", h) for h in host]
        ):
            print(f"    {kind}  {rel(path)}:{lineno}  `{word}`   {raw[:90]}")
        print()
        print("  ⇒ 这些词出现在内核里, 说明外设的职责漏进来了。")
        print("     修法不是删掉那个词, 是把那个概念移到 organs/。")

    print()
    if not files:
        print("  ⚠  内核源文件 0 个 —— 四条守卫全部空过。")
        print("     这是平凡的真, 不构成任何证据。")
        if args.strict:
            failures.append("内核没有任何源文件（--strict）")
    elif not types:
        print(f"  ⚠  内核只有 {len(files)} 个源文件、{lines} 行, 且没有声明任何公开类型。")
        print("     四条守卫此刻通过是平凡的真, 不构成任何证据。")
        print("     守卫生效的前提是四个概念至少作为类型被声明。")
        if args.strict:
            failures.append(
                "内核还没有声明任何公开类型 —— 四个概念(Frame/Log/Budget/Refusal)尚未落地（--strict）"
            )
    else:
        print(f"  内核 {len(files)} 个源文件、{lines} 行、{len(types)} 个公开类型。仍然微小。")

    print()
    if failures:
        print("守卫未通过:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("守卫通过。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
