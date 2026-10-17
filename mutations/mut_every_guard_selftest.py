# 变异：把「每一台守卫都要有自检，或者被明确列为豁免」逐条拆掉。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
A = 'tools/check_all.sh'

RULE = '''    # ★★★ 每一台守卫都要有自检，或者被明确列为豁免。
    for u in every_guard_has_a_selfcheck():
        print(u)
        bad += 1

'''

CASES = [
    ("把 frames 那条 selfcheck 删掉（一台守卫失去自检）",
     [(A, 'selfcheck "守卫自检 frames" "TEPS_FRAMES_SELF_TEST=1 python3 tools/check_frames.py"\n', '')]),
    ("加一台**没有自检**的新守卫",
     [(A, 'run check_organs.py', 'run check_newthing.py python3 tools/check_organs.py\nrun check_organs.py')]),
    ("把豁免表清空（于是 check_format.sh 被当成没有自检）",
     [(C, 'SELFCHECK_EXEMPT = {', 'SELFCHECK_EXEMPT = {}\n_UNUSED = {')]),
]

HOLE_CASES = [
    # ★★ 而**只**拆规则与被它抓的那一处是不够的：删掉 frames 那条 selfcheck 之后
    # `selfcheck_count()` 也会红（README 说十一条而实际十条）——
    # **另一条规则抓到了同一个变异**，于是「洞被演示出来」这句话不成立。
    # 所以要把标题也改成十条，让**只有**被拆掉的那一条规则能看见它。
    ("规则与违规一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (A, 'selfcheck "守卫自检 frames" "TEPS_FRAMES_SELF_TEST=1 python3 tools/check_frames.py"\n', ''),
        ('README.md', '### 十一条守卫各自被注入测试证过', '### 十条守卫各自被注入测试证过'),
    ]),
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
        g = run("python3 tools/check_contract.py > /tmp/mg.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        txt = open('/tmp/mg.txt', encoding='utf-8').read()
        right = "没有**对应的 selfcheck" in txt or "check_format.sh" in txt
        res.append((name, "抓住（契约守卫非零，且说清是哪一台）") if (caught and right)
                   else (name, f"★★ 漏网或理由不对（caught={caught}）"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mgh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、守卫也没自检，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
