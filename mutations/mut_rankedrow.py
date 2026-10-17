# 变异：把「两扇门共用一个行形状」逐条拆掉。
#
# 这一轮的来历：同一个 arbiter.Rank 有两扇门 —— arbiter.rank 与 escalation.quote。
# Round 62 只给后者补了 weight/posterior，而前者一行都没有。实测：一个从没被
# 观测过的技能与一个 30 次全成的技能在 arbiter.rank 下**逐字相同**（除 skill 名），
# 而 arbiter.state 对同一个 key 说的是 weight=0 与 weight=21.8168。
# **补一扇门不叫修好一个缺陷。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
D = 'adapters/shell/decision.go'
E = 'adapters/shell/escalation.go'

CASES = [
    ("arbiter.rank 又用自己的行形状（原缺陷：没有 weight/posterior/task_class）", [
        (D, '''	for _, r := range ranked {
		row := rankedRow(r)
		if m := metaAt(meta, r.Offer.Tag); m != nil {
			row["cost"] = m
		}
		rows = append(rows, row)
	}''', '''	for _, r := range ranked {
		row := map[string]any{
			"action": r.Offer.Action, "skill": r.Offer.Skill,
			"utility": round(r.Utility), "p": round(r.Prob),
		}
		if m := metaAt(meta, r.Offer.Tag); m != nil {
			row["cost"] = m
		}
		rows = append(rows, row)
	}'''),
    ]),
    ("margin 那一支又不报被选中的那一行（原缺陷：连 p 都没有）", [
        (D, '''		out.Body = mustJSON(map[string]any{
			"chosen":       string(esc.Chosen.Offer.Action),
			"chosen_offer": chosenRow,
			"reason":       esc.Reason, "threshold": body.Margin,
			"skipped": skipped,
		})''', '''		out.Body = mustJSON(map[string]any{
			"chosen": string(esc.Chosen.Offer.Action),
			"reason": esc.Reason, "threshold": body.Margin,
			"skipped": skipped,
		})'''),
    ]),
    # ★ 这一条的落点是 `quoteID`（它此前是从只有 p 与 u 的投影算的），
    # 不是 `escalationQuote` 的 `rows` —— 第一版把两处搞混了，于是变异改的是
    # 报价单 id 的输入，而响应里那一行没变，测试照样通过：**报「漏网」报的是
    # 变异打错了地方，不是守卫没看见。**
    ("quoteID 又只用 p 与 u 算（id 在后验移动时不变）", [
        (E, '''		row := rankedRow(r)
		// ★ offer 自己的两个数也进 id：换一个 offer 就该换一个 id。
		row["cost"] = r.Offer.Cost
		row["latency"] = r.Offer.Latency
		rows = append(rows, row)''', '''		rows = append(rows, map[string]any{
			"action": string(r.Offer.Action), "skill": r.Offer.Skill,
			"p": round(r.Prob), "u": round(r.Utility),
		})'''),
    ]),
    ("chosen 的类型被改成对象（破坏性改动）", [
        (D, '''			"chosen":       string(esc.Chosen.Offer.Action),
			"chosen_offer": chosenRow,''', '''			"chosen":       chosenRow,'''),
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
