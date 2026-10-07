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

    rows = ROW_RE.findall(read("docs/CONTRACT.md"))
    doc_codes = {c for c, _, _ in rows}
    doc_retry = {c: (mark == "✅") for c, _, mark in rows}

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

    print(f"  拒绝码 {len(codes)} 个，文档列出 {len(doc_codes)} 个。")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
