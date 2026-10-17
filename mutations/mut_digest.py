import hashlib, os, subprocess, shutil, sys
ROOT=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); BAK=os.path.join(ROOT,'.mutbak')
os.makedirs(BAK, exist_ok=True)
env=dict(os.environ); env['PATH']=os.path.join(ROOT,'tools')+':'+env.get('PATH','')
CAS='store/cas.go'; REG='store/register.go'
CASBAK='sha256:849ec07c8884f4ea21357a62105315d567f9d20e4cda919c578dd0813a712ee2'
CASES=[
 ("未知算法报成损坏（复现原缺陷）", CAS,
  'Code: core.CodeBadRequest,\n\t\t\tReason: "this store implements " + AlgoSHA256',
  'Code: core.CodeRecoveryRequired,\n\t\t\tReason: "this store implements " + AlgoSHA256'),
 ("Get 不拆 digest（前缀形式失效）", CAS,
  'algo, hexsum, ok := ValidDigest(digest)\n\tif algo != AlgoSHA256 {',
  'algo, hexsum, ok := ValidDigest(digest)\n\t_ = hexsum\n\tif false {'),
 ("ValidDigest 不查十六进制", CAS,
  'return algo, hexsum, ValidHash(hexsum)','return algo, hexsum, true'),
 ("Has 退回只认裸哈希（与 Get 不一致）", CAS,
  'algo, hexsum, ok := ValidDigest(digest)\n\tif algo != AlgoSHA256 || !ok {',
  'algo, hexsum, ok := ValidDigest(digest)\n\t_, _ = algo, ok\n\tif !ValidHash(digest) {'),
 ("GetJSON 不查归一化（第三个腿不设防）", CAS,
  'if string(canon) != string(data) {','if false {'),
 ("Resolve 又把四个答案压成一句（复现原缺陷）", REG,
  'if r, ok := err.(*core.Refusal); ok {\n\t\t\treturn nil, nil, r\n\t\t}','if false {\n\t\t\treturn nil, nil, nil\n\t\t}'),
]
def md5(p): return hashlib.md5(open(p,'rb').read()).hexdigest()
def run(c): return subprocess.run(c,shell=True,cwd=ROOT,env=env,capture_output=True,text=True,timeout=1800)
res=[]
for name,path,old,new in CASES:
    full=os.path.join(ROOT,path); bak=os.path.join(BAK,os.path.basename(path)+'.dbak')
    shutil.copy2(full,bak); src=open(full,encoding='utf-8').read()
    if old not in src:
        res.append((name,"模式没匹配（这不是证据）")); shutil.copy2(bak,full); continue
    b0=md5(full); open(full,'w',encoding='utf-8').write(src.replace(old,new,1))
    if md5(full)==b0:
        res.append((name,"这不是证据（文件没变）")); shutil.copy2(bak,full); continue
    b=run("go build ./... 2>&1")
    if b.returncode!=0:
        res.append((name,"编译失败（不算行为证据）"))
    else:
        t=run("go test ./... -count=1 2>&1"); d=run("bash tools/drive.sh 2>&1")
        tf=t.stdout.count("--- FAIL"); df=d.stdout.count("FAIL ")
        res.append((name,f"抓住（go FAIL={tf} drive FAIL={df}）" if (tf or df) else "★★ 漏网"))
    shutil.copy2(bak,full)
print()
for n,r in res: print(f"  {r:34s} {n}")
bad=[r for _,r in res if r.startswith("★") or "没匹配" in r or "编译失败" in r]
print(f"\n{len(res)-len(bad)}/{len(res)} 是行为证据")
sys.exit(1 if bad else 0)
