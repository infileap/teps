# 变异：把「一条定义了却没人调用的规则，与一条不存在的规则一样」逐条拆掉。
#
# 这一轮的来历：Round 92 里 mutation_runner_is_complete() 写出来了、
# 却**一次都没被 main() 调用过**。而 check_organs.py 里**早就有**这条判据
# （check_exported_functions_have_callers），只不过它管的是 organs/。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'

CALL_MR = '''    for u in mutation_runner_is_complete():
        print(u)
        bad += 1

'''
CALL_KIND = '''    for u in kind_vocabulary_mismatches():
        print(u)
        bad += 1

'''

CASES = [
    ("让 mutation_runner_is_complete 没人调用（就是 Round 92 那个错）",
     [(C, CALL_MR, '')]),
    ("让 kind_vocabulary_mismatches 没人调用",
     [(C, CALL_KIND, '')]),
    # ★ 而「用注释里提到名字」不该把它救活 —— 那正是第一版判据的错。
    ("把调用点删掉、但在注释里写上名字（判据不许被散文救活）",
     [(C, CALL_KIND, '    # 见 kind_vocabulary_mismatches()\n')]),
]

HOLE_CASES = [
    ("规则与它自己一起拆掉（洞：它抓不到自己的消失）", [
        (C, '''    for u in every_rule_function_is_called():
        print(u)
        bad += 1

''', ''),
    ]),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


def apply(triples):
    originals = {}
    for path, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    for path, o, _ in triples:
        if o not in originals[os.path.join(ROOT, path)]:
            return None, originals
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n, 1)
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    return True, originals


def restore(originals):
    for full, body in originals.items():
        open(full, 'w', encoding='utf-8').write(body)


res = []
for name, triples in CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mrc.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        txt = open('/tmp/mrc.txt', encoding='utf-8').read()
        right = "没有任何地方调用它" in txt
        res.append((name, "抓住（契约守卫非零，且点名了那个函数）") if (caught and right)
                   else (name, f"★★ 漏网或理由不对（caught={caught}）"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mrch.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则抓不到自己的消失）"))
        else:
            res.append((name, "★★ 守卫抓住了 —— 这条洞不成立"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:46s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
