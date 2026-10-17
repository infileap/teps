# 变异：把「端口必须真的是一个端口」与「两件事必须分开说」逐条拆掉。
#
# 这一轮的来历：isLoopback 只判 `port == ""`，于是「最后一个冒号之后的一切」
# 都算端口。实测：
#   tcp:127.0.0.1:11434        -> 通过（对）
#   tcp:localhost:80@evil.com  -> **通过**
#   tcp:localhost:not-a-port   -> **通过**
#   tcp:127.0.0.1:0            -> **通过**
# 而 Env.validate 的文档逐字写着它拒的是「a manifest that says "local" while
# naming something reachable」—— 那串 socket 本身就是一个没人检查的声明。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
M = 'store/manifest.go'

CASES = [
    # ★ 落点是 `wellFormedEndpoint` —— 它是 `Env.validate` 里的**第一道**判据，
    # 而 `isLoopback` 里那个同名的调用是第二道。第一版改的是第二道，于是
    # **行为没变**（第一道照样拦下），报「漏网」报的是变异没生效。
    ("端口只判非空（原缺陷：最后一个冒号之后的一切都算端口）", [
        (M, '''	if !validPort(host[i+1:]) {
		return false
	}''', '''	if host[i+1:] == "" {
		return false
	}'''),
    ]),
    # ★ 而 `n` 要留着用，否则只报「编译失败」。
    ("validPort 不设上下界（0 与 99999 都算端口）", [
        (M, '	return n >= 1 && n <= 65535', '	return n >= 0'),
    ]),
    ("不分开两种拒绝（把「不是端点」塞进「远端」的理由里）", [
        (M, '''		if !wellFormedEndpoint(r.Socket) {
			return bad("env.reaches[" + itoa(i) + "].socket names " + r.Socket +
				": that is not a well-formed endpoint, so there is nothing here to be local or remote — a reach must be unix:<path> or tcp:<host>:<port> with a real port (1..65535)")
		}
''', ''),
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
