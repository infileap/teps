# 变异：把「一个会倒退的计数器不是一个计数器」与「一个没有生产点的拒绝码是一个承诺」
# 逐条拆掉。
#
# 这一轮的来历：
#   ① bumpEpoch 是 `n, _ = strconv.ParseUint(...)` —— 解析错误被丢掉，于是
#      epoch 文件里一个非数字字节就把 epoch 从 2 变回 1。而 Epoch 的注释写着
#      「It grows on every Open, so a client holding a stale epoch can tell that
#      the process it was talking to is gone.」
#   ② 14 个拒绝码里有 5 个生产点是 0，而契约那张表的用法是「适配器作者据此写循环」。
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
L='core/log.go'
C='docs/CONTRACT.md'
S='adapters/shell/session.go'

CASES=[
 ("epoch 的解析错误又被丢掉（原缺陷）", L, [
   ('''			parsed, perr := strconv.ParseUint(raw, 10, 64)
			if perr != nil {
				return Refuse(CodeRecoveryRequired,
					"the epoch file holds "+strconv.Quote(raw)+", which is not a number: "+
						"the epoch must grow on every Open, and a counter that can go backwards "+
						"cannot tell a client that the process it was talking to is gone. "+
						"Fix or remove "+p+" rather than letting the next Open start from one")
			}
			n = parsed''',
    '''			n, _ = strconv.ParseUint(raw, 10, 64)'''),
 ]),
 ("空 epoch 文件被当成损坏", L, [
   ('''		if raw != "" {''', '''		if true {'''),
 ]),
 ("契约里 stale_epoch 的缺口标记被删掉", C, [
   (''' | ❌ **必须先重新 hello** · ⬜ **未实现：这个码目前没有任何生产点** |''',
    ''' | ❌ **必须先重新 hello** |'''),
 ]),
 ("一个码的生产点被删掉（no_session）", S, [
   ('''core.Refuse(core.CodeNoSession, "no session "+name)''',
    '''core.Refuse(core.CodeBadRequest, "no session "+name)'''),
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
        g=run("python3 tools/check_contract.py 2>&1")
        guard = g.returncode != 0
        b=run("go build ./... 2>&1")
        if b.returncode!=0:
            res.append((name,"抓住（编译失败——这不是行为证据）"))
        else:
            t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
            tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
            bits=[]
            if guard: bits.append("契约守卫非零")
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
