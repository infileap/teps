import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("decay_gap 退回旧式（mean − raw_rate）", 'adapters/shell/metrics.go',
  'reliability["decay_gap"] = round(sum/float64(len(tbl)) - meanOf(undecayed))',
  'reliability["decay_gap"] = round(sum/float64(len(tbl)) - rawRate(successes, observations))'),
 ("mean_undecayed 报成折过的那一份", 'adapters/shell/metrics.go',
  'reliability["mean_undecayed"] = round(meanOf(undecayed))',
  'reliability["mean_undecayed"] = round(meanOf(tbl))'),
 ("undecayedTable 也折了", 'adapters/shell/decision.go',
  'return arbiter.Fold(st.obs, 1)', 'return arbiter.Fold(st.obs, arbiter.DefaultDecay)'),
 ("arbiter.state 的 decay_gap 退回旧式", 'adapters/shell/decision.go',
  '"decay_gap":      round(p.Mean() - pRaw.Mean()),',
  '"decay_gap":      round(p.Mean() - rawRate(s.decisions.ok, n)),'),
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
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
