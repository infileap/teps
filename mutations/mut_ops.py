import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("ops 表里没有 session. 这一条","adapters/shell/ops.go",
  '{Prefix: "session.", Note: "会话状态机：谁在什么状态、能不能往下走", Handle: s.handleSession},',''),
 ("ops 表里没有 compose.propose 这一条（复现原缺陷）","adapters/shell/ops.go",
  '{Exact: "compose.propose", Note: "组合搜索：从类型里搜出没人写过的链，只提议不运行", Handle: s.handleExecute},',''),
 ("default 不再区分两种未知（复现原缺陷）","adapters/shell/main.go",
  'if claimed != "" {','if false {'),
 ("claimed 永远为空","adapters/shell/main.go",
  'claimed = c.Prefix','_ = c.Prefix'),
 ("ops 不报 prefix，只报 type","adapters/shell/ops.go",
  'row["prefix"] = c.Prefix','row["type"] = c.Prefix'),
 ("ops 按字母排序报族（顺序不再承重）","adapters/shell/ops.go",
  'claims := s.claims()','claims := s.claims()\n\tsort.Slice(claims, func(i, j int) bool { return claims[i].Note < claims[j].Note })'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path)
    run("git checkout -- "+path)
    src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=b0,"文件没变"
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    run("git checkout -- "+path)
run("go build ./... 2>&1")
print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
