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


def main():
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

    print("适配器边界通过。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
