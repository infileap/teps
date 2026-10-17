# 变异：把「契约点名过的清单字段要么有人执行、要么标明没人执行」逐条拆掉。
#
# 这一轮的来历：idempotent / effects / preconditions / postconditions / on_failure
# 都在 docs/CONTRACT.md 的 Skill ABI 那一行里，而实测非测试代码里的读次数是 0。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'

CASES = [
    ("删掉 ABI 表里 effects 那一行的 ⬜（守卫必须报出来）", [
        (D, '| `effects[]` | ⬜ **只被报出来，没有人执行它**', '| `effects[]` | 只被报出来，没有人执行它'),
    ]),
    ("__HOLE__判据取整段的反引号名字（于是字段名自己满足了它）", [
        (C, '''    marked = set()
    for line in abi.splitlines():
        if "\\u2b1c" not in line:
            continue
        marked |= set(re.findall(r'`([a-z_]+)[^`]*`', line))''',
            '''    marked = set(re.findall(r'`([a-z_]+)[^`]*`', abi))'''),
        (D, '| `effects[]` | ⬜ **只被报出来，没有人执行它**', '| `effects[]` | 只被报出来，没有人执行它'),
    ]),
    ("排除定义文件（于是 store/manifest.go 里的 Validate 看不见）", [
        (C, '''        for path in go_files():
            body = open(path, encoding="utf-8").read()
            if path.endswith("store/manifest.go"):
                body = re.sub(r'^type Manifest struct \\{.*?^\\}', '', body, flags=re.M | re.S)''',
            '''        for path in go_files():
            if path.endswith("store/manifest.go"):
                continue
            body = open(path, encoding="utf-8").read()'''),
    ]),
]


# ★ 判据相反的一条：它测的是**守卫会不会放过它本该抓的东西**，
# 所以守卫**保持绿**才算演示成功。
HOLE_CASES = []


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


moved = [c for c in CASES if c[0].startswith("__HOLE__")]
CASES = [c for c in CASES if not c[0].startswith("__HOLE__")]
for name, triples in moved:
    HOLE_CASES.append((name.replace("__HOLE__", ""), triples))

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
        # ★ 退出码要**不经管道**地取 —— `cmd | head -3; echo $?` 报的是 head 的状态。
        g = run("python3 tools/check_contract.py > /tmp/mut_abifields.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        d = run("bash tools/drive.sh 2>&1")
        df = d.stdout.count("FAIL ")
        bits = []
        if rc: bits.append("契约守卫非零")
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
        g = run("python3 tools/check_contract.py > /tmp/mut_hole.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（字段名自己满足了判据）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:34s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
