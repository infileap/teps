# 变异：把「蒸馏的那句话必须跟着状态变」逐条拆掉。
#
# 这一轮的来历：distillApply 无条件回同一句话「established, not confirmed:
# it will answer only after the same stimulus is seen working once more」。
# 而蒸馏同一个刺激两次会把强度从 0.5 推到 0.7、越过阈值 0.6 —— 于是同一份
# JSON 里 `fires: true` 旁边放着一句「它现在还不会回答」。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='adapters/shell/distill.go'

CASES=[
 ("note 退回常量（原缺陷）", F, [
   ('''	note := "established, not confirmed: it will answer only after the same stimulus is seen working once more"
	if fires {
		note = "confirmed: this stimulus has now been seen working twice, so the rule answers with no context at all. Distilling it again would only raise its strength"
	}''',
    '''	note := "established, not confirmed: it will answer only after the same stimulus is seen working once more"'''),
 ]),
 ("note 的两支对调", F, [
   ('''	note := "established, not confirmed: it will answer only after the same stimulus is seen working once more"
	if fires {
		note = "confirmed: this stimulus has now been seen working twice, so the rule answers with no context at all. Distilling it again would only raise its strength"
	}''',
    '''	note := "confirmed: this stimulus has now been seen working twice, so the rule answers with no context at all. Distilling it again would only raise its strength"
	if fires {
		note = "established, not confirmed: it will answer only after the same stimulus is seen working once more"
	}'''),
 ]),
 ("不报阈值", F, [
   ('''		"threshold": reflex.FireThreshold,''', ''''''),
 ]),
 ("阈值报成另一个数", F, [
   ('''		"threshold": reflex.FireThreshold,''', '''		"threshold": 0.5,'''),
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
