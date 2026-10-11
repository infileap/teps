#!/usr/bin/env python3
"""存在性判据不是一致性判据。

`expect` 做的是**子串搜索**。于是 `"cost":0.9` 在 `"cost":0.9091` 上通过 ——
断言说「这个值是对的」，而它实际检查的是「这个前缀出现过」。两者在绝大多数
时候给出同一个答案，**只在它们不一致的那一次才分得出来**，而那一次正是要抓的
那一次。

这个守卫找的是同一种形状：同一个键上，某条断言里的值片段是另一条里那个值的
**真前缀**。真前缀不是唯一的形状（`"ok":1` 也会匹配 `"ok":10`），所以这个守卫
只报它看得见的那一类，并说清它看不见哪一类。

★ 第一版这个守卫自己就犯了它要抓的错：它用正则抓值，而正则在一个值内部的
第一个引号处就停了 —— 于是 `"held_by":["job.a"]` 被抓成 `"held_by":[`，
并和 `"held_by":[]` 一起报成风险。**一个用截断来判定的守卫，报的是它截断的
方式，不是被检查的东西。** 现在它用一个小的 JSON 扫描器读完整值。
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DRIVE = os.path.join(ROOT, "tools", "drive.sh")

KEY = re.compile(r'"([A-Za-z_][A-Za-z0-9_.]*)":')

def read_value(src, i):
    """从 i 开始读一个 JSON 值，返回 (值文本, 结束位置)。读不出则返回 (None, i)。"""
    while i < len(src) and src[i] in " \t":
        i += 1
    if i >= len(src):
        return None, i
    c = src[i]
    if c == '"':
        j = i + 1
        while j < len(src):
            if src[j] == "\\":
                j += 2
                continue
            if src[j] == '"':
                return src[i:j + 1], j + 1
            j += 1
        return None, i
    if c in "[{":
        close = "]" if c == "[" else "}"
        depth, j = 0, i
        instr = False
        while j < len(src):
            ch = src[j]
            if instr:
                if ch == "\\":
                    j += 2
                    continue
                if ch == '"':
                    instr = False
            elif ch == '"':
                instr = True
            elif ch == c:
                depth += 1
            elif ch == close:
                depth -= 1
                if depth == 0:
                    return src[i:j + 1], j + 1
            j += 1
        return None, i
    j = i
    while j < len(src) and src[j] not in ',}\n\r':
        j += 1
    return src[i:j].strip(), j


# 只扫**断言的第三段**（needle），不扫请求体。
#
# 第一版扫的是整个文件，于是 `"ms":10` 这种**请求字段**也被算成断言 —— 69 处
# 「风险」里绝大多数是它自己造的。一个守卫必须只检查它声称检查的东西，
# 否则它报的是文件里有多少个数字，不是有多少条断言在撒谎。
_STR = chr(34)
CALL = re.compile(
    r'^expect\s+' + _STR + r'((?:[^' + _STR + r'\\]|\\.)*)' + _STR +
    r'\s+(\S+)\s+' +
    r"(?:'([^']*)'|" + _STR + r'((?:[^' + _STR + r'\\]|\\.)*)' + _STR + r')',
    re.M,
)

def needles(src):
    for m in CALL.finditer(src):
        body = m.group(3) if m.group(3) is not None else m.group(4)
        if body is None:
            continue
        yield body.replace('\\' + _STR, _STR), src.count(chr(10), 0, m.start()) + 1

def main():
    src = open(DRIVE, encoding='utf-8').read()
    by_key = {}
    for body, ln in needles(src):
        for m in KEY.finditer(body):
            val, _ = read_value(body, m.end())
            if val is None:
                continue
            by_key.setdefault(m.group(1), []).append((val, ln))
    return report(by_key)

def report(by_key):
    hazards = []
    for key, vals in sorted(by_key.items()):
        for v, ln in vals:
            for other, oln in vals:
                if other != v and other.startswith(v):
                    hazards.append((key, v, ln, other, oln))
    total = sum(len(v) for v in by_key.values())
    if not hazards:
        print(f"断言前缀风险：0 处（扫了 {total} 个断言里的完整 JSON 值片段）")
        return 0
    print(f"断言前缀风险：{len(hazards)} 处（扫了 {total} 个片段）")
    for key, v, ln, other, oln in hazards:
        print(f'    "{key}":{v}  (drive.sh:{ln})  是  "{key}":{other}  (drive.sh:{oln})  的前缀')
    print()
    print("  这类断言读起来像一致性判据，实际是存在性判据 ——")
    print("  它只会在它最该报错的那一次沉默。修法：改成 expect_val（按 JSON 路径精确比较）。")
    print("  这个守卫只看得见「同键前缀」这一类；看不见「不同键值相同」与「值对而对象错」。")
    return 1

if __name__ == "__main__":
    sys.exit(main())
