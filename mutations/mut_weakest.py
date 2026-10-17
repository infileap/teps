import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("永远点名第一个因子（不挑最小的）", 'adapters/shell/metrics.go',
  'if f.v < weakest {', 'if false {'),
 ("并列时按 map 顺序（去掉排序）", 'adapters/shell/metrics.go',
  '\tsort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })\n', ''),
 ("weakest_factor 报的是平均", 'adapters/shell/metrics.go',
  'capability["weakest_factor"] = round(wf)', 'capability["weakest_factor"] = round(reliabilityFactor)'),
 ("weakest_name 写死成 coverage", 'adapters/shell/metrics.go',
  'capability["weakest_name"] = wname', 'capability["weakest_name"] = "coverage.intercept_rate"'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    open(full,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:32s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
