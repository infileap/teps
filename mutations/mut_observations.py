# 变异：把「这个 key 自己被看过几次」与「带遗忘的权重」拆开的那件事逐条拆掉。
#
# 这一轮的来历：arbiter.state 报的 observations 是 `n := len(s.decisions.obs)`
# ——**整张表**的次数，而它旁边坐的是**这一个 key** 的 alpha/beta/mean/upper。
# 实测（s.a 拿 3 次、s.b 拿 7 次，表里共 10 次）：两个技能都报 10。
#
# 而第一版修法把它写成 `Posterior.Observations() = Count() - 2`，那个名字是错的：
# 有遗忘（DefaultDecay = 0.98）时先验的两个伪计数也在一起衰减，实测记 1 次是
# **0.96**、3 次是 2.82、7 次是 6.33。**一个名字读作 A 而值是 B。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
D = 'adapters/shell/decision.go'
P = 'organs/arbiter/posterior.go'

CASES = [
    ("observations 又报整张表的次数（原缺陷）", [
        (D, '"observations":       obsForKey(s.decisions.obs, k),',
            '"observations":       n,'),
    ]),
    ("weight 报成条数（把两个数并回一个名字）", [
        (D, '"weight":             round(p.Weight()),', '"weight":             float64(obsForKey(s.decisions.obs, k)),'),
    ]),
    ("Weight 不减去先验那两个伪计数", [
        (P, 'func (p Posterior) Weight() float64 { return p.Count() - 2 }',
            'func (p Posterior) Weight() float64 { return p.Count() }'),
    ]),
    ("不报 table_observations（一个名字两个意思）", [
        (D, '"observations":       obsForKey(s.decisions.obs, k),\n\t\t\t"weight":             round(p.Weight()),',
            '"observations":       obsForKey(s.decisions.obs, k),'),
    ]),
]


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


res = []
for name, triples in CASES:
    originals = {}
    for path, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    if any(o not in originals[os.path.join(ROOT, p)] for p, o, _ in triples):
        res.append((name, "模式没匹配（这不是证据）")); continue
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n, 1)
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    try:
        b = run("go build ./... 2>&1")
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        else:
            t = run("go test ./... -count=1 2>&1")
            tf = t.stdout.count("--- FAIL")
            d = run("bash tools/drive.sh 2>&1")
            df = d.stdout.count("FAIL ")
            bits = []
            if tf: bits.append(f"go FAIL={tf}")
            if df: bits.append(f"drive FAIL={df}")
            res.append((name, "抓住（" + " + ".join(bits) + "）") if bits else (name, "★★ 漏网"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:34s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
