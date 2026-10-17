# 变异：把「证据在仓库里与跑得起来是两件事」逐条拆掉。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
R = 'tools/run_mutations.sh'

RULE = '''    for u in mutation_runner_is_complete():
        print(u)
        bad += 1

'''

CASES = [
    ("让 runner 漏掉一个脚本（它在目录里但跑不到）",
     [(R, 'scripts=$(cd "$dir" && ls mut_*.py 2>/dev/null | sort)',
          'scripts=$(cd "$dir" && ls mut_*.py 2>/dev/null | sort | grep -v mut_accuracy.py)')]),
    ("把 runner 的 --list 改成什么都不回",
     [(R, '  printf \'%s\\n\' "$scripts"', '  : # 变异：什么都不回')]),
]

HOLE_CASES = [
    ("规则与违规一起拆掉（洞：没人检查这件事）", [
        (C, RULE, ''),
        (R, 'scripts=$(cd "$dir" && ls mut_*.py 2>/dev/null | sort)',
            'scripts=$(cd "$dir" && ls mut_*.py 2>/dev/null | sort | grep -v mut_accuracy.py)'),
    ]),
    ("把 runner 删掉（而判据也被拆了）", [
        (C, RULE, ''),
        ("MOVE", R, "/home/sakiko/inimerse/.teps/.rm_hole.bak"),
    ]),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


def norm(triples):
    out = []
    for t in triples:
        if t[0] == "MOVE":
            out.append((t[1], None, t[2], "move"))
        else:
            out.append((t[0], t[1], t[2], "sub"))
    return out


def apply(triples):
    triples = norm(triples)
    originals = {}
    for path, _, _, _ in triples:
        full = os.path.join(ROOT, path)
        if os.path.exists(full):
            originals[full] = open(full, encoding='utf-8').read()
    for path, o, _, kind in triples:
        if kind == "sub" and o not in originals.get(os.path.join(ROOT, path), ""):
            return None, originals
    for path, o, n, kind in triples:
        full = os.path.join(ROOT, path)
        if kind == "move":
            os.replace(full, n)
        else:
            body = originals[full].replace(o, n)
            open(full, 'w', encoding='utf-8').write(body)
    return True, originals


def restore(originals):
    for full, body in originals.items():
        open(full, 'w', encoding='utf-8').write(body)
    # 被 MOVE 走的要搬回来
    if os.path.exists("/home/sakiko/inimerse/.teps/.rm_hole.bak"):
        os.replace("/home/sakiko/inimerse/.teps/.rm_hole.bak", os.path.join(ROOT, "tools/run_mutations.sh"))


res = []
for name, triples in CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mr.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        txt = open('/tmp/mr.txt', encoding='utf-8').read()
        right = "跑不到它" in txt or "列了" in txt or "找不到" in txt
        res.append((name, "抓住（契约守卫非零，且说清是哪一处）") if (caught and right)
                   else (name, f"★★ 漏网或理由不对（caught={caught}）"))
    finally:
        restore(originals)

for name, triples in HOLE_CASES:
    ok, originals = apply(triples)
    if ok is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        g = run("python3 tools/check_contract.py > /tmp/mrh.txt 2>&1; echo RC=$?")
        if "RC=0" in g.stdout:
            res.append((name, "洞被演示出来（规则拆了、runner 也坏了，没人报）"))
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
