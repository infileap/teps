# 变异：把「链上真的写的字段与契约描述的那两条记录必须对得上」逐条拆掉。
#
# 这一轮的来历：契约那一段「拆成两条记录」写的是
#   {"rec": "intent", "trace": ..., "attempt": ..., "session": ...,
#    "task_class": ..., "skill_ref": "<内容哈希>", "strategy": ...,
#    "budget": {...}, "action_hash": ...}
#   {"rec": "resolution", "of": "<intent head 哈希>",
#    "outcome": "ok|failed|timeout|refused",
#    "verified_by": "verifier", "verify_assert": ..., "gain": ..., "actual": {...}}
# 而实测（skill.invoke 一个 exit 0 的技能，然后读盘）：
#   key=a1:intent
#     {"attempt":"a1","rec":"execution","skill":"e.one","strategy":"s1","task_class":"task_exec"}
#   key=a1:resolution
#     {"attempt":"a1","latency":2,"ok":true,"rec":"execution","rules":"<哈希>",
#      "skill":"e.one","strategy":"s1","task_class":"task_exec","verified_by":"verify_assert"}
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
D = 'docs/CONTRACT.md'
G = 'adapters/shell/decision.go'

RULE = '''    # ★★★ 链上真的写的字段，与契约描述的那两条记录，必须对得上。
    for u in chain_record_mismatches():
        print(u)
        bad += 1

'''

# 契约里那两行 jsonc（现在是对的）
GOOD_INTENT = '{"rec": "execution", "attempt": "a1", "skill": "e.one",\n "strategy": "s1", "task_class": "task_exec"}'
GOOD_RES = '"ok": true, "verified_by": "verify_assert",'
COST = ', "latency": 2, "cost": 0}'

CASES = [
    ("把 cost 从契约的块里删掉", [(D, COST, ', "latency": 2}')]),
    # ★ 改**标签**而不是删字段 —— 删字段会让 `Rec` 变成未使用的字段，
    # 那只报「编译失败」，而**一个只编译不过的变异不是行为证据**。
    ("把 rec 的 json 标签改掉（代码那一侧）", [
        (G, '\tRec       string  `json:"rec"`', '\tRec       string  `json:"rec_x"`'),
    ]),
    ("把契约的块改回旧的幽灵字段", [
        (D, GOOD_RES, '"outcome": "ok", "verified_by": "verify_assert",\n "gain": 1, "actual": {},'),
    ]),
]

HOLE_CASES = [
    ("规则与契约一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (D, COST, ', "latency": 2}'),
    ]),
]


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


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
        b = run("go build ./... 2>&1")
        g = run("python3 tools/check_contract.py > /tmp/mut_cr.txt 2>&1; echo RC=$?")
        rc = 1 if "RC=1" in g.stdout else 0
        if b.returncode != 0:
            res.append((name, "抓住（编译失败——这不是行为证据）"))
        elif rc:
            txt = open('/tmp/mut_cr.txt', encoding='utf-8').read()
            side = "会说清哪一边" if ("真的会写字段" in txt or "从不写它" in txt) else "★★ 理由没说清哪一边"
            res.append((name, f"抓住（契约守卫非零，{side}）"))
        else:
            res.append((name, "★★ 漏网"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mut_crh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、契约也拆了，没人报）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
