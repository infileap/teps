import hashlib, os, subprocess, shutil, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BAK = os.path.join(ROOT, '.mutbak')
env = dict(os.environ)
env['PATH'] = os.path.join(ROOT, 'tools') + ':' + env.get('PATH', '')

CASES = [
    ("不点名也去占槽", "adapters/shell/admission.go",
     'if want == "" && !pick {', 'if false {'),
    ("远端资源也放行（链上会写一句假话）", "adapters/shell/admission.go",
     'if found.Kind != pool.KindLocal {', 'if false {'),
    ("pick 把远端也算进候选", "adapters/shell/admission.go",
     'if c.Kind == pool.KindLocal {', 'if true {'),
    ("点名与 pick 同时给也放行", "adapters/shell/execute.go",
     'if req.Resource != "" && req.Pick {', 'if false {'),
    ("准入算出的钱不扣（等于没定价）", "adapters/shell/execute.go",
     'Money: adm.Money}', 'Money: 0}'),
    ("跑完不释放槽", "adapters/shell/execute.go",
     'if adm.Holder != "" {\n\t\tdefer s.discharge(req.Attempt)\n\t}', 'if false {\n\t\tdefer s.discharge(req.Attempt)\n\t}'),
    ("释放不写链（重放后槽永远占着）", "adapters/shell/admission.go",
     's.pools.append(poolRecord{Rec: "pool", Op: "release", Holder: attempt}, attempt+keyRelease)',
     '_ = attempt'),
    ("租约不比进程活得久", "adapters/shell/admission.go",
     'leaseSlack = 5000', 'leaseSlack = 0'),
]

def md5(p):
    return hashlib.md5(open(p, 'rb').read()).hexdigest()

def run(cmd):
    return subprocess.run(cmd, shell=True, cwd=ROOT, env=env,
                          capture_output=True, text=True, timeout=1800)

results = []
for name, path, old, new in CASES:
    full = os.path.join(ROOT, path)
    bak = os.path.join(BAK, os.path.basename(path) + '.admbak')
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
        results.append((name, f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★ 漏网"))
    shutil.copy2(bak, full)

print()
for n, r in results:
    print(f"  {r:34s} {n}")
surv = [r for _, r in results if r.startswith("★") or "没匹配" in r]
print(f"\n{len(results) - len(surv)}/{len(results)} 被抓住")
sys.exit(1 if surv else 0)
