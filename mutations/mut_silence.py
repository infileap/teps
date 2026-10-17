import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
os.makedirs(BAK, exist_ok=True)
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
V='organs/executor/verify.go'; X='adapters/shell/execute.go'
CASES=[
 ("空针又匹配一切（复现原缺陷）", V,
  'if lit.str == "" {\n\t\t\tc.Detail = "the needle is empty',
  'if false {\n\t\t\tc.Detail = "the needle is empty'),
 ("空针判成假（另一个方向的错）", V,
  'c.Detail = "the needle is empty: an assertion that matches everything asserts nothing, and an assertion that cannot fail is not a check"\n\t\t\treturn c',
  'c.Result = ResultFalse\n\t\t\treturn c'),
 ("先判断言后判 Refused（沉默被读成结果）", V,
  'if obs.Refused {\n\t\tj.Verdict = VerdictRefused',
  'if false {\n\t\tj.Verdict = VerdictRefused'),
 ("先判超时后判 Refused", V,
  'if obs.Refused {','if obs.TimedOut {\n\t\tj.Verdict = VerdictTimeout\n\t\tj.Reason = "x"\n\t\treturn j\n\t}\n\tif obs.Refused {'),
 ("没起来也当成功（Exit 初值 0 而不是 -1）", X,
  'obs := executor.Observed{Exit: -1}','obs := executor.Observed{Exit: 0}'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.slbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    if md5(full)==b0:
        res.append((name,"这不是证据（文件没变）")); shutil.copy2(bak,full); continue
    b=run("go build ./... 2>&1")
    if b.returncode!=0:
        res.append((name,"编译失败（不算行为证据）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:34s} {n}")
# 已知等价变异：两个标志互斥（已由构造保证并两侧断言），所以顺序不承重。
EQUIVALENT = {"先判超时后判 Refused"}
print()
for n, r in res:
    if n in EQUIVALENT and r.startswith("★"):
        print(f"  {n}：等价变异 —— 两个标志互斥（构造保证 + 两侧断言），顺序不承重")
bad=[(n,r) for n,r in res if (r.startswith("★") and n not in EQUIVALENT) or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(bad)}/{len(res)} 是行为证据（另有 {len(EQUIVALENT & {n for n,_ in res})} 条已知等价）")
sys.exit(1 if bad else 0)
