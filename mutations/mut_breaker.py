# 变异：把「三个状态、三个计数」逐条拆掉。
#
# 这一轮的来历：`open` 是 `if st != breaker.Closed { open++ }` —— 一个叫 open
# 的数数的是不是闭的。实测同一个宿主三次失败之后：
#   冷却 30s 内  -> {"open":1, hosts:[{"state":"open"}]}
#   冷却过去之后 -> {"open":1, hosts:[{"state":"half-open"}]}
# **同一个 1，两个状态**，而 open 拒绝一切、half-open 放一个探针进去。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='adapters/shell/breaker.go'

CASES=[
 ("open 数「不是闭的」（原缺陷）", F, [
   ('''		switch st {
		case breaker.Open:
			open++
		case breaker.HalfOpen:
			halfOpen++
		default:
			closed++
		}''',
    '''		if st != breaker.Closed {
			open++
		}
		if st == breaker.HalfOpen {
			halfOpen++
		}
		if st == breaker.Closed {
			closed++
		}'''),
 ]),
 ("half_open 把闭着的也算进去", F, [
   ('''		case breaker.HalfOpen:
			halfOpen++''',
    '''		case breaker.HalfOpen:
			halfOpen++
			fallthrough
		case breaker.Closed:
			halfOpen++'''),
 ]),
 ("hosts_total 报成 trip 条数而不是宿主数", F, [
   ('''		"hosts_total": len(rows),''', '''		"hosts_total": len(trips),'''),
 ]),
 ("open 与 half_open 对调", F, [
   ('''		case breaker.Open:
			open++
		case breaker.HalfOpen:
			halfOpen++''',
    '''		case breaker.Open:
			halfOpen++
		case breaker.HalfOpen:
			open++'''),
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
