# 变异：把「一个数如果不说它数的是什么，它就会读成另一个数」逐条拆掉。
#
# 这一轮的来历：README 的标题写着「六条守卫各自被注入测试证过」，而 check_all.sh
# 里有六条 selfcheck；而标题此前写的是「五条」，下面展开的是另外五条 ——
# 两个数说的不是同一件事，而标题只写了一个数。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
R = 'README.md'
A = 'tools/check_all.sh'

RULE = '''    # ★★★ 一个数如果不说它数的是什么，它就会读成另一个数。
    for u in selfcheck_count():
        print(u)
        bad += 1

'''

CASES = [
    ("标题改回五条（旧值）", [(R, '### 六条守卫各自被注入测试证过', '### 五条守卫各自被注入测试证过')]),
    ("给 check_all.sh 加一条 selfcheck", [
        (A, 'selfcheck "守卫自检 organs"', 'selfcheck "守卫自检 假的" "true"\nselfcheck "守卫自检 organs"'),
    ]),
    # ★★★ 判据连错两次的形状，都表现为「守卫不红」而不是「守卫报错」：
    #   ① 用 \d+ 认 ASCII 数字，而标题写的是中文数字
    #   ② 在数字与量词之间要求一个空格，而「六条」是连着写的
    # 这一条把第二处错放回去 —— 于是判据**又一次静默地什么都没查**。
    ("__HOLE__判据在数字与量词之间要求空格（于是它一次都不匹配）", [
        (C, "SELFCHECK_HEAD = re.compile(r'^### ([一二三四五六七八九十]+)条守卫各自被注入测试证过', re.M)",
            "SELFCHECK_HEAD = re.compile(r'^### ([一二三四五六七八九十]+) 条守卫各自被注入测试证过', re.M)"),
        (R, '### 六条守卫各自被注入测试证过', '### 五条守卫各自被注入测试证过'),
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


moved = [c for c in CASES if c[0].startswith("__HOLE__")]
CASES = [c for c in CASES if not c[0].startswith("__HOLE__")]
HOLE_CASES = [(n.replace("__HOLE__", ""), t) for n, t in moved]

res = []
for name, triples in CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_sc.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if rc:
            txt = open('/tmp/mut_sc.txt', encoding='utf-8').read()
            why = "会说清哪个数" if "条 selfcheck" in txt else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_sch.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（判据静默地什么都没查）"))
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
