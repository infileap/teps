# 变异：把「判决是一次比较，两个被减数都要在」逐条拆掉。
#
# 这一轮的来历：ShouldEscalate 把 ActionSkill 与 ActionLocal 一起算进「本地」，
# 而 best 取 ranked[0]（全体最好）。于是当一个 skill 动作就是全体最好时
# local == best，比较恒真，而理由在说一件没发生的事。实测（margin 0.5）：
#   chosen = skill/paid utility 9 · local = skill/paid utility 9 · reason 说
#   "the paid action does not win by the margin" —— **付费动作从来没被比过**。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
E = 'organs/arbiter/escalation.go'
D = 'adapters/shell/decision.go'

CASES = [
    # ★ 变异就是**原缺陷那一行**：把「本地」写成按动作名枚举，把 `skill` 也算进去。
    # 第一版把它换成等价的 `== escalate || == ask_human`，于是**语义没变、
    # 答案没变**，报「漏网」报的是变异没生效。
    ("按动作名枚举「本地」（原缺陷：skill 也算本地）", [
        (E, 'if spendsOutsideMoney(r.Offer.Action) {',
            'if r.Offer.Action == ActionLocal || r.Offer.Action == ActionSkill {'),
    ]),
    ("`spendsOutsideMoney` 把一切都算成花外面钱的", [
        (E, 'return a == ActionEscalate || a == ActionAskHuman', 'return true'),
    ]),
    ("判决不报被比下去的那一个（原缺陷）", [
        (D, '''			"local":       string(esc.Local.Offer.Action),
			"local_offer": localRow,''', '''			"local": string(esc.Local.Offer.Action),'''),
    ]),
    ("gap 永远是 0（差不是算出来的）", [
        # ★ 空格数要照抄：实际那一行是 `"gap":    round(...)`（冒号后**四个**空格，
        # 为了与 `chosen_offer` / `local_offer` 对齐）。第一版写了三个 ——
        # **一个差一个空格的模式报的是「模式没匹配」，而那不是证据。**
        (D, '"gap":    round(esc.Chosen.Utility - esc.Local.Utility),',
            '"gap":    0.0,'),
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
