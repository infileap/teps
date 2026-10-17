import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ)
P='tools/ef_ledger.py'
# 每条变异说清「被抓住」长什么样：我这条检查从通过变成不通过。
CASES=[
 ("游标判据退回回退式（复现原缺陷）",
  '    v2 = d.get("next_cursor_v2")\n    if v2 is not None:\n        return v2 not in STOP_SENTINELS',
  '    return bool(d.get("next_cursor_v2") or d.get("next_cursor"))'),
 ("哨兵 0 不再被认出来",
  'STOP_SENTINELS = ("", "0")', 'STOP_SENTINELS = ("",)'),
 ("合并时不去重（同一会话数两次）",
  '            cid = c.get("conv_id")\n            if cid:\n                seen[cid] = c',
  '            seen[str(len(seen)) + str(c.get("conv_id"))] = c'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=600)
res=[]
for name,old,new in CASES:
    src=open(P,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(P,'w',encoding='utf-8').write(src.replace(old,new,1))
    st=run("TEPS_EF_LEDGER_SELF_TEST=1 python3 tools/ef_ledger.py 2>&1").stdout
    mg=run("python3 tools/ef_ledger.py /tmp/ef_c34.json /tmp/ef_c34b.json /tmp/ef_c34c.json 2>&1").stdout
    if name.startswith("合并"):
        got = ("没有去重" in st)
    else:
        # ★ 判据必须是**那条检查自己会说的话**，不是 "SELF-TEST" 这个词
        #   （自检末尾那条「注入的违规」永远在，所以后者永远为真）。
        got = ("没被认出来" in st) or ("没有去重" in st) or ("读成了" in st)
    res.append((name, "抓住" if got else "★★ 漏网"))
    open(P,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:8s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
