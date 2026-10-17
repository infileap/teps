import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
os.makedirs(BAK, exist_ok=True)
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
R='store/register.go'
CASES=[
 ("不核上限（复现原缺陷）", R,
  'if evidenceRank(m.Evidence) > evidenceRank(ceiling) {','if false {'),
 ("上限取第一个孩子而不是最小（复现原缺陷）", R,
  'ceiling := MinEvidenceAll(depEvidence)','ceiling := depEvidence[0]'),
 ("等于上限也拒（把天花板当等级）", R,
  'if evidenceRank(m.Evidence) > evidenceRank(ceiling) {','if evidenceRank(m.Evidence) >= evidenceRank(ceiling) {'),
 ("入账时把孩子等级顶上去（我第一次改错的那个）", R,
  '	res.Evidence = m.Evidence','	res.Evidence = E1'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.lbak')
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
