# 变异：把「一个解释规则的帧，看不出规则此刻有没有被用上」逐条拆掉。
#
# 这一轮的来历：Round 56 把半开的探针做成了一份租约（s.breakers.probes），
# allowHost 在占不到位时拒绝，而 breaker.state 留在原地。实测（同一个宿主
# /bin/sh，预算 800ms、脚本 sleep 5）：
#   ② 冷却过去、还没放探针： [{"host":"/bin/sh","state":"half-open"}]
#   ③ 探针正在飞：           [{"host":"/bin/sh","state":"half-open"}]   ← 与 ② 逐字相同
#   ③b 而此刻另一次尝试：      err=circuit_open "…a probe (p1) is already in flight…"
# 同一个回答，两件相反的事。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
B = 'adapters/shell/breaker.go'

CASES = [
    # ★ 变异就是**那个数**：`probes_in_flight` 永远是 0。第一版把键与
    # `inFlight++` 一起删掉，于是 `inFlight` 变成未使用 ⇒ 只报「编译失败」——
    # **一个只会编译失败的变异不是行为证据。**
    ("probes_in_flight 永远是 0（原缺陷：探针一个字都不报）", [
        (B, '\t\t"probes_in_flight": inFlight,', '\t\t"probes_in_flight": 0,'),
    ]),
    ("每一行都不带 probe_in_flight（槽这件事整个不见了）", [
        (B, '''		if st == breaker.HalfOpen {
			if p, held := probes[h]; held && p.expires > now {
				inFlight++
				row["probe_in_flight"] = true
				row["probe_attempt"] = p.attempt
				row["probe_expires_at"] = p.expires
			} else {
				// ★ 槽是空的 —— 这与「有探针在飞」是**相反**的两件事，
				// 而它们此前在回答里长得一样。
				row["probe_in_flight"] = false
			}
		}
''', ''),
    ]),
    ("槽这个键加给了每一个宿主（open 的也加上）", [
        (B, '		if st == breaker.HalfOpen {', '		if true {'),
    ]),
    ("不点名是哪一个探针占着槽", [
        (B, '\t\t\t\trow["probe_attempt"] = p.attempt\n', ''),
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
