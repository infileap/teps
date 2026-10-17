# 变异：把「一条「变异 N/N」的主张必须告诉读者怎么核它」逐条拆掉。
#
# 这一轮的来历：README 有 59 处「变异 N/N」，证据在 mutations/（Round 91）、
# 有一条命令能跑一遍（Round 92）—— 而**读的人不知道那条命令**。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
R = 'README.md'

HOWTO = '''
★★★ **这一节里每一处「变异 N/N」都可以一条命令核**：

```sh
tools/run_mutations.sh            # 全跑（约 40 分钟：80 条 × 约 30 秒）
tools/run_mutations.sh --list     # 只列，不跑（秒级）
tools/run_mutations.sh --only mut_channels.py
```
'''

CASES = [
    ("把「怎么核」那一小段整段删掉",
     [(R, HOWTO, '\n')]),
    ("把 runner 的名字从那一小段里抹掉（只留代码块）",
     [(R, 'tools/run_mutations.sh --list     # 只列，不跑（秒级）',
          'xxx --list     # 只列，不跑（秒级）')]),
    ("让判据退回「整份 README 出现过就算」（于是被一处提到就救活）",
     [(C, '''            if MUT_CLAIM.search(sect) and MUT_RUNNER not in sect:''',
          '''            if MUT_CLAIM.search(read(rel)) and MUT_RUNNER not in read(rel):''')]),
]

HOLE_CASES = [
    ("规则与那一段一起拆掉（洞：没人检查这件事）", [
        (C, '''    for u in mutation_claims_name_their_runner():
        print(u)
        bad += 1

''', ''),
        (R, HOWTO, '\n'),
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
    for path, o, _ in triples:
        if o not in originals[os.path.join(ROOT, path)]:
            return None, originals
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n, 1)
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    return True, originals


def restore(originals):
    for full, body in originals.items():
        open(full, 'w', encoding='utf-8').write(body)


res = []
for name, triples in CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mcn.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        txt = open('/tmp/mcn.txt', encoding='utf-8').read()
        right = "读的人不知道那条命令" in txt
        res.append((name, "抓住（契约守卫非零，且点名了那一节）") if (caught and right)
                   else (name, f"★★ 漏网或理由不对（caught={caught}）"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mcnh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、那一段也删了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:46s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
