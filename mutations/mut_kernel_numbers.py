# 变异：把「文档里的一个数也是一个主张」逐条拆掉。
#
# 这一轮的来历：docs/KERNEL.md 说「Refusal + 13 个拒绝码常量」（实际 14）与
# 「456 行（Gate 1 上限 3000）」（实际 core/ 非测试 661 行），而没有任何守卫看着它们。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
K = 'docs/KERNEL.md'
R = 'core/refusal.go'

RULE = '''    # ★★★ 文档里的一个数也是一个主张。
    for u in doc_number_mismatches():
        print(u)
        bad += 1

'''

CASES = [
    ("把拒绝码数改回 13（旧值）", [(K, '`Refusal` + 14 个拒绝码常量', '`Refusal` + 13 个拒绝码常量')]),
    ("把行数改回 456（旧值）", [(K, '，661 行（Gate 1', '，456 行（Gate 1')]),
    ("代码那一侧多一个拒绝码常量", [
        (R, '\tCodeBadRequest:       false,\n', '\tCodeBadRequest:       false,\n\tCodeNonsense:         false,\n'),
        (R, '\tCodeBadRequest       = "bad_request"\n', '\tCodeBadRequest       = "bad_request"\n\tCodeNonsense         = "nonsense"\n'),
    ]),
]

HOLE_CASES = [
    ("规则与文档一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (K, '`Refusal` + 14 个拒绝码常量', '`Refusal` + 13 个拒绝码常量'),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_kn.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_kn.txt', encoding='utf-8').read()
            why = "会说清哪个数" if ("而 core/refusal.go 里是" in txt or "而 core/ 的非测试" in txt) else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_knh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、文档也拆了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:42s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
