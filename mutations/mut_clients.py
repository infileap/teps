import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("server.json 缺 version","server.json",'"version": "0.1.0",\n  "websiteUrl"','"websiteUrl"'),
 ("packages 项的传输改成未知类型","server.json",'"type": "stdio"','"type": "carrier-pigeon"'),
 ("launch 指向一个不存在的文件","server.json",'./adapters/mcp','./adapters/nope'),
 ("CLIENTS.md 提到一个不存在的文件","docs/CLIENTS.md",'tools/install_go.sh','tools/install_go_missing.sh'),
 ("SKILL.md 删掉「装技能不等于接服务器」那句","SKILL.md",'does not register','does not'),
 ("SKILL.md 的 name 与目录名不一致","SKILL.md",'name: teps','name: tepz'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=600)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT, '.agents/skills/teps/SKILL.md' if path=='SKILL.md' else path)
    run("git checkout -- "+os.path.relpath(full,ROOT))
    src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    r=run("python3 tools/check_clients.py 2>&1")
    res.append((name, "抓住（守卫退出 %d）" % r.returncode if r.returncode!=0 else "★★ 漏网"))
    run("git checkout -- "+os.path.relpath(full,ROOT))
print()
for n,r in res: print(f"  {r:24s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
