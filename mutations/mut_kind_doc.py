# 变异：把「kind 那张表必须与真的 Kind 声明对得上」逐条拆掉。
#
# 这一轮的来历：store/manifest.go 声明七个 Kind，而契约那一行只列六个 ——
# 缺的正是 compose，而 Manifest.Validate 里四条判据都以 kind=compose 为条件。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
M = 'store/manifest.go'

RULE = '''    # ★★★ kind 那张表必须与真的 Kind 声明对得上。
    for u in kind_vocabulary_mismatches():
        print(u)
        bad += 1

'''

GOOD = '`rl_policy` | `human` | `compose`。'

CASES = [
    ("把 compose 从那一行删掉（回到旧的样子）", [
        (D, GOOD, '`rl_policy` | `human`。'),
    ]),
    ("那一行多列一个 memory（幽灵值）", [
        (D, GOOD, '`rl_policy` | `human` | `compose` | `memory`。'),
    ]),
    # ★ 常量与表要**一起**改 —— 只改表会让 `KindMemory` 未定义，
    # 那只报「编译失败」，而**一个只编译不过的变异不是行为证据**。
    ("代码那一侧多声明一个 Kind", [
        (M, '\tKindCompose: true,\n', '\tKindCompose: true, KindMemory: true,\n'),
        (M, '\tKindCompose Kind = "compose"\n', '\tKindCompose Kind = "compose"\n\tKindMemory Kind = "memory"\n'),
    ]),
]

HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, GOOD, '`rl_policy` | `human`。'),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_k.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_k.txt', encoding='utf-8').read()
            why = "会说清哪一边" if ("没有列它" in txt or "没有声明它" in txt) else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_kh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、契约也拆了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
