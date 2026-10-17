import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
P='tools/ef_ledger.py'
CASES=[
 ("前缀不再被判成判不了（从前缀里猜）",
  '    if preview_is_prefix(c):\n        return "unclear", ("预览是 %d 个字符的前缀，切在哪不知道 —— "\n                           "**判不了**。先取全文：%s"\n                           % (len(p), history_cmd(c.get("conv_id"))))',
  '    if False:\n        return "unclear", ""'),
 ("前缀判据改成「看起来没说完」",
  'return len(p) >= PREVIEW_LIMIT', 'return p.endswith(("...", "…", "，", "、"))'),
 ("判不了时不给取全文的命令",
  '                           % (len(p), history_cmd(c.get("conv_id"))))',
  '                           % (len(p), ""))'),
 ("短预览里的收尾也不认了（一律 unclear）",
  '    if hits and not asks:', '    if False:'),
 ("短预览里的问句也不认了",
  '    if asks:', '    if False:'),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,capture_output=True,text=True,timeout=600)
def st():
    r=run("TEPS_EF_LEDGER_SELF_TEST=1 python3 tools/ef_ledger.py")
    return r.returncode!=0 and "SELF-TEST" in r.stdout
res=[]
for name,old,new in CASES:
    src=open(P,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(P,'w',encoding='utf-8').write(src.replace(old,new,1))
    res.append((name, "抓住（自检报出 SELF-TEST）" if st() else "★★ 漏网"))
    open(P,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:26s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
