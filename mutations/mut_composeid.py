import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
ORIG='identity = composeSkillName(spec.Op) + ":" + planner.ShapeID(spec.Op, ids)[:12]'
CASES=[
 ("内联身份退回到只有算子（复现原缺陷）", ORIG,
  'identity = composeSkillName(spec.Op) + ":" + planner.ShapeID(spec.Op, nil)[:12]'),
 ("身份里混进 attempt（每次运行都独一无二）", ORIG,
  'identity = composeSkillName(spec.Op) + ":" + req.Attempt + planner.ShapeID(spec.Op, nil)[:0]'),
 ("孩子的顺序不再算进形状", 'ids = append(ids, pr.man.SkillID)',
  'ids = append(ids, pr.man.SkillID)\n\t\tfor i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {\n\t\t\tids[i], ids[j] = ids[j], ids[i]\n\t\t}'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
p='adapters/shell/compose.go'
for name,old,new in CASES:
    src=open(os.path.join(ROOT,p),encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(os.path.join(ROOT,p),'w',encoding='utf-8').write(src.replace(old,new,1))
    b=run("tools/go build ./... 2>&1")
    if b.returncode!=0:
        res.append((name,"编译失败："+b.stdout.strip().split("\n")[-1][:60]))
    else:
        d=run("bash tools/drive.sh 2>&1")
        df=d.stdout.count("FAIL ")
        res.append((name, f"抓住（drive FAIL={df}）" if df else "★★ 漏网"))
    run("git checkout -- "+p)
print()
for n,r in res: print(f"  {r:40s} {n}")
surv=[r for _,r in res if r.startswith("★★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 是行为证据")
sys.exit(1 if surv else 0)
