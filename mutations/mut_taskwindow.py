# 变异：把「一个被截断的列表必须说它被截断了」逐条拆掉。
#
# 这一轮的来历：taskStats 是 `if len(recent) > 20 { recent = recent[len(recent)-20:] }`
# 然后只回 {"stats":…,"recent":…}。实测（取进 25 个任务）：
#   {"recent":[…20 条…],"stats":{"total":25,…}}
# total 是 25、recent 有 20 条，而没有一处说这件事；也没有一处说那 20 条是最新的。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
I = 'adapters/shell/intake.go'

CASES = [
    ("静默截断（原缺陷：只有 stats 与 recent）", [
        (I, '''	body := map[string]any{
		"stats": stats, "recent": recent,
		// ★ 窗口本身：上限、实际给了几条。
		"limit": limit, "returned": len(recent),
		// ★ 被截断了吗。
		"truncated": len(recent) < stats.Total,
		"basis": "recent is the newest arrivals, oldest first within the window; " +
			"a truncated list means stats.total counts more arrivals than recent holds",
	}''', '''	body := map[string]any{
		"stats": stats, "recent": recent,
	}'''),
    ]),
    # ★ 只改这一处：truncated 永远是 true ⇒ 没截断时报的是 true。
    # 第一版还加了一个 `_ignored: 0` 的 map 字面量，那只报「编译失败」——
    # **一个只会编译失败的变异不是行为证据。**
    ("truncated 永远是 true（没截断时也在说被截断了）", [
        (I, '\t\t"truncated": len(recent) < stats.Total,', '\t\t"truncated": true,'),
    ]),
    ("不报 limit（一个不说的上限与没有上限长得一样）", [
        (I, '\t\t"limit": limit, "returned": len(recent),', '\t\t"returned": len(recent),'),
    ]),
    ("窗口取的是最旧的而不是最新的", [
        (I, '\t\trecent = recent[len(recent)-limit:]', '\t\trecent = recent[:limit]'),
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
