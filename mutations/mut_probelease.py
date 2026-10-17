# 变异：把「半开只放一个探针」逐条拆掉。
#
# 这一轮的来历：Allow 在 case HalfOpen 里无条件回 nil，于是「半开只放一个探针」
# 靠的是副作用（第一个探针的失败把状态推回 open）。串行时恰好成立，并发时不成立。
# 实测（三次宿主失败 → 等 31 秒进 half-open → 同时发 5 次）：
#   d7 放行 · d5 放行 · d4 circuit_open · d8 circuit_open · d6 放行
# **放行了三次**，而 README 写着「半开只放一个探针」。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
B = 'adapters/shell/breaker.go'

CASES = [
    ("半开时不占位（原缺陷：读状态而不是占位）", [
        (B, '\t\tif p, held := s.breakers.probes[host]; held {\n'
            '\t\t\treturn core.Refuse(core.CodeCircuitOpen,\n'
            '\t\t\t\t"host "+host+" is half-open and a probe ("+p.attempt+") is already in flight: the "+\n'
            '\t\t\t\t\t"cooldown admits exactly one probe, and admitting the full traffic of every skill "+\n'
            '\t\t\t\t\t"behind this host is the thing half-open exists to avoid")\n'
            '\t\t}\n'
            '\t\ts.breakers.probes[host] = probeLease{attempt: attempt, expires: now + breaker.CooldownMS}\n',
            ''),
    ]),
    ("占位永远不还（一个成功的探针会让 host 再也放不出探针）", [
        (B, '\tif p, held := s.breakers.probes[host]; held && p.attempt == attempt {\n'
            '\t\tdelete(s.breakers.probes, host)\n'
            '\t}\n', ''),
    ]),
    # ★ `p` 变成未使用会只报「编译失败」—— 而一个只会编译失败的变异不是行为证据。
    ("任何一个 attempt 都能替探针还占位", [
        (B, 'if p, held := s.breakers.probes[host]; held && p.attempt == attempt {',
            'if _, held := s.breakers.probes[host]; held {'),
    ]),
    ("过期占位不清理（崩掉的探针把熔断器永远卡在半开）", [
        (B, '\tfor h, p := range s.breakers.probes {\n'
            '\t\tif p.expires <= now {\n'
            '\t\t\tdelete(s.breakers.probes, h)\n'
            '\t\t}\n'
            '\t}\n', ''),
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
