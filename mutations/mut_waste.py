# 变异：把「浪费率必须带着它的两个计数出现」这条改动逐条拆掉，看守卫抓不抓得住。
#
# ★ 这个脚本的第一版有一个**自己制造的缺陷**：变异时写的是
#   `src = src.replace(o, n, 1)`，而收尾恢复时写的还是 `src`——于是「恢复」
#   写回的是**变异后**的内容，工作树被留在变异状态里，而脚本报的却是
#   「模式没匹配」。**一个不能恢复的变异脚本比没有变异脚本更糟：它把一次
#   验证变成一次污染。** 现在原内容单独存在 `original` 里。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')

F = 'organs/session/machine.go'
S = 'adapters/shell/session.go'

CASES = [
 ("flagsJSON 不报分母（原缺陷：一个读不出来的比例）", S, [
   ('''		"escalations":          f.Escalations,
		"escalations_degraded": f.EscalationsDegraded,
''', ''''''),
 ]),
 ("Flags() 不带上两个计数（比例与分母分离）", F, [
   ('''		Escalations:         m.Escalations,
		EscalationsDegraded: m.EscalationsDegraded,
''', ''''''),
 ]),
 ("EscalationsDegraded 只数「中间没有 Grant 的子集」（旧注释的读法）", F, [
   ('''	EscalationOpen bool''',
    '''	EscalationOpen bool
	// mutation: 记录这次升级有没有被批过
	EscalationGranted bool'''),
   # ★ 这个标志必须是**粘**的：一次升级被批过，此后整段会话都算「中间有 Grant」。
   # 第一版把它在每次进入 Waiting 时清零，于是「批过 → 又失败 → 再升级」这条
   # 路上它自己把自己抹掉了，变异等于没变异——**一个不改变答案的变异报「漏网」，
   # 报的是变异没生效，不是守卫没抓住。**
   ('''	if next == Waiting {
		out.Escalations++
		out.EscalationOpen = true
	} else if m.State == Waiting {
		out.EscalationOpen = false
	}
	if next == Degraded {
		out.EscalationsDegraded = out.Escalations
	}''',
    '''	if next == Waiting {
		out.Escalations++
		out.EscalationOpen = true
	} else if m.State == Waiting {
		out.EscalationOpen = false
		if next == Executing {
			out.EscalationGranted = true
		}
	}
	if next == Degraded {
		out.EscalationsDegraded = out.Escalations
		if out.EscalationGranted {
			out.EscalationsDegraded = out.Escalations - 1
		}
	}'''),
 ]),
 ("waste 报成分子本身（把计数当成比例）", F, [
   ('''		Waste:            WasteRatio(m.Escalations, m.EscalationsDegraded),''',
    '''		Waste:            float64(m.EscalationsDegraded),'''),
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
        # ★ 恢复写的是**原内容**，不是 src。
        open(full,'w',encoding='utf-8').write(original)

print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
