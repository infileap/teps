import os, re, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
P='tools/check_nomodel.py'

# 每条变异说清「被抓住」长什么样：不是「输出里有某句话」，而是
# **我这条检查从通过变成不通过**。极性与检查一一对应。
def probes(out):
    m=re.search(r'边界 (\d+) 条探针', out)
    return int(m.group(1)) if m else None

def caught(name, selfout, realout):
    if name=="引擎侧被排除":        return "没被选中" in selfout
    if name=="内容侧被包含":        return "被选中了" in selfout
    if name=="引擎探针被删":        return probes(selfout) != 3
    if name=="内容探针被删":        return probes(selfout) != 3
    if name=="分母不再报":          return "扫了" not in realout
    return False

CASES=[
 ("引擎侧被排除", 'ENGINE_DIRS = ("core/", "organs/", "adapters/", "store/", "evidence/")',
                  'ENGINE_DIRS = ("organs/", "adapters/", "store/", "evidence/")'),
 ("内容侧被包含", 'ENGINE_DIRS = ("core/", "organs/", "adapters/", "store/", "evidence/")',
                  'ENGINE_DIRS = ("core/", "organs/", "adapters/", "store/", "evidence/", "examples/")'),
 ("引擎探针被删", '    probes += 1\n    if not in_engine:', '    if not in_engine:'),
 ("内容探针被删", '    probes += 1\n    if in_content:', '    if in_content:'),
 ("分母不再报",   '    print(scanned_summary(files))', '    pass'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,old,new in CASES:
    src=open(P,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(P,'w',encoding='utf-8').write(src.replace(old,new,1))
    st=run("TEPS_NOMODEL_SELF_TEST=1 python3 tools/check_nomodel.py 2>&1").stdout
    rl=run("python3 tools/check_nomodel.py 2>&1").stdout
    res.append((name, "抓住" if caught(name, st, rl) else "★★ 漏网"))
    open(P,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:10s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
