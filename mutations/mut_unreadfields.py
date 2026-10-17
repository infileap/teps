# 变异：把「一个只被写、从不被读的字段是一个承诺」逐条拆掉。
#
# 这一轮的来历：store/manifest.go 的 Effects / Idempotent / OnFailure / Pre /
# CapturedAt / URL / Commit / Packages 有 json 标签而**从没被读** —— 而
# Manifest 这个结构体**从来没被序列化过**（Registration.Manifest 与
# Resolution.Manifest 都是 string，是清单哈希）。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
O = 'tools/check_organs.py'
M = 'store/manifest.go'

CASES = [
    ("判据的字面量写错（'\"json:' 而不是 'json:\"'）", [
        (O, '''                    if 'json:"' in line and struct_is_marshalled(others, struct_name):''',
            '''                    if '"json:' in line and struct_is_marshalled(others, struct_name):'''),
        (M, '\t// DEAD-API: 同上\n\tCommit     string `json:"commit,omitempty"`\n', '\tCommit     string `json:"commit,omitempty"`\n'),
        (M, '\t// DEAD-API: 同上\n\tCapturedAt string `json:"captured_at,omitempty"`\n', '\tCapturedAt string `json:"captured_at,omitempty"`\n'),
        (M, '\t// DEAD-API: 出处只被写、从没被读 —— 一条没人读的出处不是出处\n', ''),
        (M, '\t// DEAD-API: 解释器版本由 Env 的其它字段决定，这一张表只被写、从没被读\n', ''),
    ]),
    # ★ 这一条挪到下面的 HOLE_CASES —— 它的判据是反的。
    ("__MOVE__带 json 标签的一律放过（原缺陷：那一刀太宽）", [
        (O, '''                    if 'json:"' in line and struct_is_marshalled(others, struct_name):
                        continue''',
            '''                    if 'json:"' in line:
                        continue'''),
        (M, '\t// DEAD-API: 同上\n\tCommit     string `json:"commit,omitempty"`\n', '\tCommit     string `json:"commit,omitempty"`\n'),
        (M, '\t// DEAD-API: 同上\n\tCapturedAt string `json:"captured_at,omitempty"`\n', '\tCapturedAt string `json:"captured_at,omitempty"`\n'),
        (M, '\t// DEAD-API: 出处只被写、从没被读 —— 一条没人读的出处不是出处\n', ''),
        (M, '\t// DEAD-API: 解释器版本由 Env 的其它字段决定，这一张表只被写、从没被读\n', ''),
    ]),
    ("read_uses 把写也当成读", [
        (O, '''        if tail.startswith('=') and not tail.startswith('=='):
            continue  # 写''', '''        pass'''),
        (M, '\t// DEAD-API: 同上\n\tCommit     string `json:"commit,omitempty"`\n', '\tCommit     string `json:"commit,omitempty"`\n'),
        (M, '\t// DEAD-API: 同上\n\tCapturedAt string `json:"captured_at,omitempty"`\n', '\tCapturedAt string `json:"captured_at,omitempty"`\n'),
        (M, '\t// DEAD-API: 出处只被写、从没被读 —— 一条没人读的出处不是出处\n', ''),
        (M, '\t// DEAD-API: 解释器版本由 Env 的其它字段决定，这一张表只被写、从没被读\n', ''),
    ]),
    ("把一个 DEAD-API 标记删掉（守卫必须报出来）", [
        (M, '\t// DEAD-API: 出处只被写、从没被读 —— 一条没人读的出处不是出处\n', ''),
    ]),
]


# ★★ 判据相反的几条：它们测的是**守卫会不会放过它本该抓的东西**，
# 所以守卫**保持绿**才算演示成功。
HOLE_CASES = []


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


# 把标记为 __MOVE__ 的挪进 HOLE_CASES
moved = [c for c in CASES if c[0].startswith("__MOVE__")]
CASES = [c for c in CASES if not c[0].startswith("__MOVE__")]
for name, triples in moved:
    HOLE_CASES.append((name.replace("__MOVE__", ""), triples))

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
        g = run("python3 tools/check_organs.py 2>&1")
        guard = g.returncode != 0
        b = run("go build ./... 2>&1")
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        else:
            t = run("go test ./... -count=1 2>&1")
            tf = t.stdout.count("--- FAIL")
            d = run("bash tools/drive.sh 2>&1")
            df = d.stdout.count("FAIL ")
            bits = []
            if guard: bits.append("器官守卫非零")
            if tf: bits.append(f"go FAIL={tf}")
            if df: bits.append(f"drive FAIL={df}")
            res.append((name, "抓住（" + " + ".join(bits) + "）") if bits else (name, "★★ 漏网"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

for name, triples in HOLE_CASES:
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
        g = run("python3 tools/check_organs.py 2>&1")
        if g.returncode == 0:
            res.append((name, "洞被演示出来（守卫放过了那些没有标记的未读字段）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:34s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
