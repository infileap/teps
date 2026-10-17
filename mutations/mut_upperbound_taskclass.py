# 变异：把「上界随证据收缩」与「顶层 task_class 必须被读」逐条拆掉。
#
# ① organs/arbiter/posterior.go 的 UpperBound 此前 `if n <= 1 { return 1.0 }`，
#    把「零观测」与「一次观测」合并成一档 —— 而 n = 1 时公式是有定义的。
#    实测（{2,1}）：此前 1.0000，公式本应给 0.6667 + 0.25*0.8326 = 0.8748。
# ② adapters/shell/escalation.go 的 priceOf 此前根本不读 quoteBody.TaskClass，
#    于是顶层给了 task_class、offer 里没给时，报价落在**空类**（先验）上。
#    实测（{s.a, task_exec} 攒 20 次全成）：quote 收下并给 p=1，而同一份 decide 会拒
#    "…was read under : …"（那个空串就是证据）。**两扇门一个字段两个答案。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
P = 'organs/arbiter/posterior.go'
E = 'adapters/shell/escalation.go'

CASES = [
    ("零观测与一次观测又合并成一档（原缺陷）", [
        (P, '\tif n <= 0 {', '\tif n <= 1 {'),
    ]),
    ("零观测那一档直接返回公式的结果（丢掉那个具名选择）", [
        (P, '\t\treturn UnobservedUpperBound', '\t\treturn 0.5'),
    ]),
    ("offersOf 又不读顶层的 task_class（原缺陷）", [
        (E, '''		tc := o.TaskClass
		switch {
		case tc == "":
			// ★ 调用方在顶层说了，就用它 —— 这才是「被读」。
			tc = taskClass
		case taskClass != "" && tc != taskClass:
			return nil, core.Refuse(core.CodeBadRequest,
				"the quote names task class "+taskClass+" but the offer for "+o.Skill+
					" was priced under "+tc+": the same price cannot be read under two classes")
		}''', '''		tc := o.TaskClass'''),
    ]),
    ("offersOf 用了顶层那个，但冲突时不拒（静默改写）", [
        (E, '''		case taskClass != "" && tc != taskClass:
			return nil, core.Refuse(core.CodeBadRequest,
				"the quote names task class "+taskClass+" but the offer for "+o.Skill+
					" was priced under "+tc+": the same price cannot be read under two classes")''',
            '''		case taskClass != "" && tc != taskClass:
			tc = taskClass'''),
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
