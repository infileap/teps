# 变异：把「链上每一条记录都带着 rec，而契约从来没有列过它的词汇表」逐条拆掉。
#
# 这一轮的来历：adapters/ 里写出去的 rec 恰好七个（breaker/escalation/execution/
# pool/reflex/session/task），而契约里以引号形式出现过的只有 "execution" 与
# "session" —— 另外五个一次都没出现过。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
S = 'adapters/shell/session.go'

RULE = '''    # ★★★ 链上每一条记录都带着 rec，而契约从来没有列过它的词汇表。
    for u in rec_vocabulary_mismatches():
        print(u)
        bad += 1

'''

REFLEX_ROW = '| `reflex` | `reflex.go` 与 `distill.go` | 一条反射规则，或它的蒸馏 |\n'
ESC_ROW = '| `escalation` | `escalation.go` | 一次升级报价 |'

CASES = [
    ("从契约的表里删掉 reflex 那一行", [(D, REFLEX_ROW, '')]),
    ("表里多列一个 memory（幽灵值）", [
        (D, ESC_ROW, ESC_ROW + '\n| `memory` | 没有 | 幽灵 |'),
    ]),
    ("代码里多写一个 rec=ghost（代码那一侧）", [
        # ★ 字面量是 `Rec: "session"`（一个空格），第一版写成四个空格 ⇒ 模式没匹配。
        (S, 'Rec: "session"', 'Rec: "ghost"'),
    ]),
    ("判据拿绝对路径去比相对前缀（于是它报「没有东西可查」）", [
        (C, '''        if not os.path.relpath(path, ROOT).startswith("adapters" + os.sep):''',
            '''        if not path.startswith("adapters/"):'''),
    ]),
]

HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, REFLEX_ROW, ''),
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
        mutated[full] = mutated[full].replace(o, n)
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
        b = run("go build ./... 2>&1")
        g = run("python3 tools/check_contract.py > /tmp/mut_r.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_r.txt', encoding='utf-8').read()
            why = "会说清哪一边" if ("真的会写 rec=" in txt or "没有一处写它" in txt) else "报的是「没有东西可查」"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_rh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、契约也拆了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
