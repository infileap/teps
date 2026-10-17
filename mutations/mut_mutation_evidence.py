# 变异：把「README 那些「变异 N/N」的证据必须在仓库里、且跑得起来」逐条拆掉。
#
# 这一轮的来历：README 里有 56 处「变异 N/N」的主张，而它们的证据在 .mutbak/ ——
# 一个**被 gitignore 的**目录。**一个克隆这个仓库的人跑不了其中任何一条。**
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
M = 'mutations/mut_action_doc.py'

RULE = '''    # ★★★ README 那些「变异 N/N」的证据必须在仓库里、且跑得起来。
    for u in mutation_evidence_is_reachable():
        print(u)
        bad += 1

'''

CASES = [
    ("弄坏一个变异脚本（跑不了的证据与没有证据一样）",
     [("APPEND", M, "\ndef broken(:\n")]),
]

HOLE_CASES = [
    # ★ 把判据改成永远回空 —— 这是**洞**，不是「被抓住」：
    # 一条把自己弄瞎的规则，判据自己是看不见的（与 Round 73/74 的同类判据一样）。
    ("把判据改成永远回空（洞：它看不见自己瞎了）",
     [(C, "    if not os.path.isdir(d):\n        return [f\"找不到 {MUT_DIR}/ —— README 里那些「变异 N/N」的证据必须在仓库里\"]",
          "    return []\n    if not os.path.isdir(d):\n        return []")]),
    ("规则与违规一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        ("APPEND", M, "\ndef broken(:\n"),
    ]),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


def norm(triples):
    """("APPEND", path, text) 与 (path, old, new) 统一成 (path, old, new, is_append)。"""
    out = []
    for t in triples:
        if t[0] == "APPEND":
            out.append((t[1], None, t[2], True))
        else:
            out.append((t[0], t[1], t[2], False))
    return out


def apply(triples):
    triples = norm(triples)
    originals = {}
    for path, _, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    for path, o, _, is_ap in triples:
        if is_ap:
            continue
        if o not in originals[os.path.join(ROOT, path)]:
            return None, originals
    mutated = dict(originals)
    for path, o, n, is_ap in triples:
        full = os.path.join(ROOT, path)
        if is_ap:
            mutated[full] = mutated[full] + n
        else:
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
        g = run("python3 tools/check_contract.py > /tmp/mm.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        txt = open('/tmp/mm.txt', encoding='utf-8').read()
        right = "解析不了" in txt or "只追踪" in txt or "找不到" in txt
        res.append((name, "抓住（契约守卫非零，且说清是哪一处）") if (caught and right)
                   else (name, f"★★ 漏网或理由不对（caught={caught}）"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mmh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、脚本也坏了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
