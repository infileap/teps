# 变异：把「梯子上没有的等级必须被拒，不许折成 E0」逐条拆掉。
#
# 这一轮的来历：`compose.build` 是
#   rank := 0.0; if r, ok := evidence.Rank(...); ok { rank = r/4 }
# —— ok 被检查了，然后未知的那一种照样落到 0.0，也就是 E0。
# 实测（两条只有 evidence 不同的候选，预算只够留一条）：
#   a=E4 b=E0 -> kept=["a"]      a=E4 b=E9 -> kept=["a"]   逐字相同
# 而 evidence.Rank 的注释逐字写着那件事不许发生。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
K='adapters/shell/knowledge.go'
M='store/manifest.go'

CASES=[
 ("未知等级折回 E0（原缺陷）", K, [
   ('''		rank, ok := evidence.Rank(evidence.Level(c.Evidence))
		if !ok {
			out.Err = core.Refuse(core.CodeBadRequest,
				"candidate "+strconv.Itoa(i)+" ("+c.ID+") declares evidence "+c.Evidence+
					", which is not a level on the ladder: an unrecognised level is not the weakest "+
					"one, it is an unrecognised one, and folding it onto E0 would turn a typo into a "+
					"trust decision")
			return
		}''',
    '''		rank, _ := evidence.Rank(evidence.Level(c.Evidence))
		_ = i
		_ = strconv.Itoa'''),
 ]),
 ("拒绝时说成「最弱」而不是「读不懂」", K, [
   ('''					", which is not a level on the ladder: an unrecognised level is not the weakest "+
					"one, it is an unrecognised one, and folding it onto E0 would turn a typo into a "+
					"trust decision")''',
    '''					", which is the weakest level")'''),
 ]),
 ("evidenceRank 又丢掉 ok", M, [
   ('''func evidenceRank(l Evidence) (int, bool) {
	return evidence.Rank(l)
}''',
    '''func evidenceRank(l Evidence) (int, bool) {
	r, _ := evidence.Rank(l)
	return r, true
}'''),
 ]),
 ("天花板上的比较符从 > 变成 >=（等于也被拒）", 'store/register.go', [
   ('''		if declaredRank > ceilingRank {''', '''		if declaredRank >= ceilingRank {'''),
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
