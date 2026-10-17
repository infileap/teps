# 变异：把「只有声明过的通道能被写」逐条拆掉。
#
# 这一轮的来历：refs.go 声明了两个通道（canary/stable），register.go 的解析只看
# 这两个，而 Set/Get 用 ValidName 校验通道 —— 它接受任何 [a-z0-9][a-z0-9._-]*。
# 实测（E2 的技能）：promote 到 'whatever'/'prod'/'a.b-c_d' **全都成功了**，
# 而没有任何一次读能够到达它们。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
R = 'store/refs.go'

CASES = [
    ("Set 不检查通道（原缺陷：写进去的指针没人读得到）", [
        (R, '''	// ★ 而通道必须是**这个存储定义过的**那两个之一：写进第三个通道的指针
	// 没有任何读能得到它（解析只看 stable 与 canary）。
	if !ValidChannel(channel) {
		return &core.Refusal{Code: core.CodeBadRequest, Reason: "unknown channel " + channel +
			": this store defines " + ChannelCanary + " and " + ChannelStable +
			", and a pointer written to any other channel is a write no read can reach"}
	}
''', ''),
    ]),
    ("Get 不检查通道（一个能读到的未知通道意味着它可能已被写过）", [
        (R, '''	if !ValidChannel(channel) {
		return "", &core.Refusal{Code: core.CodeBadRequest, Reason: "unknown channel " + channel +
			": this store defines " + ChannelCanary + " and " + ChannelStable}
	}
''', ''),
    ]),
    ("ValidChannel 什么都接受", [
        (R, '	return ch == ChannelCanary || ch == ChannelStable', '	return true'),
    ]),
    ("理由不说这个存储定义了哪两个", [
        (R, '''"unknown channel " + channel +
			": this store defines " + ChannelCanary + " and " + ChannelStable +
			", and a pointer written to any other channel is a write no read can reach"''',
            '''"unknown channel " + channel'''),
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
