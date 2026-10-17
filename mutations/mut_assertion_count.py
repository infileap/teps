# 变异：把「一个现在时的数是可核的，也就该被核」逐条拆掉。
#
# 这一轮的来历：docs/CLIENTS.md:93 写着「tools/drive.sh 的 478 条断言全程只用
# curl」，而实测是 613。而 docs/CONTRACT.md 自己立着规矩：
#   「写不成判据的数，就不该写成数。」
# 于是同一件事有两种正确处置 —— 现在时的主张核它；历史的数删掉它。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
L = 'docs/CLIENTS.md'
D = 'tools/drive.sh'

RULE = '''    # ★★★ 一个现在时的数是可核的，也就该被核。
    for u in drive_assertion_count():
        print(u)
        bad += 1

'''

CASES = [
    ("把 CLIENTS.md 的数改回 478（旧值）", [
        (L, '的 613 条断言全程只用', '的 478 条断言全程只用'),
    ]),
    # ★ 第一版锚在一条**不存在**的 printf 上 ⇒ 报「模式没匹配」，
    # 而那不是「守卫没看见」，是**变异没生效**。
    ("给 drive.sh 加一条断言（于是文档里的数过期）", [
        (D, "  printf '  ok    skill_ref 是 64 位内容哈希\\n'\n",
            "  printf '  ok    skill_ref 是 64 位内容哈希\\n'\n  printf '  ok    变异加的一条\\n'\n"),
    ]),
]

HOLE_CASES = [
    ("规则与文档一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (L, '的 613 条断言全程只用', '的 478 条断言全程只用'),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_ac.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if rc:
            txt = open('/tmp/mut_ac.txt', encoding='utf-8').read()
            why = "会说清哪个数" if ("条断言，而实测是" in txt) else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_ach.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、文档也拆了，没人报）"))
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
