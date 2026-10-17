# 变异：把「一条「变异 N/N」的主张必须告诉读者怎么核它」逐条拆掉。
#
# 这一轮的来历：README 有 59 处「变异 N/N」，证据在 mutations/（Round 91）、
# 有一条命令能跑一遍（Round 92）—— 而**读的人不知道那条命令**。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
R = 'README.md'

# ★ 与 README 里那一段**逐字一致**（含前后的空行）—— 第一版少了开头那个换行，
# 于是报「模式没匹配」，而那是**变异没生效**，不是守卫没看见。
HOWTO = """★★★ **这一节里每一处「变异 N/N」都可以一条命令核**：

```sh
tools/run_mutations.sh            # 全跑（约 40 分钟：80 条 × 约 30 秒）
tools/run_mutations.sh --list     # 只列，不跑（秒级）
tools/run_mutations.sh --only mut_channels.py
```

"""

# ★★★ 判据是**按节**判的，所以一次注入必须让**那一节整个**失去 runner 的提及。
#
# 实测确认过：`## 当前状态` 这一节里 Round 92 那一条状态行**也**提到了 runner，
# 所以只删我新加的那一小段，判据照样绿 —— **那不是「守卫没看见」，
# 是这个注入没能造出它要抓的那个状态。**
# 把那一节里**每一处** runner 提及都抹掉，才是它该抓的形状。
CASES = [
    ("把那一节里每一处 runner 提及都抹掉（判据必须报出来）",
     [(R, 'tools/run_mutations.sh', 'XXX-runner')]),
    ("只抹掉「怎么核」那一小段（边界：那一节里还有别的提及，判据不会红）",
     [(R, HOWTO, '\n')]),
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
        # ★★ 全部替换，不是第一处 —— README 里 `tools/run_mutations.sh` 出现多次，
        # 只换第一处之后那一节里还有别的提及，判据照样绿。
        # **报「漏网」报的是变异没生效**（Round 73 的同一个坑，又踩一次）。
        mutated[full] = mutated[full].replace(o, n)
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
        if "只抹掉" in name:
            # ★ 这一条是**边界**：它**不该**红 —— 那一节里还有别的提及。
            res.append((name, "边界确认（只删一小段，判据不红 —— 它只保证「这一节里有人说过」）")
                       if not caught else (name, "★★ 它红了 —— 那说明边界比写下来的更强"))
        else:
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
        txt = open('/tmp/mcnh.txt', encoding='utf-8').read()
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、那一段也删了，没人报）"))
        elif "没有任何地方调用它" in txt:
            # ★★★ 这是**另一条规则**抓到的（Round 93 的 every_rule_function_is_called）——
            # 把一条规则的调用点删掉，它就变成「定义了却没人调用」。
            # **一个洞的样本必须只让被拆的那条规则能看见它**（Round 90 的教训），
            # 而在这里做不到：拆掉这条规则的调用，另一条规则必然接手。
            res.append((name, "洞被另一条规则接手（every_rule_function_is_called）—— 这是边界，不是失败"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立")) 
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:46s} {n}")
surv = [r for _, r in res if (r.startswith("★") and "另一条规则接手" not in r) or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
