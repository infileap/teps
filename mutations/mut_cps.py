import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("成功数永远不加","adapters/shell/decision.go",'if *rec.OK {\n\t\tst.ok++\n\t}','if false {\n\t\tst.ok++\n\t}'),
 ("失败也计成成功","adapters/shell/decision.go",'if *rec.OK {\n\t\tst.ok++\n\t}','if true {\n\t\tst.ok++\n\t}'),
 ("分母为零也说有定义","adapters/shell/metrics.go",'if successes == 0 {','if false {'),
 ("每次成功成本不算（恒为 0）","adapters/shell/metrics.go",'cost["per_success"] = round(spent / float64(successes))','cost["per_success"] = 0.0'),
 ("每次成功成本报成花费本身","adapters/shell/metrics.go",'cost["per_success"] = round(spent / float64(successes))','cost["per_success"] = round(spent)'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.cpsbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=b0,"文件没变"
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:32s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
