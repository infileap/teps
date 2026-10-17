# 变异：把「契约的状态图必须与那张真的转换表对得上」逐条拆掉。
#
# 这一轮的来历：契约此前那四条缩写的箭头有三处不对 ——
#   rearbitrate（实际叫 rearbitrating，契约里 0 次）
#   planning 只有入边没有出边（实际 Planning: {Planned: Executing}）
#   retrying 只有入边没有出边（实际 Retrying: {Rerun: Executing}）
# **两个状态在契约的图里是死胡同，而它们在代码里都能走出去。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
S = 'organs/session/state.go'

RULE = '''    # ★★★ 契约的状态图必须与那张真的转换表对得上。
    for u in state_diagram_mismatches():
        print(u)
        bad += 1

'''

CASES = [
    ("把 rearbitrating 改回 rearbitrate（错名字）", [(D, 'rearbitrating', 'rearbitrate')]),
    ("删掉 retrying --rerun--> 那一行（死胡同）", [(D, 'retrying      --rerun-->     executing\n', '')]),
    ("删掉 planning --planned--> 那一行（另一个死胡同）", [(D, 'planning      --planned-->   executing\n', '')]),
    ("图里多画一条不存在的边", [(D, 'done          （没有出边）', 'done          --pass-->      degraded')]),
    ("代码那一侧多一条边", [
        (S, 'Done:          {},', 'Done:          {Pass: Degraded},'),
    ]),
]

HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, 'rearbitrating', 'rearbitrate'),
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
        g = run("python3 tools/check_contract.py > /tmp/mut_sd.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_sd.txt', encoding='utf-8').read()
            why = "会说清哪一条边" if ("是一条真的边" in txt or "没有这条边" in txt) else "★★ 理由没说清"
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
        g = run("python3 tools/check_contract.py > /tmp/mut_sdh.txt 2>&1; echo RC=$?")
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
