import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
R='adapters/shell/compose.go'
CASES=[
 ("elevate 又被静默忽略（复现原缺陷）", R, 'if req.Elevate {', 'if false {'),
 ("拒绝不给出具体上限", R, 'strconv.Itoa(len(preps))+" children is "+string(ceiling)+". ', '""+". '),
 ("拒绝不说真正的路", R, 'Raise the children, and this composition\'s ceiling rises with them.', ''),
 ("结果里不报天花板", R, 'Ceiling: string(ceiling),', 'Ceiling: "",'),
 ("天花板恒为 E0（不数孩子）", R, 'ceiling = store.MinEvidenceAll(evs)', 'ceiling = store.E0'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.ecbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    if md5(full)==b0:
        res.append((name,"这不是证据（文件没变）")); shutil.copy2(bak,full); continue
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"编译失败（不算行为证据）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:32s} {n}")
bad=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(bad)}/{len(res)} 是行为证据")
sys.exit(1 if bad else 0)
