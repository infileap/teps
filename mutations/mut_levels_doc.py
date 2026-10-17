# 变异：把「契约没提过的等级等于契约没做出的决定」逐条拆掉。
#
# 这一轮的来历：契约 A6 写的是「E0–E4 在传递中不可丢失、不可提升」—— 一个**范围**。
# 而实测：E1 / E2 / E3 在 docs/CONTRACT.md 里**一次都没出现过**。
# 而 E1 是那扇门：Manifest.Verified() 就是 evidence.AtLeast(m.Evidence, E1)。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'

RULE = '''    # ★★★ 契约没提过的等级，等于契约没做出的决定。
    for u in undocumented_levels():
        print(u)
        bad += 1

'''

CASES = [
    ("把契约里的 E1 全部删掉（守卫必须报出来）", [
        (D, 'E1', 'EX'),
    ]),
    ("把 E4 也删掉（同一个判据的另一半）", [
        (D, 'E4', 'EX'),
    ]),
]

# ★ 判据相反的那些：规则失效/被拆，**并且**契约也被拆 —— 守卫保持绿才算演示成功。
HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, 'E1', 'EX'),
    ]),
    ("判据的正则不匹配任何东西（守卫查不出自己的正则坏了）", [
        (C, "LEVEL_CONST = re.compile(r'^\\tL\\d\\s+Level\\s*=\\s*\"(E\\d)\"', re.M)",
            "LEVEL_CONST = re.compile(r'^\\tNOPE\\d\\s+Level\\s*=\\s*\"(E\\d)\"', re.M)"),
        (D, 'E1', 'EX'),
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
        # ★ 全部替换，不是第一处（Round 73 踩过这个坑）。
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
        g = run("python3 tools/check_contract.py > /tmp/mut_l.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        res.append((name, "抓住（契约守卫非零）") if rc else (name, "★★ 漏网"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_lh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、契约也拆了，而没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:34s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
