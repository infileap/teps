# 变异：把「用量必须带着它的分项」逐条拆掉。
#
# 这一轮的来历：composer.Result 有 Allocated map[string]int（逐段预算）与
# Used int（一个总和）。读的人看到 allocated: {retrieval: 35, recent: 15} 与
# used: 20，分不出那 20 来自哪一段 —— 而「哪一段还有余量」正是调用方决定
# 「还能不能再塞一条」时唯一需要的那个数。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
C='organs/composer/composer.go'
K='adapters/shell/knowledge.go'

CASES=[
 ("不累计分项（原缺陷：只报总数）", C, [
   ('''			res.Used += c.Len
			res.UsedBySection[name] += c.Len''', '''			res.Used += c.Len'''),
 ]),
 ("分项全塞在同一个键上（与一个总数没有区别）", C, [
   ('''			res.UsedBySection[name] += c.Len''', '''			res.UsedBySection["all"] += c.Len'''),
 ]),
 ("分项被后一段覆盖（last wins）", C, [
   ('''			res.UsedBySection[name] += c.Len''', '''			res.UsedBySection[name] = c.Len'''),
 ]),
 ("适配器不报分项", K, [
   ('''		"used_by_section": res.UsedBySection,''', ''''''),
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
