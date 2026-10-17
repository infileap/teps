# 变异：把「搜索的分母必须出现」逐条拆掉。
#
# 这一轮的来历：compose.propose 只回一个 `"catalog": len(catalog)` —— 登记表
# 条数，而真正交给规划器的技能是 len(skills)，两者之间差着 untyped 那一批。
# 实测（登记表 4 个技能、2 个不声明类型）：参与搜索的技能数**没有这个字段**，
# 读的人只能自己做减法。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='adapters/shell/propose.go'

CASES=[
 ("不报 searched（原缺陷）", F, [
   ('''		"searched": len(skills),''', ''''''),
 ]),
 ("searched 报成登记表条数", F, [
   ('''		"searched": len(skills),''', '''		"searched": len(catalog),'''),
 ]),
 ("untyped_count 报成 0", F, [
   ('''		"untyped_count": len(untyped),''', '''		"untyped_count": 0,'''),
 ]),
 ("catalog 与 searched 对调", F, [
   ('''		"catalog": len(catalog),''', '''		"catalog": len(skills),'''),
 ]),
]

def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,pairs in CASES:
    full=os.path.join(ROOT,path)
    original=open(full,encoding='utf-8').read()
    if any(o not in original for o,_ in pairs):
        res.append((name,"模式没匹配（这不是证据）")); continue
    mutated=original
    for o,n in pairs: mutated = mutated.replace(o,n,1)
    open(full,'w',encoding='utf-8').write(mutated)
    try:
        b=run("go build ./... 2>&1")
        if b.returncode!=0:
            res.append((name,"抓住（编译失败——这不是行为证据）"))
        else:
            t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
            tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
            res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    finally:
        open(full,'w',encoding='utf-8').write(original)

print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
