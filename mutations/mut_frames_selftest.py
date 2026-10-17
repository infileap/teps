# 变异：把「帧守卫的眼睛必须还看得见」逐条拆掉。
import os, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env = dict(os.environ); env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')
F = 'tools/check_frames.py'
C = 'tools/check_all.sh'
K = 'tools/check_contract.py'
R = 'README.md'

CASES = [
    ("弄瞎 PREFIX_RE（HasPrefix 认不出来了）",
     [(F, "PREFIX_RE = re.compile(r'HasPrefix\\(f\\.T,\\s*\"([a-z][a-z0-9_]*\\.)\"\\)')",
          "PREFIX_RE = re.compile(r'NOPE\\(\\)')")]),
    ("弄瞎 CASE_RE（多 token 的 case 认不出来了）",
     [(F, "CASE_RE = re.compile(r'case\\s+((?:\"[a-z]+\\.[a-z_]+\"\\s*,?\\s*)+):')",
          "CASE_RE = re.compile(r'NOPE():')")]),
    ("弄瞎 BARE_EQ_RE（`f.T != \"ops\"` 这种不带点的认不出来）",
     [(F, "BARE_EQ_RE = re.compile(r'f\\.T\\s*[!=]=\\s*\"([a-z][a-z0-9_]*)\"')",
          "BARE_EQ_RE = re.compile(r'NOPE()')")]),
    # ★ 反过来：让变量前缀**也**被认出来（那正是「表对守卫隐形」的反面）。
    ("让变量前缀也被认成帧（把 HasPrefix 的正则放宽到变量）",
     [(F, "PREFIX_RE = re.compile(r'HasPrefix\\(f\\.T,\\s*\"([a-z][a-z0-9_]*\\.)\"\\)')",
          "PREFIX_RE = re.compile(r'HasPrefix\\(f\\.T,\\s*([a-zA-Z.]+)\\)')")]),
]

HOLE_CASES = [
    ("把 frames 那条 selfcheck 从 check_all.sh 里删掉",
     [(C, 'selfcheck "守卫自检 frames" "TEPS_FRAMES_SELF_TEST=1 python3 tools/check_frames.py"\n', '')]),
    # ★★ 中文数字表只认到「十」—— 标题升到「十一条」时它会报一个**假的失败**。
    ("中文数字表退回只认单字（于是「十一」被读成 None）",
     [(K, '    if s == "十":\n        return 10\n', '    return CN_NUM.get(s)\n'),
      (R, '### 十一条守卫各自被注入测试证过', '### 十一条守卫各自被注入测试证过')]),
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
        g = run("TEPS_FRAMES_SELF_TEST=1 python3 tools/check_frames.py 2>&1")
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
        g = run("python3 tools/check_contract.py > /tmp/mf.txt 2>&1; echo RC=$?")
        caught = "RC=1" in g.stdout
        if "中文数字表" in name:
            # ★ 这一条是**洞**：判据会报一个假的失败（十一 vs 11），
            # 而那不是「它看见了」—— 判据的判据是「报出来的两个数**不同**」。
            txt = open('/tmp/mf.txt', encoding='utf-8').read()
            same = "说 十一 条守卫有注入自检，而 check_all.sh 里有 11 条" in txt
            res.append((name, "洞被演示出来（它报了一个假的失败：十一 vs 11）") if same
                       else (name, "★★ 守卫没报出那个假失败"))
        else:
            res.append((name, "抓住（契约守卫非零）") if caught else (name, "★★ 漏网"))
    finally:
        restore(originals)

print()
for n, r in res:
    print(f"  {r:44s} {n}")
surv = [r for _, r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res) - len(surv)}/{len(res)} 达到预期")
sys.exit(1 if surv else 0)
