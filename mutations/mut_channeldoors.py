# 变异：把「同一扇门有三处」逐条拆掉。
#
# 这一轮的来历：Round 71 堵了 Set 与 Get，而 History 与 Channels 漏了。
#   History("x","whatever") 回「no history for x/whatever」—— 一个关于**文件不存在**
#   的理由，而真正发生的是「这个存储没有这个通道」。
#   Channels 列的是「目录里有什么」，而 skill.list 直接报出去。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
R = 'store/refs.go'

CASES = [
    ("History 不检查通道（原缺陷：说成「没有历史」）", [
        (R, '''	if !ValidChannel(channel) {
		return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "unknown channel " + channel +
			": this store defines " + ChannelCanary + " and " + ChannelStable}
	}
''', ''),
    ]),
    ("Channels 列出目录里的一切（原缺陷：把磁盘状态当成契约）", [
        (R, '''		if !ValidChannel(n) {
			continue
		}
''', ''),
    ]),
    ("Channels 只滤后缀（等于上一半，但保留 ValidChannel 的调用点）", [
        (R, '		if !ValidChannel(n) {\n			continue\n		}\n',
            '		if strings.HasSuffix(n, ".nope") {\n			continue\n		}\n'),
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
