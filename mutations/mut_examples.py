import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("必败那一半被删掉","examples/negative-controls/cases.json",'"must_fail": {','"must_fail_off": {'),
 ("两半期望同一个判定","examples/negative-controls/cases.json",'"assert": "exit != 0",\n        "expect": "failed"','"assert": "exit != 0",\n        "expect": "ok"'),
 ("expect 是一个未知判定","examples/negative-controls/cases.json",'"expect": "failed"','"expect": "broken"'),
 ("重复的 id","examples/negative-controls/cases.json",'"id": "empty-needle"','"id": "truncated-tail"'),
 ("run.sh 里没有 curl","examples/negative-controls/run.sh",'curl -sS','curlX -sS'),
 ("run.sh 要先装东西","examples/negative-controls/run.sh",'req() {','req() { pip install teps;'),
 ("必过那一半期望 unknown","examples/negative-controls/cases.json",'"assert": "stdout contains \\"n\\"",\n        "expect": "ok"','"assert": "stdout contains \\"n\\"",\n        "expect": "unknown"'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.exbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=b0,"文件没变"
    g=run("python3 tools/check_examples.py 2>&1")
    d=run("bash examples/negative-controls/run.sh 2>&1")
    gr = g.returncode!=0; dr = "FAIL" in d.stdout or "不合格" in d.stdout
    res.append((name, ("抓住（守卫红）" if gr else "")+(" 抓住（跑红）" if dr else "") if (gr or dr) else "★★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:26s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
