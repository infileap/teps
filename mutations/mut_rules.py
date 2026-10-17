import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
os.makedirs(BAK, exist_ok=True)
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("RuleID 不排序（顺序算进身份）","organs/executor/plan.go",
  'canon := append([]string(nil), asserts...)\n\tsort.Strings(canon)','canon := append([]string(nil), asserts...)'),
 ("RuleID 丢掉分隔符","organs/executor/plan.go",
  'h.Write([]byte{0})','_ = 0'),
 ("Plan.Rules 永远为空","organs/executor/plan.go",'p.Rules = RuleID(d.Assert)','p.Rules = ""'),
 ("RulesOf 不去重","organs/arbiter/posterior.go",
  'if !seen[o.Key][id] {','if true {'),
 ("RulesOf 把空值当普通值（丢掉 unrecorded）","organs/arbiter/posterior.go",
  'id = "(unrecorded)"','id = "?"'),
 ("absorb 不抄 rec.Rules","adapters/shell/decision.go",
  'Rules: rec.Rules','Rules: ""'),
 ("capability 无视 mixed","adapters/shell/metrics.go",
  'case mixed > 0:','case false:'),
 ("组合每一步的 resolution 丢掉 Rules","adapters/shell/compose.go",
  'Latency: float64(obs.LatencyMS), Rules: plan.Rules,','Latency: float64(obs.LatencyMS),'),
 ("内联组合的身份退回 compose:serial","adapters/shell/compose.go",
  'identity = composeSkillName(spec.Op) + ":" + planner.ShapeID(spec.Op, ids)[:12]',
  'identity = composeSkillName(spec.Op)'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.rbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    if md5(full)==b0:
        res.append((name,"这不是证据（文件没变）")); shutil.copy2(bak,full); continue
    b=run("go build ./... 2>&1")
    if b.returncode!=0:
        res.append((name,"编译失败（不算行为证据）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:34s} {n}")
bad=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(bad)}/{len(res)} 是行为证据")
sys.exit(1 if bad else 0)
