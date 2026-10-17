import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
P='adapters/shell/metrics.go'
CASES=[
 ("字段名退回 observed（与 observations 撞词）",
  'out["chains_run"] = observed', 'out["observed"] = observed\n\tout["chains_run"] = observed'),
 ("字段名退回 favoured",
  'out["chains_favoured"] = favoured', 'out["favoured"] = favoured\n\tout["chains_favoured"] = favoured'),
 ("chains_traced 只列跑过的（清单与计数对不上）",
  'out["chains_traced"] = chains', 'out["chains_traced"] = chains[:observed]'),
 ("basis 不再说它数的是链不是运行",
  '"counts chains, not runs: a chain is counted', '"a chain is counted'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,old,new in CASES:
    src=open(P,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(P,'w',encoding='utf-8').write(src.replace(old,new,1))
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    open(P,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:30s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
