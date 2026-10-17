# 变异：把「一个拼错的字段名必须被拒，而不是换一个操作」逐条拆掉。
#
# 这一轮的来历：decodeBody 用 json.Unmarshal，它静默丢掉不认识的字段。
# 实测（arbiter.observe）：
#   ① 把 outcome 拼成 ok -> {"seq":1,"rev":1,"body":{...,"open":1}}  **被当成一次 intent**
#   ② 写对了 outcome     -> {"err":{"code":"bad_request","reason":"an outcome needs verified_by: …"}}
# 两句请求做的事完全不同，而**拼错的那一句成功了**。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
K = 'adapters/shell/skill.go'
S = 'adapters/shell/session.go'
M = 'adapters/shell/main.go'

CASES = [
    # ★ 变异就是**那一行**：去掉 DisallowUnknownFields。第一版整个换回
    # json.Unmarshal，于是 `bytes` 变成未使用的 import ⇒ 只报「编译失败」——
    # **一个只会编译失败的变异不是行为证据。**
    ("不拒未知字段（原缺陷：静默丢字段）", [
        (K, '\tdec.DisallowUnknownFields()\n', ''),
    ]),
    ("session.* 绕过 decodeBody（自己 json.Unmarshal 请求体）", [
        (S, '''	if len(f.Body) > 0 {
		if ref := decodeBody(f.Body, &body); ref != nil {
			out.Err = ref
			return true
		}
	}''', '''	if len(f.Body) > 0 {
		if err := json.Unmarshal(f.Body, &body); err != nil {
			out.Err = core.Refuse(core.CodeBadRequest, err.Error())
			return true
		}
	}'''),
    ]),
    ("spend 绕过 decodeBody", [
        (M, '''		if len(f.Body) > 0 {
			if ref := decodeBody(f.Body, &want); ref != nil {
				out.Err = ref
				return out
			}
		}''', '''		if len(f.Body) > 0 {
			if err := json.Unmarshal(f.Body, &want); err != nil {
				out.Err = core.Refuse(core.CodeBadRequest, err.Error())
				return out
			}
		}'''),
    ]),
    ("不查第二个 JSON 值（多出来的那一个静默消失）", [
        (K, '''	if dec.More() {
		return core.Refuse(core.CodeBadRequest,
			"the body holds more than one JSON value: the first one would be read and the rest silently ignored")
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
            f = run("python3 tools/check_frames.py 2>&1")
            ff = 1 if f.returncode != 0 else 0
            d = run("bash tools/drive.sh 2>&1")
            df = d.stdout.count("FAIL ")
            bits = []
            if tf: bits.append(f"go FAIL={tf}")
            if ff: bits.append("帧守卫非零")
            if df: bits.append(f"drive FAIL={df}")
            res.append((name, "抓住（" + " + ".join(bits) + "）") if bits else (name, "★★ 漏网"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
