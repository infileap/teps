# 变异：把「一个没有调用点的导出函数是一个承诺」逐条拆掉。
#
# 这一轮的来历：organs/arbiter/capex.go 的 ShouldFix 注释逐字写着那个比较为什么
# 必须严格，而 adapters/shell/decision.go 的 arbiterFix 自己重写了一遍 v > 0 ——
# 器官那一份是死的，活着的那一份在适配器里。实测：organs/store/evidence 里有
# 13 个导出函数在任何非测试代码里都没有调用点。
#
# ★ 每条变异是一串 (文件, 旧, 新) 三元组 —— 因为「守卫的洞」那两条要同时改
# **两个**文件（守卫本身，以及往器官里注入一个孤儿）。第一版把「文件」写在
# 用例一级，于是往器官里注入的那一处被拿去 tools/check_organs.py 里找，
# 报的是「模式没匹配」—— 而模式其实在，只是找错了文件。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')

D = 'adapters/shell/decision.go'
O = 'tools/check_organs.py'
R = 'organs/reflex/reflex.go'
DEAD = '// DEAD-API: reflex 的率只报在 reflex.state 的逐行里，这个函数没有调用点'

CASES = [
    ("arbiterFix 又自己判一次（ShouldFix 因此变成死的）", 'guard_red', [
        (D, '\tfix, v, ok := arbiter.ShouldFix(arbiter.Fixing{',
            '\tv, ok := arbiter.FixValue(arbiter.Fixing{'),
        (D, '\tout.Body = mustJSON(map[string]any{"value": round(v), "fix": fix})',
            '\tout.Body = mustJSON(map[string]any{"value": round(v), "fix": v > 0})'),
    ]),
    ("删掉一个 DEAD-API 标记", 'guard_red', [
        (R, DEAD + '\n', ''),
    ]),
    # 判据是反的：守卫**保持绿**才算演示成功（它放过了那个注入的孤儿）。
    ("守卫把定义文件整个排除（定义行因此成了调用点）", 'guard_green', [
        (O, '                        if p2 == path:\n'
            '                            body = "\\n".join(\n'
            '                                "" if EXPORTED_FUNC.match(l) else l\n'
            '                                for l in body.splitlines())\n', ''),
        (R, DEAD, DEAD + '\n\n'
            '// OrphanProbe 是一个注入的孤儿：没有任何非测试代码调用它，也没有标记。\n'
            'func OrphanProbe() int { return 1 }'),
    ]),
    ("守卫不剥注释（于是文档注释成了调用点）", 'guard_green', [
        (O, '                        body = strip_comments(open(p2, encoding="utf-8").read())',
            '                        body = open(p2, encoding="utf-8").read()'),
        (R, DEAD, DEAD + '\n\n'
            '// OrphanProbe 是一个注入的孤儿：只有这句注释提到它。\n'
            'func OrphanProbe() int { return 1 }'),
    ]),
]


def run(c):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)


res = []
for name, expected, triples in CASES:
    originals = {}
    for path, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    if any(o not in originals[os.path.join(ROOT, p)] for p, o, _ in triples):
        res.append((name, "模式没匹配（这不是证据）")); continue
    # ★ 逐文件累积：同一个文件上的两处替换必须**叠加**，否则后一处会用原内容
    # 覆盖前一处 —— 那会造出一个只改了一半的树，报出来的「编译失败」不是变异的
    # 证据，是脚本自己的 bug。
    mutated = dict(originals)
    for path, o, n in triples:
        full = os.path.join(ROOT, path)
        mutated[full] = mutated[full].replace(o, n, 1)
    for full, body in mutated.items():
        open(full, 'w', encoding='utf-8').write(body)
    try:
        g = run("python3 tools/check_organs.py 2>&1")
        guard_red = g.returncode != 0
        if expected == 'guard_red':
            if guard_red:
                res.append((name, "抓住（器官守卫非零）"))
            else:
                b = run("go build ./... 2>&1")
                if b.returncode != 0:
                    res.append((name, "抓住（编译失败——这不是行为证据）"))
                else:
                    t = run("go test ./... -count=1 2>&1")
                    tf = t.stdout.count("--- FAIL")
                    res.append((name, f"抓住（go FAIL={tf}）") if tf else (name, "★★ 漏网"))
        else:
            res.append((name, "洞被演示出来（守卫放过了那个注入的孤儿）") if not guard_red
                       else (name, "★★ 守卫抓住了 —— 这条洞不成立，或注入没生效"))
    finally:
        for full, body in originals.items():
            open(full, 'w', encoding='utf-8').write(body)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
