# 变异：把「率必须带着它的分母与那句话」逐条拆掉。
#
# 这一轮的来历：organs/intake 的 Stats 有 Total / ByLayer / InterceptRate / Scope，
# 而它的注释写着「Scope always travels with the rate. A rate without its
# denominator is how "60-80%" became a claim nobody could check.」
# 而 metrics.get 是这个率被报出来的唯一一个地方，它把 by_layer 与 scope 都丢了。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
M = 'adapters/shell/metrics.go'

CASES = [
    ("不报 by_layer（原缺陷）", [
        (M, '\t\t"by_layer": byLayer,\n', ''),
    ]),
    ("不报 scope（率没有那句说明）", [
        (M, '\t\t"scope":  stats.Scope,\n', ''),
    ]),
    ("scope 报成 source（一个说从哪来，一个说除的是什么）", [
        (M, '\t\t"scope":  stats.Scope,', '\t\t"scope":  "task.stats",'),
    ]),
    ("by_layer 只数 reflex 那一层（分项与总数对不上）", [
        (M, '\t\tbyLayer[string(l)] = n', '\t\tif l == intake.LayerReflex {\n\t\t\tbyLayer[string(l)] = n\n\t\t}'),
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
