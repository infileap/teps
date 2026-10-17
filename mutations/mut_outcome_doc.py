# 变异：把「outcome 的词汇表必须是两向对齐的」逐条拆掉。
#
# 这一轮的来历：契约那一行写的是 "ok|error|timeout|refused|degraded"，
# 而写进链上的 outcome 逐字取自 Verdict.Outcome() —— ok/failed/timeout/refused，
# VerdictUnknown 走 default 回 ("", false)（没有 resolution 本身就是 unknown）。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
V = 'organs/executor/verify.go'

GOOD = '"outcome": "ok|failed|timeout|refused"'
OLD = '"outcome": "ok|error|timeout|refused|degraded"'

RULE = '''    # ★★★ outcome 的词汇表必须是**两向**对齐的。
    for u in outcome_vocabulary_mismatches():
        print(u)
        bad += 1

'''

CASES = [
    ("把契约那一行改回旧的那串（三个值都不对）", [(D, GOOD, OLD)]),
    ("契约漏掉 failed（会写出去的那个）", [(D, GOOD, '"outcome": "ok|timeout|refused"')]),
    ("契约多列一个 degraded（永远不会出现）", [(D, GOOD, '"outcome": "ok|failed|timeout|refused|degraded"')]),
    ("代码那一侧少写一个（Outcome() 不再返回 failed）", [
        (V, '	case VerdictOK, VerdictFailed, VerdictTimeout, VerdictRefused:',
            '	case VerdictOK, VerdictTimeout, VerdictRefused:'),
    ]),
    # ★★★ 这一条与 Round 73/74 的同类**不一样**，而且是好的不一样：
    # 正则坏掉之后 OUTCOME_LINE.search() 返回 None，而 outcome_vocabulary_mismatches()
    # 把「读不出来」写成一个**失败**（docs/CONTRACT.md 里找不到 ... 那一行），
    # 而不是沉默。**「查不出来」这件事本身可以是它能报出来的。**
    ("判据的正则不匹配任何东西（应当被抓住，不是洞）", [
        (C, """OUTCOME_LINE = re.compile(r'"outcome":\s*"([a-z|]+)"')""",
            """OUTCOME_LINE = re.compile(r'"nope":\s*"([a-z|]+)"')"""),
        (D, GOOD, OLD),
    ]),
]

# ★ 判据相反的那些：规则失效/被拆，**并且**契约也被拆 —— 守卫保持绿才算演示成功。
HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, GOOD, OLD),
    ]),
]


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


def apply(triples):
    originals = {}
    for path, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    if any(o not in originals[os.path.join(ROOT, p)] for p, o, _ in triples):
        return None, originals
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n)   # ★ 全部替换
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    return mutated, originals


def restore(originals):
    for full, body in originals.items():
        open(full, 'w', encoding='utf-8').write(body)


res = []
for name, triples in CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_o.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if rc:
            # ★ 而拒绝理由必须说清是**哪一边**不对。
            txt = open('/tmp/mut_o.txt', encoding='utf-8').read()
            why = "会说清哪一边" if ("会被写进链上" in txt or "从不写它" in txt) else "★★ 理由没说清哪一边"
            res.append((name, f"抓住（契约守卫非零，{why}）"))
        else:
            res.append((name, "★★ 漏网"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_oh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则失效而契约也被拆，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:38s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
