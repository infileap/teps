# 变异：把「适配器守卫的两条前提必须还看得见」逐条拆掉。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
A = 'tools/check_adapters.py'
C = 'tools/check_all.sh'

CASES = [
    ("strip_comments 什么都不剥（于是它去报散文）",
     [(A, 'def strip_comments(src):', 'def strip_comments(src):\n    return src  # 变异')]),
    ("DISCARDED_OK 不匹配任何东西（判据瞎了）",
     [(A, "DISCARDED_OK = re.compile(r'\\w+\\s*,\\s*_\\s*:?=\\s*evidence\\.Rank\\(')",
          "DISCARDED_OK = re.compile(r'NOPE\\(')")]),
    ("imports_of 什么都看不见",
     [(A, '    found = []\n    for m in re.finditer(r\'^import\\s*\\(([^)]*)\\)\', src, re.M | re.S):',
          '    found = []\n    return found\n    for m in re.finditer(r\'^import\\s*\\(([^)]*)\\)\', src, re.M | re.S):')]),
]

HOLE_CASES = [
    ("把 adapters 那条 selfcheck 从 check_all.sh 里删掉",
     [(C, 'selfcheck "守卫自检 adapters" "TEPS_ADAPTERS_SELF_TEST=1 python3 tools/check_adapters.py"\n', '')]),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


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
        g = run("TEPS_ADAPTERS_SELF_TEST=1 python3 tools/check_adapters.py 2>&1")
        ok = "SELF-TEST OK" in g.stdout
        res.append((name, "抓住（自检没报 SELF-TEST OK）") if not ok
                   else (name, "★★ 漏网：判据瞎了而自检仍然报 OK"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/ma2.txt 2>&1; echo RC=$?")
        res.append((name, "抓住（契约守卫非零）") if "RC=1" in g.stdout else (name, "★★ 漏网"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
