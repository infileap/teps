# 变异：把「一个窗口必须报出它的窗口」逐条拆掉。
#
# 这一轮的来历：链上 25 条、不传 limit 时 audit.tail 回 20 条，
# 而回答里只有 entries / head / seq / epoch —— 调用方分不出
# 「这 20 条是我要的」还是「服务端替我决定的」，也分不出
# 「这就是链」还是「这是链的尾巴」。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='adapters/shell/audit.go'

CASES=[
 ("不报生效的 limit（原缺陷：默认值是无声的）", F, [
   ('''		"limit": body.Limit,''', ''''''),
 ]),
 ("不报 returned（要调用方自己数数组）", F, [
   ('''		"returned": len(rows),''', ''''''),
 ]),
 ("truncated 恒为 true（一个恒真的字段不是读数）", F, [
   ('''		"truncated": seq > uint64(len(rows)),''', '''		"truncated": true,'''),
 ]),
 ("returned 报成请求的 limit 而不是真的回来的行数", F, [
   ('''		"returned": len(rows),''', '''		"returned": body.Limit,'''),
 ]),
 ("窗口两端对调", F, [
   ('''		"first_seq": first,
		"last_seq":  last,''', '''		"first_seq": last,
		"last_seq":  first,'''),
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
