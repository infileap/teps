# 变异：把「三个事实、三个答案」逐条拆掉。
#
# 这一轮的来历：Cheapest 无论为什么找不到候选都回 budget_exhausted，
# 理由是一句「either at capacity or costs more than X」。实测（一个容量 1 的
# 资源、预算给足 1000000）它回的仍然是 budget_exhausted / retryable:false，
# 而挡住它的是**容量** —— 容量会自己好，core/refusal.go:50 早就为它准备了
# CodeLeaseHeld: true。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='organs/pool/pool.go'

CAP_BRANCH = '''	if atCapacity > 0 && overBudget == 0 {
		wait := ""
		if soonest > now {
			wait = "; the soonest lease expires in " + itoa64(soonest-now) + "ms"
		}
		return Resource{}, core.Refuse(core.CodeLeaseHeld,
			"every resource that could do this work is at capacity ("+itoa(atCapacity)+" of "+
				itoa(len(rs))+")"+wait+". This is a lease, not a budget: it frees itself")
	}'''

CASES=[
 ("容量挡着时也回 budget_exhausted（原缺陷）", F, [
   (CAP_BRANCH,
    '''	if atCapacity > 0 && overBudget == 0 {
		return Resource{}, core.Refuse(core.CodeBudgetExhausted,
			"no resource can do this work within the budget: every one is either at capacity or costs more than the budget")
	}'''),
 ]),
 ("容量用对了码，但理由里不点明等多久", F, [
   ('''		wait := ""
		if soonest > now {
			wait = "; the soonest lease expires in " + itoa64(soonest-now) + "ms"
		}''',
    '''		wait := ""'''),
 ]),
 ("登记表空着也走预算分支（四个事实混成三个）", F, [
   ('''	if len(rs) == 0 {
		// 第四个事实：一个资源都没登记。这不是预算问题，也不是容量问题。
		return Resource{}, core.Refuse(core.CodeBadRequest,
			"no resource is registered, so there is nothing to price this work against: "+
				"register one before asking what it costs")
	}''', ''''''),
 ]),
 ("两个都挡着时只说一个原因", F, [
   ('''		"no resource can do this work: "+itoa(atCapacity)+" of "+itoa(len(rs))+
			" are at capacity and the rest cost more than the budget allows (cheapest "+
			itoa64(cheapest.Money)+" money / "+itoa64(cheapest.Tokens)+" tokens against "+
			itoa64(available.Money)+" money / "+itoa64(available.Tokens)+
			" tokens). Waiting frees the capacity; only a bigger budget fixes the rest")''',
    '''		"no resource can do this work: the budget is too small")'''),
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
