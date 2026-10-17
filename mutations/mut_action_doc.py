# 变异：把「仲裁器的动作表必须与真的 Action 声明对得上」逐条拆掉。
#
# 这一轮的来历：契约写了 arbiter 的四帧，却从来没有列过仲裁器可以选的那些动作。
# organs/arbiter/escalation.go:45-61 声明恰好六个，而契约里以单词边界出现的次数是
# local 0 · ask_human 0 · degrade 0 · escalate 1（那一次还是状态机的事件）。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
E = 'organs/arbiter/escalation.go'

RULE = '''    # ★★★ 仲裁器的动作表必须与真的 Action 声明对得上。
    for u in action_vocabulary_mismatches():
        print(u)
        bad += 1

'''

LOCAL_ROW = '| `local` | answers from what is already known. Nearly free, low ceiling. |\n'
DEGRADE_ROW = '| `degrade` | answers honestly with less.'

CASES = [
    ("删掉 local 那一行", [(D, LOCAL_ROW, '')]),
    ("表里多列一个 search（幽灵值）", [(D, DEGRADE_ROW, '| `search` | 幽灵 |\n' + DEGRADE_ROW)]),
    # ★ 常量与 AllActions 要**一起**改 —— 只加常量会让 AllActions 少一个，
    # 而那只报「编译失败」；一个只编译不过的变异不是行为证据。
    ("代码那一侧多声明一个 Action", [
        (E, '\tActionDegrade Action = "degrade"\n',
            '\tActionDegrade Action = "degrade"\n\tActionSearch Action = "search"\n'),
        (E, 'ActionLocal, ActionSkill, ActionShadow, ActionEscalate, ActionAskHuman, ActionDegrade,',
            'ActionLocal, ActionSkill, ActionShadow, ActionEscalate, ActionAskHuman, ActionDegrade, ActionSearch,'),
    ]),
]

HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, LOCAL_ROW, ''),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_a.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_a.txt', encoding='utf-8').read()
            why = "会说清哪一边" if ("表里没有它" in txt or "没有声明它" in txt) else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_ah.txt 2>&1; echo RC=$?")
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
