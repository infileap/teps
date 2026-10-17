import hashlib, os, subprocess, shutil, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BAK = os.path.join(ROOT, '.mutbak')
env = dict(os.environ); env['PATH'] = os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES = [
 ("一个数两个来源也放行", "adapters/shell/decision.go", 'if o.Cost != 0 {', 'if false {'),
 ("点名没登记的资源的报价也放行", "adapters/shell/decision.go",
  'if !ok {\n\t\t\t\treturn nil, nil, nil, core.Refuse(core.CodeBadRequest,\n\t\t\t\t\t"offer "+o.Action+" names a resource that is not registered: "+o.Resource)',
  'if !ok {\n\t\t\t\treturn nil, nil, nil, core.Refuse(core.CodeBadRequest,\n\t\t\t\t\t"x"+o.Resource)'),
 ("占不到的报价也照排", "adapters/shell/decision.go",
  'if len(pool.Live(leases, r.ID, now)) >= r.Capacity {',
  'if len(pool.Live(leases, r.ID, now)) >= 1<<40 {'),
 ("钱不从池里取（用调用方填的 0）", "adapters/shell/decision.go",
  'cost = float64(pool.Cost(r, core.Budget{MS: o.MS, Tokens: o.Tokens}).Money)',
  'cost = 0'),
 ("每条报价的账按 action 盖（复现原 bug）", "adapters/shell/decision.go",
  'Tag: strconv.Itoa(len(offers)),', 'Tag: "0",'),
 ("Tag 越界也回一条错的账", "adapters/shell/decision.go",
  'if err != nil || i < 0 || i >= len(meta) {', 'if err != nil {'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c, shell=True, cwd=ROOT, env=env, capture_output=True, text=True, timeout=1800)
res=[]
for name, path, old, new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK, os.path.basename(path)+'.prbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=b0, "文件没变，这不是证据"
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name, f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:32s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
