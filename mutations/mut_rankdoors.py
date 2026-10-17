# 变异：把「证据阶梯只有一种读法」逐条拆掉。
#
# 这一轮的来历：同一份 `r, _ := evidence.Rank(l)` 前向器在项目里被写了**三遍**
# （store/manifest.go 的 evidenceRank、adapters/shell/compose.go 的 rankOf、
# adapters/shell/knowledge.go 里内联的一段），而两句注释都说自己读的是
# **唯一那扇门**。Round 48 修了两处，Round 49 修第三处并把规则写进守卫。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
C='adapters/shell/compose.go'
A='tools/check_adapters.py'

CASES=[
 ("elevateCompose 又丢掉 ok（原缺陷）", C, [
   ('''	declaredRank, declaredKnown := evidence.Rank(self.Evidence)
	ceilingRank, ceilingKnown := evidence.Rank(ceiling)''',
    '''	declaredRank, _ := evidence.Rank(self.Evidence)
	ceilingRank, _ := evidence.Rank(ceiling)
	declaredKnown, ceilingKnown := true, true'''),
 ]),
 ("读不懂的上限不再被拒，直接比秩", C, [
   ('''	if !declaredKnown || !ceilingKnown {''', '''	_, _ = declaredKnown, ceilingKnown
	if false {'''),
 ]),
 ("比较符从 >= 变成 >（到顶了还去提级）", C, [
   ('''	if declaredRank >= ceilingRank {''', '''	if declaredRank > ceilingRank {'''),
 ]),
 ("守卫不剥注释了（于是它报的是散文）", A, [
   ('''        code = strip_comments(open(path, encoding="utf-8").read())''',
    '''        code = open(path, encoding="utf-8").read()'''),
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
        g=run("python3 tools/check_adapters.py 2>&1")
        guard = g.returncode != 0
        b=run("go build ./... 2>&1")
        if b.returncode!=0:
            res.append((name,"抓住（编译失败——这不是行为证据）"))
        else:
            t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
            tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
            bits=[]
            if guard: bits.append("守卫非零")
            if tf: bits.append(f"go FAIL={tf}")
            if df: bits.append(f"drive FAIL={df}")
            res.append((name,"抓住（"+(" + ".join(bits))+"）" if bits else "★★ 漏网"))
    finally:
        open(full,'w',encoding='utf-8').write(original)

print()
for n,r in res: print(f"  {r:40s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
