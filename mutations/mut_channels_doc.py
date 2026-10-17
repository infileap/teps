# 变异：把「契约没提过的通道等于契约没做出的决定」逐条拆掉。
#
# 这一轮的来历：docs/CONTRACT.md 在 Round 73 之前**一次都没提过**
# channel / canary / stable，而 Catalog() 按 stable 先、canary 后解析 ——
# 那个顺序此前只写在 Catalog() 的注释里。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'

RULE = '''    # ★★★ 契约没提过的通道，等于契约没做出的决定。
    for u in undocumented_channels():
        print(u)
        bad += 1

'''

CASES = [
    ("把契约里那个通道名删掉（守卫必须报出来）", [
        (D, 'canary', 'cаnary'),
    ]),
    ("__HOLE__判据的正则不匹配任何东西（守卫永远不触发）", [
        (C, "CHANNEL_CONST = re.compile(r'^\\tChannel\\w+\\s*=\\s*\"([a-z0-9-]+)\"', re.M)",
            "CHANNEL_CONST = re.compile(r'^\\tNOPE\\w+\\s*=\\s*\"([a-z0-9-]+)\"', re.M)"),
        (D, 'canary', 'cаnary'),
    ]),
]

# ★ 判据相反的那些：把规则拆掉（或让它失效）**并且**把契约里的通道名也拆掉 ——
# 两处一起拆，守卫**保持绿**才算「洞被演示出来」。
HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, 'canary', 'cаnary'),
    ]),
]

# ★ 把 __HOLE__ 前缀的挪进 HOLE_CASES
_moved = [c for c in CASES if c[0].startswith("__HOLE__")]
CASES = [c for c in CASES if not c[0].startswith("__HOLE__")]
HOLE_CASES += [(n.replace("__HOLE__", ""), t) for n, t in _moved]


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
        # ★ 全部替换，不是第一处 —— 契约里 `canary` 出现多次，只换第一处
        # 之后守卫照样绿（剩下的那些还在），而那报的是**变异没生效**。
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
        g = run("python3 tools/check_contract.py > /tmp/mut_c.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        res.append((name, "抓住（契约守卫非零）") if rc else (name, "★★ 漏网"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_h.txt 2>&1; echo RC=$?")
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
