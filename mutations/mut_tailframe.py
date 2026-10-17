# 变异：把「Tail 也要受帧上限约束」逐条拆掉。
#
# 这一轮的来历：MaxTail 是条数上限、MaxFrameBytes 是一个帧的上限，两个数各自
# 独立选出来，于是条数上限能造出一个违反帧上限的回答。实测（5 条 300 KiB 的
# 记录、audit.tail limit:1000）回答 1537486 字节 = 1.47 MiB，而 MaxFrameBytes
# 是 1 MiB，它的注释写着「caps one frame」。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
F='core/log.go'

CASES=[
 ("字节预算退回「不设」（原缺陷）", F, [
   ('''	budget := MaxFrameBytes - tailFrameReserve''', '''	budget := 1 << 62'''),
 ]),

 ("字节预算连第一条也卡（装不下就一条都不回）", F, [
   ('''		if len(out) > 0 && size > budget {''', '''		if size > budget {'''),
 ]),
 ("Append 不查记录大小（链会因此再也读不回来）", F, [
   ('''	if len(line) > MaxFrameBytes {
		return Entry{}, Refuse(CodeFrameTooLarge,
			"record is "+itoa(len(line))+" bytes and the chain reads one record per line capped at "+
				itoa(MaxFrameBytes)+": a record the chain cannot read back is not a record, and "+
				"writing it would make the whole chain unreadable rather than just this one")
	}
''', ''''''),
 ]),
 ("窗口预算超过帧上限（回答会越界）", F, [
   ('''	budget := MaxFrameBytes - tailFrameReserve''', '''	budget := MaxFrameBytes + tailFrameReserve'''),
 ]),
 ("不把窗口翻回链序", F, [
   ('''	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
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
