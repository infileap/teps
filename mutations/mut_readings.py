import hashlib, os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("raw_rate 报成折过的 mean（两个读数变成同一个）","adapters/shell/decision.go",'return float64(ok) / float64(n)','return float64(ok) / float64(n) * 0.5'),
 ("decay_gap 恒为 0","adapters/shell/decision.go",'"decay_gap":    round(meanOf(tbl) - rawRate(s.decisions.ok, n)),','"decay_gap": 0.0,'),
 ("meanOf 只数第一个键","adapters/shell/decision.go",'for _, p := range tbl {\n\t\tsum += p.Mean()\n\t}','for _, p := range tbl {\n\t\tsum += p.Mean()\n\t\tbreak\n\t}'),
 ("metrics 的 raw_rate 报成折过的","adapters/shell/metrics.go",'reliability["raw_rate"] = round(rawRate(successes, observations))','reliability["raw_rate"] = reliability["mean"].(float64)'),
 ("空记录报成完美","adapters/shell/decision.go",'	if n == 0 {\n\t\treturn 0\n\t}','	if n == 0 {\n\t\treturn 1\n\t}'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=b0,"文件没变"
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★ 漏网"))
    run("git checkout -- "+path)
print()
for n,r in res: print(f"  {r:32s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
