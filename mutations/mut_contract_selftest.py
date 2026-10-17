# 变异：把「一台守卫在某个时刻红过，不等于它现在还能红」逐条拆掉。
#
# 这一轮的来历：check_all.sh 里有六条 selfcheck，**唯独没有 check_contract.py** ——
# 而它是现在最大的一台守卫（Round 73–84 加了十一条规则，没有一条有自检）。
import os, re, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
C = 'tools/check_contract.py'
A = 'tools/check_all.sh'

CASES = [
    # ① 把一条规则**弄瞎**（正则不再匹配）—— 自检必须报出来。
    ("弄瞎 outcome 那条规则的正则",
     [(C, """OUTCOME_LINE = re.compile(r'"outcome":\\s*"([a-z|]+)"')""",
          """OUTCOME_LINE = re.compile(r'"nope":\\s*"([a-z|]+)"')""")],
     "selftest"),
    ("弄瞎 selfcheck 那条规则的正则",
     [(C, "SELFCHECK_HEAD = re.compile(r'^### ([一二三四五六七八九十]+)条守卫各自被注入测试证过', re.M)",
          "SELFCHECK_HEAD = re.compile(r'^### ([一二三四五六七八九十]+)条NOPE', re.M)")],
     "selftest"),
    # ② 自检自己那一份清单少一条 —— 它**查不出自己的清单**（这是它的边界）。
    ("从 SELF_TESTS 里删掉一条（自检查不出自己的清单）",
     # ★ 要真的**删掉那一行**，不是改个名字 —— 改名字之后自检照样跑它。
     # 而那一行里含一个 `\n` 字面量，用普通字符串替换锚不稳，所以这里用正则。
     # ★ 那一条是**两行**，只删第一行会留下语法错误 —— 而一个只编译不过的
     # 变异不是行为证据（第一版就是这样，它报的是「守卫抓住了」）。
     [("REGEX", C, r'^\s*\("selfcheck-count".*\n\s*selfcheck_count, "九"\),\n', '')],
     "hole"),
    # ③ 把自检从 check_all.sh 里拿掉 —— selfcheck_count() 必须报出来。
    ("把 contract 那条 selfcheck 从 check_all.sh 里删掉",
     [(A, 'selfcheck "守卫自检 contract" "TEPS_CONTRACT_SELF_TEST=1 python3 tools/check_contract.py"\n', '')],
     "guard"),
]


def run(c, timeout=1800):
    return subprocess.run(c, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=timeout)


def norm(triples):
    """把 ("REGEX", path, pat, repl) 与 (path, old, new) 统一成 (path, old, new, is_regex)。"""
    out = []
    for t in triples:
        if t[0] == "REGEX":
            out.append((t[1], t[2], t[3], True))
        else:
            out.append((t[0], t[1], t[2], False))
    return out


def apply(triples):
    triples = norm(triples)
    originals = {}
    for path, _, _, _ in triples:
        full = os.path.join(ROOT, path)
        originals[full] = open(full, encoding='utf-8').read()
    for path, o, _, is_re in triples:
        if is_re:
            continue
        if o not in originals[os.path.join(ROOT, path)]:
            return None, originals
    mutated = dict(originals)
    for path, o, n, is_re in triples:
        full = os.path.join(ROOT, path)
        if is_re:
            mutated[full] = re.sub(o, n, mutated[full], count=1, flags=re.M)
        else:
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
        # ★★★ 判据**不是退出码** —— 自检模式**永远**回 1（与 check_docs.py 同一条
        # 约定），而 check_all.sh 的 selfcheck() 看的是输出里有没有 `SELF-TEST OK`。
        # 第一版按退出码判，于是每一条都「抓住」—— **一个永远为真的判据**。
        if kind == "guard":
            g = run("python3 tools/check_contract.py > /tmp/mut_cg.txt 2>&1; echo RC=$?")
            res.append((name, "抓住（契约守卫非零）") if "RC=1" in g.stdout
                       else (name, "★★ 漏网"))
        else:
            g = run("TEPS_CONTRACT_SELF_TEST=1 python3 tools/check_contract.py 2>&1")
            ok = "SELF-TEST OK" in g.stdout
            if kind == "selftest":
                res.append((name, "抓住（自检没报 SELF-TEST OK：它看见那条规则瞎了）")
                           if not ok else (name, "★★ 漏网：规则瞎了而自检仍然报 OK"))
            else:
                res.append((name, "洞被演示出来（自检查不出自己的清单少了一条）")
                           if ok else (name, "★★ 守卫抓住了 —— 这条洞不成立"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
