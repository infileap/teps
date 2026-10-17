# 变异：把「一次检索必须报出它按什么滤的、每条命中的是什么」逐条拆掉。
#
# 这一轮的来历：memory.retrieve 只回 count / ids / texts / total，而 Retrieve 正是
# 按 MinEvidence 与 Tags 过滤的。实测（三条记录 E0/E1/E2，标签 x/x/y）：
# 登记表里共 3 条，四次查询回了 2/1/1/2 —— 而读的人分不出某一条是因为等级不够
# 被滤掉、还是因为标签不对、还是根本不存在。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
K = 'adapters/shell/knowledge.go'

CASES = [
    ("不报 records（原缺陷：只有 id 与正文）", [
        (K, '\t\t\t"records": rows,\n', ''),
    ]),
    ("不报 filter（读的人不知道按什么滤的）", [
        (K, '''			"filter": map[string]any{
				"min_evidence": string(floor), "tags": body.Tags, "as_of": body.AsOf,
			},
''', ''),
    ]),
    ("filter 报的是请求原文而不是生效的缺省值", [
        (K, '"min_evidence": string(floor), "tags": body.Tags, "as_of": body.AsOf,',
            '"min_evidence": body.MinEvidence, "tags": body.Tags, "as_of": body.AsOf,'),
    ]),
    ("命中的记录不带 evidence（判据那一栏不见了）", [
        (K, '\t\t\t\t"evidence": string(r.Evidence), "recorded_at": r.RecordedAt,',
            '\t\t\t\t"recorded_at": r.RecordedAt,'),
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
            res.append((name, f"抓住（go FAIL={tf} drive FAIL={df}）") if (tf or df) else (name, "★★ 漏网"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:34s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
