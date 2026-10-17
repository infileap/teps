# 变异：把「effects 不许点名 Env 已经管着的东西」逐条拆掉。
#
# 这一轮的来历：effects[] 与 Env 说的是同一件事，而实测
#   ① env.network:true     -> 被拒（不可复现）
#   ② effects:["network"]  -> **通过了**
# 一条为了「不可复现」立下的规矩，换个字段写就能绕过去。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
M = 'store/manifest.go'

CASES = [
    ("不检查 effects（原缺陷：第二扇门开着）", [
        (M, '''	for _, e := range m.Effects {
		if reachWords[strings.ToLower(strings.TrimSpace(e))] {
			return bad("effects names " + e + ", which Env already governs: env.network / env.reaches are the checked spelling of the same fact, and a second, unchecked spelling of a claim that is refused in the first one is a door around the rule rather than a description of the skill")
		}
	}
''', ''),
    ]),
    ("reachWords 空表（判据还在，但它什么都抓不到）", [
        (M, '''var reachWords = map[string]bool{
	"network": true, "net": true, "socket": true, "reaches": true,
	"host": true, "remote": true,
}''', 'var reachWords = map[string]bool{}'),
    ]),
    ("不做大小写与空格归一化（` NETWORK ` 成为第三个入口）", [
        (M, 'reachWords[strings.ToLower(strings.TrimSpace(e))]', 'reachWords[e]'),
    ]),
    ("理由不点名它撞上了谁（只说「不许」）", [
        (M, '''"effects names " + e + ", which Env already governs: env.network / env.reaches are the checked spelling of the same fact, and a second, unchecked spelling of a claim that is refused in the first one is a door around the rule rather than a description of the skill"''',
            '''"effects names " + e + ", which is not allowed here"'''),
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
