import os, shutil, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
# ★ 探针不能放 .scratch —— check_docs.py 的 SKIP_DIRS 里有它（那是构建/暂存目录）。
# 第一版就放在那里，于是四条「漏网」全是我的探针自己看不见。
PROBE=os.path.join(ROOT,'docs','split_probe.md')
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=600)
def probe(text):
    open(PROBE,'w',encoding='utf-8').write(text)
res=[]
CASES=[
 ("规则1 ⬜ 指向存在的路径", "| x | ⬜ 还没做，见 `tools/check_docs.py` |\n"),
 ("规则2 别处有实现状态表",  "| 实现 | ⬜ 未写 |\n"),
 ("规则3 ✅ 点名不存在的产物","| 本地复现 | ✅ 已做：`tools/gone.py` |\n"),
 ("规则3 ✅ 没点名任何产物",  "| 本地复现 | ✅ 已做 |\n"),
]
for name,text in CASES:
    probe(text)
    d=run("python3 tools/check_docs.py 2>&1")
    o=run("python3 tools/check_organs.py 2>&1")
    got = d.returncode!=0
    leaked = o.returncode!=0
    res.append((name, ("抓住(docs)" if got else "★★ 漏网") + ("  ★ 但 organs 也抓了" if leaked else "")))
    os.remove(PROBE)
# 两条留下的规则：拿真实的 organs/README.md 与 README.md 改一处
CASES2=[
 ("方向1 状态行缺一个器官", "organs/README.md", "| **仲裁器（opex + capex）** | ✅", "| **XXXXX** | ✅"),
 ("方向2 引用不存在的器官", "README.md", "`organs/session/`", "`organs/nosuch/`"),
]
for name,path,old,new in CASES2:
    full=os.path.join(ROOT,path); bak=full+'.splitbak'
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); os.remove(bak); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    o=run("python3 tools/check_organs.py 2>&1")
    d=run("python3 tools/check_docs.py 2>&1")
    res.append((name, ("抓住(organs)" if o.returncode!=0 else "★★ 漏网") + ("  ★ docs 也抓了" if d.returncode!=0 else "")))
    shutil.copy2(bak,full); os.remove(bak)
print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
