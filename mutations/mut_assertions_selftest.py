# 变异：把「断言守卫必须还看得见」逐条拆掉。
#
# 这一轮的来历：check_assertions.py 此前没有自检，而它的 needles() 与 report()
# 都是纯的 —— 自检根本不需要碰盘。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
A = 'tools/check_assertions.py'
C = 'tools/check_all.sh'

CASES = [
    # ① 弄瞎 needles() 的正则 ⇒ 自检的「一段都没抓到」那一半必须红。
    ("弄瞎 CALL 的正则（needles 什么都抓不到）",
     [(A, "    r'^expect\\s+'", "    r'^NOPE\\s+'")], "selftest"),
    # ② 让 report() 永远回 0 ⇒ 自检的「有前缀的会红」那一半必须红。
    ("让 report() 永远回 0",
     [(A, "    print(f\"断言前缀风险：{len(hazards)} 处（扫了 {total} 个片段）\")",
          "    print(f\"断言前缀风险：{len(hazards)} 处（扫了 {total} 个片段）\")\n    return 0")], "selftest"),
    # ③ 让 report() 永远回 1 ⇒ 自检的「没前缀的会绿」那一半必须红。
    ("让 report() 永远回 1",
     [(A, "    if not hazards:\n        print(f\"断言前缀风险：0 处",
          "    if not hazards:\n        print(f\"断言前缀风险：0 处")], "hole_check"),
    # ④ 把 selfcheck 从 check_all.sh 里拿掉 ⇒ selfcheck_count() 必须报出来。
    ("把 assertions 那条 selfcheck 从 check_all.sh 里删掉",
     [(C, 'selfcheck "守卫自检 assertions" "TEPS_ASSERTIONS_SELF_TEST=1 python3 tools/check_assertions.py"\n', '')],
     "guard"),
]

# ③ 的修法：直接把 `if not hazards:` 那一支的 return 0 改成 return 1
CASES[2] = ("让「没有风险」那一支回 1",
            [(A, "        print(f\"断言前缀风险：0 处（扫了 {total} 个断言里的完整 JSON 值片段）\")\n        return 0",
                 "        print(f\"断言前缀风险：0 处（扫了 {total} 个断言里的完整 JSON 值片段）\")\n        return 1")],
            "selftest")


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
for name, triples, kind in CASES:
    mutated, originals = apply(triples)
    if mutated is None:
        res.append((name, "模式没匹配（这不是证据）")); continue
    try:
        if kind == "guard":
            g = run("python3 tools/check_contract.py > /tmp/ma_g.txt 2>&1; echo RC=$?")
            res.append((name, "抓住（契约守卫非零）") if "RC=1" in g.stdout else (name, "★★ 漏网"))
        else:
            # ★ 判据看输出里的 SELF-TEST OK，不是退出码（自检模式永远回 1）。
            g = run("TEPS_ASSERTIONS_SELF_TEST=1 python3 tools/check_assertions.py 2>&1")
            ok = "SELF-TEST OK" in g.stdout
            res.append((name, "抓住（自检没报 SELF-TEST OK）") if not ok
                       else (name, "★★ 漏网：判据瞎了而自检仍然报 OK"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:40s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
