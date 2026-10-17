import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("阈值退回 0.5 但名字退回 ok", 'adapters/shell/metrics.go', 'out["favoured"] = favoured', 'out["ok"] = favoured'),
 ("阈值不报出来", 'adapters/shell/metrics.go', 'out["threshold"] = evenOdds', ''),
 ("basis 不再说那条线更难", 'adapters/shell/metrics.go',
  'strictly harder than', 'no stronger than'),
 ("value 用 observed 做分子（自洽判据该红）", 'adapters/shell/metrics.go',
  'out["value"] = round(float64(favoured) / float64(observed))',
  'out["value"] = round(float64(observed) / float64(observed))'),
 ("unrun 不再等于 traced−observed", 'adapters/shell/metrics.go',
  'out["unrun"] = traced - observed', 'out["unrun"] = traced'),
 ("阈值降到 0（那条线不再是那条线）", 'adapters/shell/metrics.go',
  'const evenOdds = 0.5', 'const evenOdds = 0.0'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"编译失败（不是行为证据）"))
    else:
        d=run("bash tools/drive.sh 2>&1")
        df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（drive FAIL={df}）" if df else "★★ 漏网"))
    open(full,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:26s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 是行为证据")
sys.exit(1 if surv else 0)
