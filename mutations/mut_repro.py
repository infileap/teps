"""方向五的变异。

★ 「把一条检查删掉」这一类变异，判据不能是「守卫变红」——它当然不会变红，
它变红才是奇怪的。判据必须是：

    删掉这条检查 **并且** 注入它本该抓住的那个违规 ⇒ 守卫**漏网**。
    漏网就证明了那条检查是唯一抓住它的东西。

这与 Round 21 的教训是同一条：一个只在失败时说话的检查，与一个被删掉的检查，
输出完全一样。所以这里把「删掉」与「注入」配成一对来判。
"""
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)

CHECKS=[
 ("方向五整段被删掉",
  'tools/check_organs.py',
  '            if not (line.startswith("|") and "本地复现" in line and "✅" in line):',
  '            if True:\n                continue'),
 ("「产物不存在」那一支被删掉",
  'tools/check_organs.py',
  '            if missing:',
  '            if missing and False:'),
 ("「没点名产物」那一支被删掉",
  'tools/check_organs.py',
  '            if not named:',
  '            if False:'),
]
# 每条检查各自该抓住的那个违规
VIOLATION = {
    "方向五整段被删掉": ('docs/PROVENANCE.md', '`examples/local-model/`', '`examples/local-model-gone/`'),
    "「产物不存在」那一支被删掉": ('docs/PROVENANCE.md', '`examples/local-model/`', '`examples/local-model-gone/`'),
    "「没点名产物」那一支被删掉": ('docs/PROVENANCE.md', '（`examples/local-model/`）', ''),
}
res=[]
for name, path, old, new in CHECKS:
    full=os.path.join(ROOT,path); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    vpath, vold, vnew = VIOLATION[name]
    vfull=os.path.join(ROOT,vpath); vsrc=open(vfull,encoding='utf-8').read()
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    open(vfull,'w',encoding='utf-8').write(vsrc.replace(vold,vnew,1))
    # 先证明：这条检查在的时候，这个违规是被抓住的
    run("git stash --quiet 2>/dev/null || true")
    open(full,'w',encoding='utf-8').write(src)
    open(vfull,'w',encoding='utf-8').write(vsrc.replace(vold,vnew,1))
    with_check = run("python3 tools/check_organs.py >/dev/null 2>&1").returncode != 0
    # 再证明：删掉它之后，同一个违规漏网
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    without_check = run("python3 tools/check_organs.py >/dev/null 2>&1").returncode != 0
    run("git checkout -- "+path+" "+vpath)
    if with_check and not without_check:
        res.append((name,"抓住（有它则红、删它则漏 ⇒ 它是唯一来源）"))
    elif not with_check:
        res.append((name,"这条检查本来就没抓住那个违规（不是证据）"))
    else:
        res.append((name,"★★ 删掉之后仍然抓住 ⇒ 这条检查不是唯一来源，另有一条在守"))
print()
for n,r in res: print(f"  {r:44s} {n}")
surv=[r for _,r in res if "不是证据" in r or r.startswith("★★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 是行为证据")
sys.exit(1 if surv else 0)
