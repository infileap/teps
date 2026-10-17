# 变异：把「一个声明了却没人执行的字段是一句装饰」逐条拆掉。
#
# 这一轮的来历：Effects 与 Idempotent 在 docs/CONTRACT.md:157 的 Skill ABI 那一行
# 里是契约承诺过的字段。实测（publish 带 idempotent:false，再 resolve）：
# **两个都被报出来了** —— 所以它们不是「没人读」，是「被读出来、却从来不被执行」。
# 而 A2 说所有变更幂等，内核靠 attempt 键做到 —— 声明与行为不一致，而那是静默的。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
M = 'store/manifest.go'

CASES = [
    ("不检查 idempotent（原缺陷：声明了却没人执行）", [
        (M, '''	if !m.Idempotent {
		return bad("idempotent must be true: the ABI makes every change idempotent by the attempt key (A2), so a skill declaring otherwise is claiming something the kernel will not honour — the declaration would be decoration")
	}
''', ''),
    ]),
    ("判据反了（声明 true 的反被拒）", [
        (M, '	if !m.Idempotent {', '	if m.Idempotent {'),
    ]),
    ("理由不说「装饰」这件事（只说「不许」）", [
        (M, '''so a skill declaring otherwise is claiming something the kernel will not honour — the declaration would be decoration")''',
            '''so a skill declaring otherwise is claiming something the kernel will not honour")'''),
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
