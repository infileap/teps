# 变异：把「四条内核判据必须还看得见」逐条拆掉。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
K = 'tools/check_kernel.py'
C = 'tools/check_all.sh'

CASES = [
    ("弄瞎 camelCase 切分（第 4 条的前提）",
     [(K, '    for word in _CAMEL.findall(part):', '    for word in [part]:')]),
    ("弄瞎 go_direct_requires（require 块不再被看见）",
     [(K, '        if line.startswith("require"):', '        if line.startswith("NOPE"):')]),
    ("count_lines 把测试文件也数进去",
     [(K, '        if path.name.endswith("_test.go"):\n            continue', '        pass')]),
    ("public_types 把私有类型也收进去",
     [(K, '    return [(n, p) for n, p in names if n[:1].isupper()]',
          '    return [(n, p) for n, p in names]')]),
]

HOLE_CASES = [
    ("把 kernel 那条 selfcheck 从 check_all.sh 里删掉",
     [(C, 'selfcheck "守卫自检 kernel" "TEPS_KERNEL_SELF_TEST=1 python3 tools/check_kernel.py"\n', '')]),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


def apply(triples):
    originals = {}
    for path, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    if any(o not in originals[os.path.join(ROOT, p)] for p, o, _ in triples):
        return None, originals
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n)
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    return mutated, originals


def restore(originals):
    for full, body in originals.items():
        open(full, 'w', encoding='utf-8').write(body)


res = []
for name, triples in CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        # ★ 判据看输出里的 SELF-TEST OK，不是退出码。
        g = run("TEPS_KERNEL_SELF_TEST=1 python3 tools/check_kernel.py 2>&1")
        ok = "SELF-TEST OK" in g.stdout
        res.append((name, "抓住（自检没报 SELF-TEST OK）") if not ok
                   else (name, "★★ 漏网：判据瞎了而自检仍然报 OK"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mk.txt 2>&1; echo RC=$?")
        res.append((name, "抓住（契约守卫非零）") if "RC=1" in g.stdout
                   else (name, "★★ 漏网"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
