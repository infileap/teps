import os, subprocess, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CASES=[
 ("Weaken 无条件算一次开火（原缺陷）", 'organs/reflex/reflex.go',
  '''func Weaken(r Rule) Rule {
	if Fires(r) {
		r.Fires++
	}
	r.Strength = clamp01(r.Strength - WeakenStep)''',
  '''func Weaken(r Rule) Rule {
	r.Fires++
	r.Strength = clamp01(r.Strength - WeakenStep)'''),
 ("Reinforce 无条件算一次开火", 'organs/reflex/reflex.go',
  '''func Reinforce(r Rule) Rule {
	if Fires(r) {
		r.Fires++
	}
	r.Strength = clamp01(r.Strength + ReinforceStep)''',
  '''func Reinforce(r Rule) Rule {
	r.Fires++
	r.Strength = clamp01(r.Strength + ReinforceStep)'''),
 ("判据取削弱之后的强度（边界上就错）", 'organs/reflex/reflex.go',
  '''	if Fires(r) {
		r.Fires++
	}
	r.Strength = clamp01(r.Strength - WeakenStep)''',
  '''	r.Strength = clamp01(r.Strength - WeakenStep)
	if Fires(r) {
		r.Fires++
	}'''),
 ("extinguished 丢掉「现在不答」那一半", 'adapters/shell/reflex.go',
  '''		} else if r.Fires > 0 {''',
  '''		} else if false {'''),
]
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); src=open(full,encoding='utf-8').read()
    if old not in src: res.append((name,"模式没匹配（这不是证据）")); continue
    open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    b=run("go build ./... 2>&1")
    if b.returncode!=0: res.append((name,"抓住（编译失败）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    open(full,'w',encoding='utf-8').write(src)
print()
for n,r in res: print(f"  {r:34s} {n}")
surv=[r for _,r in res if r.startswith("★") or "没匹配" in r]
print(f"\n{len(res)-len(surv)}/{len(res)} 被抓住")
sys.exit(1 if surv else 0)
