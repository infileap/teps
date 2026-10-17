# 变异：把「记录必须全部来自被定价的那一条 offer」逐条拆掉。
#
# 这一轮的来历：escalationDecide 写进链里的 skill / task_class 取的是**请求**，
# 而 utility / p / grant_cost / grant_ms 取的是 arbiter 选中的那一条。两条都是
# skill 动作、技能与价格不同时，链上写下「关于 write.file 做了一次决定，
# 它花 1、效用 8.5」—— 而这三件事没有一件是关于 write.file 的。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='adapters/shell/escalation.go'

CASES=[
 ("记录的 skill 回到请求（原缺陷）", F, [
   ('''		Skill:     chosen.Offer.Skill,''', '''		Skill:     body.Skill,'''),
 ]),
 ("记录的 task_class 回到请求", F, [
   ('''		TaskClass: chosen.Offer.TaskClass,''', '''		TaskClass: body.TaskClass,'''),
 ]),
 ("删掉 skill 一致性检查", F, [
   ('''	if body.Skill != "" && body.Skill != chosen.Offer.Skill {
		out.Err = core.Refuse(core.CodeBadRequest,
			"the decision names skill "+body.Skill+" but the price it cites is for "+
				chosen.Offer.Skill+": a record must name the thing that was priced")
		return
	}
''', ''''''),
 ]),
 ("删掉 task_class 一致性检查", F, [
   ('''	if body.TaskClass != "" && body.TaskClass != chosen.Offer.TaskClass {
		out.Err = core.Refuse(core.CodeBadRequest,
			"the decision names task class "+body.TaskClass+" but the price it cites was read "+
				"under "+chosen.Offer.TaskClass+": recording another one would name a fact that "+
				"never happened")
		return
	}
''', ''''''),
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
