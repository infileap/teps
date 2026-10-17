import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
P='adapters/shell/metrics.go'
CASES=[
 ("skills 退回数行数（复现原缺陷）", '"skills":       len(distinctSkills),', '"skills":       len(tbl),'),
 ("rows 也退回数技能",             '"rows":         len(tbl),',            '"rows":         len(distinctSkills),'),
 ("distinctSkills 按任务类分组（数法错了）", 'distinctSkills[k.Skill] = struct{}{}', 'distinctSkills[k.Skill+k.TaskClass] = struct{}{}'),
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
