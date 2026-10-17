import hashlib, os, subprocess, shutil, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BAK = os.path.join(ROOT, '.mutbak')
env = dict(os.environ)
env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')

CASES = [
    ("Live 的边界从 <= 改成 <（正好过期的算活着）",
     "organs/pool/pool.go", "if l.Expires <= now {", "if l.Expires < now {"),
    ("liveCount 把正好过期的算成活着",
     "adapters/shell/pool.go", "if l.Expires > now {", "if l.Expires >= now {"),
    ("容量检查差一（>= 改成 >）",
     "organs/pool/pool.go", ">= r.Capacity {", "> r.Capacity {"),
    ("不按资源定价的那一维收费（复现刚修的 bug）",
     "organs/pool/pool.go", "if r.MoneyPerToken <= 0 {\n\t\treturn 0\n\t}", "if false {\n\t\treturn 0\n\t}"),
    ("Release 不按持有者过滤",
     "organs/pool/pool.go", "if l.Holder == holder {", "if false {"),
    ("Cheapest 不看容量（忙的也选）",
     "organs/pool/pool.go", "if len(Live(leases, r.ID, now)) >= r.Capacity {", "if false {"),
    ("容量为零也放行",
     "organs/pool/pool.go", "r.Capacity < 1 {", "r.Capacity < 0 {"),
    ("零 ttl 也放行",
     "organs/pool/pool.go", "ttl <= 0 {", "ttl < 0 {"),
    ("重复 id 也放行",
     "adapters/shell/pool.go", "if _, exists := s.pools.find(r.ID); exists {", "if false {"),
    ("租约记 ttl 而不是绝对过期时间",
     "adapters/shell/pool.go", "Rec: \"pool\", Op: \"lease\", ID: r.ID, Holder: lease.Holder, Expires: lease.Expires,",
     "Rec: \"pool\", Op: \"lease\", ID: r.ID, Holder: lease.Holder, Expires: body.TTLMS,"),
]

def md5(p):
    return hashlib.md5(open(p, 'rb').read()).hexdigest()

def run(cmd):
    return subprocess.run(cmd, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1200)

results = []
for name, path, old, new in CASES:
    full = os.path.join(ROOT, path)
    bak = os.path.join(BAK, os.path.basename(path) + '.bak')
    shutil.copy2(full, bak)
    src = open(full, encoding='utf-8').read()
    if old not in src:
        results.append((name, "模式没匹配（这不是证据）"))
        shutil.copy2(bak, full)
        continue
    before = md5(full)
    open(full, 'w', encoding='utf-8').write(src.replace(old, new, 1))
    assert md5(full) != before, "文件没变，这不是证据"

    b = run("go build ./... 2>&1")
    if b.returncode != 0:
        results.append((name, "抓住（编译失败）"))
    else:
        t = run("go test ./... -count=1 2>&1")
        d = run("bash tools/drive.sh 2>&1")
        tf = t.stdout.count("--- FAIL")
        df = d.stdout.count("FAIL ")
        if tf or df:
            results.append((name, f"抓住（go FAIL={tf} drive FAIL={df}）"))
        else:
            results.append((name, "★ 漏网"))
    shutil.copy2(bak, full)

print()
for n, r in results:
    print(f"  {r:38s} {n}")
surv = [r for _, r in results if r.startswith("★") or "没匹配" in r]
print(f"\n{len(results) - len(surv)}/{len(results)} 被抓住")
sys.exit(1 if surv else 0)
