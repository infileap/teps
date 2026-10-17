import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
# 每条变异说清「被抓住」长什么样：我这条检查从通过变成不通过。
CASES=[
 ("规则 4 不再被调用（从 RULES 里拿掉）","tools/check_docs.py",
  "RULES = (check_unbuilt, check_one_status_table, check_reproduced, check_test_counts)",
  "RULES = (check_unbuilt, check_one_status_table, check_reproduced)"),
 ("数不再与实测比（改成永远相等）","tools/check_docs.py",
  "            if want != got:", "            if False:"),
 ("数不点名器官时不再报错（改成猜第一个）","tools/check_docs.py",
  '            if len(claims) != 1 or len(named) != 1:', '            if len(claims) != 1:'),
 ("测试计数改成查表（第二份拷贝）","tools/check_docs.py",
  "            got = count_tests(pkgs[organ])", "            got = want"),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
P='tools/check_docs.py'
for name,path,old,new in CASES:
    src=open(os.path.join(ROOT,path),encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(os.path.join(ROOT,path),'w',encoding='utf-8').write(src.replace(old,new,1))
    st=run("TEPS_DOCS_SELF_TEST=1 python3 tools/check_docs.py 2>&1")
    real=run("python3 tools/check_docs.py 2>&1")
    # ★ 判据不是「退出码非零」：一个崩掉的自检也返回非零。
    # 「被抓住」= 我这条检查从通过变成不通过，而**报出了它自己的样本检查**。
    caught = ("SELF-TEST OK" not in st.stdout) or real.returncode != 0
    res.append((name, "抓住" if caught else "★★ 漏网"))
    open(os.path.join(ROOT,path),'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:10s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
