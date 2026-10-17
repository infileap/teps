# 变异：把「分派表声明的每一个族都必须有人认得」这条新规则逐条拆掉。
#
# 这一轮的来历：注入 {Prefix: "teleport.", Handle: s.handleSession} 之后，
# check_frames.py 报「帧类型一致」退出码 0，drive.sh 的 ops 段报
# 「ops 报的每一个族都真的被分派认领（18 个）」—— **两台守卫都绿，
# 而 ops 在宣传一个对什么都答 unknown 的族。**
import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
OPS='adapters/shell/ops.go'
OPS_ANCHOR='''		{Prefix: "ops", Note: "这一张表本身", Handle: s.handleOps},'''

CASES=[
 ("幽灵族：声明了 teleport.，包内没人认得它", OPS, [
   (OPS_ANCHOR,
    '''		{Prefix: "teleport.", Note: "★ 幽灵族", Handle: s.handleSession},
''' + OPS_ANCHOR)]),
 ("指错了人：session. 挂在 handleOps 上", OPS, [
   ('''		{Prefix: "session.", Note: "会话状态机：谁在什么状态、能不能往下走", Handle: s.handleSession},''',
    '''		{Prefix: "session.", Note: "会话状态机：谁在什么状态、能不能往下走", Handle: s.handleOps},''')]),
 ("不带点的名字指错人：ops 挂在 handleSession 上", OPS, [
   (OPS_ANCHOR,
    '''		{Prefix: "ops", Note: "这一张表本身", Handle: s.handleSession},''')]),
 ("表里整条删掉一个族（memory.）：分派就不认它了", OPS, [
   ('''		{Prefix: "memory.", Note: "记忆 ABI：写入不可变、更正只能靠取代", Handle: s.handleKnowledge},
''', '''''')]),
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
        g=run("python3 tools/check_frames.py 2>&1")
        static = g.returncode != 0
        b=run("go build ./... 2>&1")
        if b.returncode!=0:
            res.append((name,"抓住（编译失败——这不是行为证据）"))
        else:
            d=run("bash tools/drive.sh 2>&1")
            df=d.stdout.count("FAIL ")
            if static and df:
                res.append((name,f"抓住（check_frames 非零 + drive FAIL={df}）"))
            elif static:
                res.append((name,"抓住（check_frames 非零，drive 没红）"))
            elif df:
                res.append((name,f"抓住（drive FAIL={df}，check_frames 没红）"))
            else:
                res.append((name,"★★ 漏网"))
    finally:
        open(full,'w',encoding='utf-8').write(original)

print()
for n,r in res: print(f"  {r:42s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
