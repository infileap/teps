import hashlib, os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("远端也可以发布（复现原缺陷）","store/manifest.go",
  'if m.Env.Network {','if false {'),
 ("非回环地址也放行","store/manifest.go",
  'if !isLoopback(r.Socket) {','if false {'),
 ("黑名单式判断（只拒 example.com）","store/manifest.go",
  'return host == "127.0.0.1" || host == "localhost" || host == "::1"',
  'return host != "example.com"'),
 ("没有制品哈希也放行","store/manifest.go",
  'if r.Artifact == "" {','if false {'),
 ("制品不查是不是哈希","store/manifest.go",
  'if _, _, ok := ValidDigest(r.Artifact); !ok {','if false {'),
 ("重复套接字不查","store/manifest.go",
  'if seen[r.Socket] {','if false {'),
 ("空的 socket 也放行","store/manifest.go",
  'if r.Socket == "" {','if false {'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
def check(cmd):
    # 「被抓住」= 这条检查从通过变成不通过
    return run(cmd).returncode != 0
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path)
    src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    assert md5(full)!=hashlib.md5(src.encode()).hexdigest()
    b=run("tools/go build ./... 2>&1")
    if b.returncode!=0:
        res.append((name,"抓住（编译失败——不算行为证据）"))
    else:
        tf=run("tools/go test ./... -count=1 2>&1").returncode!=0
        df=check("bash tools/drive.sh >/dev/null 2>&1")
        res.append((name, ("抓住" if (tf or df) else "★★ 漏网")+f"（go {'红' if tf else '绿'} drive {'红' if df else '绿'}）"))
    run("git checkout -- "+path)
print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 是行为证据")
sys.exit(1 if surv else 0)
