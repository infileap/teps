# 变异：把「同一个事实写四遍，就会有三个地方没被改到」逐条拆掉。
#
# 这一轮的来历：core/ 的行数被写成四份、三种数（459 / 516 / 661）。
# Round 81 只改到了 docs/KERNEL.md:69 那一份，而**判据只写了那一种措辞** ——
# 于是另外三份在它眼里不存在。**一个照着「我已经修好的那一份」写出来的判据，
# 会放过所有还没修的份。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
R = 'README.md'
K = 'docs/KERNEL.md'

RULE = '''    # ★★★ 文档里的一个数也是一个主张。
    for u in doc_number_mismatches():
        print(u)
        bad += 1

'''

# 四种措辞，各一条
CASES = [
    ("目录清单里的 459 行", [(R, '661 行 · 6 类型', '459 行 · 6 类型')]),
    ("P0 表里的 459 行", [(R, '（661 行 / 6 公开类型', '（459 行 / 6 公开类型')]),
    ("KERNEL 表里的 459 行", [(K, '661 行 · 6 个公开类型', '459 行 · 6 个公开类型')]),
    ("守卫读数里的 516 / 3000 行", [(R, '661 / 3000 行', '516 / 3000 行')]),
    ("Gate 那一种措辞", [(K, '，661 行（Gate 1', '，456 行（Gate 1')]),
]

HOLE_CASES = [
    ("规则与四份拷贝一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (R, '661 行 · 6 类型', '459 行 · 6 类型'),
        (R, '（661 行 / 6 公开类型', '（459 行 / 6 公开类型'),
        (K, '661 行 · 6 个公开类型', '459 行 · 6 个公开类型'),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_cl.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if rc:
            txt = open('/tmp/mut_cl.txt', encoding='utf-8').read()
            why = "会说清哪个数" if "行，而非测试" in txt else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_clh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（四份拷贝一起漂，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
