#!/usr/bin/env python3
"""check_examples.py —— 成对负控的形状守卫。

它守的是**一条性质**：每一对负控都是两半，而且两半声明的是**不同的判定**。

★ 为什么这是一条守卫而不是文档检查：

  一份只有必过用例的声明，证明的是「它通过了」；没被证明的是「它有能力失败」。
  于是 `exit == 0` 的技能只要永远返回 0，就能拿到 E1 ——
  **永远成功和正确地成功，在那份声明下分不出来。**

  而 `failed` 是**唯一**算被驳倒的判定。`unknown` / `timeout` / `refused` 都不是
  「这条断言被证明为假」——把三者混起来，就会让一个坏掉的技能靠「跑不起来」
  拿到双边证据。所以每条负控必须自己声明它期望哪一种，并因此说清它在证明什么。

这个守卫**不建内核**（形状是静态的，跑一遍是 `examples/negative-controls/run.sh` 的事）。
但形状对不对，决定了那一遍跑出来的是证据还是形式。
"""
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CASES = os.path.join(ROOT, "examples", "negative-controls", "cases.json")
RUNNER = os.path.join(ROOT, "examples", "negative-controls", "run.sh")

# 引擎可能给出的五个判定。只有第一个算「被驳倒」。
VERDICTS = ["ok", "failed", "timeout", "refused", "unknown"]
REFUTING = "failed"


def problems_for(doc):
    out = []
    cases = doc.get("cases")
    if not isinstance(cases, list) or not cases:
        return ["cases.json 里没有 cases"]

    ids = [c.get("id", "") for c in cases]
    if len(set(ids)) != len(ids):
        out.append("cases.json 里有重复的 id：%s" % ids)
    for i, c in enumerate(cases):
        tag = c.get("id") or ("#%d" % i)
        if not c.get("id"):
            out.append("第 %d 条没有 id" % i)
        for k in ("catches", "proves", "skill_id", "interpreter", "budget_ms", "files"):
            if not c.get(k):
                out.append("%s 缺 %s" % (tag, k))
        for half in ("must_pass", "must_fail"):
            h = c.get(half)
            if not isinstance(h, dict):
                out.append("%s 缺 %s：一对负控少一半就不是一对" % (tag, half))
                continue
            if not h.get("assert"):
                out.append("%s 的 %s 没有 assert" % (tag, half))
            if h.get("expect") not in VERDICTS:
                out.append("%s 的 %s 期望一个未知判定：%r（已知的是 %s）"
                           % (tag, half, h.get("expect"), "/".join(VERDICTS)))
        mp, mf = c.get("must_pass") or {}, c.get("must_fail") or {}
        if mp.get("expect") and mf.get("expect") and mp.get("expect") == mf.get("expect"):
            out.append("%s 的两半期望**同一个**判定（%s）：这不是一对负控，"
                       "它只是把同一个结论说了两遍" % (tag, mp.get("expect")))
        # ★ 必过那一半期望的不是 ok，本身就是一个可疑信号：一个必过用例
        # 如果连通过都做不到，那它不是必过用例。
        if mp.get("expect") and mp.get("expect") != "ok":
            out.append("%s 的必过那一半期望 %s 而不是 ok：必过用例必须真的通过"
                       % (tag, mp.get("expect")))
        # ★ 期望 failed 的那一半才算一条真的负控；别的都在证明另一种事，
        # 所以必须**在 catches/proves 里说清它在证明什么**。
        if mf.get("expect") and mf.get("expect") != REFUTING:
            if "判不了" not in (c.get("proves", "") + c.get("catches", "")) and \
               "not a check" not in (c.get("proves", "") + c.get("catches", "")):
                out.append("%s 的必败那一半期望 %s（不是 %s），"
                           "而 catches/proves 里没说清它在证明什么"
                           % (tag, mf.get("expect"), REFUTING))
    return out



# 一份会调模型的载荷，与一份负控，是同一条纪律的两个实例：
# **它必须有能力失败**，否则一个永远成功的载荷与一份正确的声明长得一模一样。
LOCAL_PAYLOAD = os.path.join(ROOT, "examples", "local-model", "main.py")
LOCAL_RUNNER = os.path.join(ROOT, "examples", "local-model", "run.sh")


def problems_for_local_model():
    out = []
    if not os.path.exists(LOCAL_PAYLOAD):
        return ["没有 examples/local-model/main.py"]
    if not os.path.exists(LOCAL_RUNNER):
        return ["没有 examples/local-model/run.sh：光有载荷没有跑法"]

    payload = open(LOCAL_PAYLOAD, encoding="utf-8").read()
    runner = open(LOCAL_RUNNER, encoding="utf-8").read()

    # 1. 载荷必须有能力失败。
    #
    # ★ 判据是**那一条分支**，不是「文件里出现过 refute 这个词」。
    # 第一版写成 `if "refute" not in payload`，于是把 `body.get("refute")` 换成
    # `False` 之后它照样通过 —— 文件里别处还有这个词（文档字符串里）。
    # 又一次：**存在性判据冒充一致性判据**。
    branch = re.search(
        r"if\s+[^\n]*refute[^\n]*:\s*\n(?:[^\n]*\n){0,4}?\s*return\s+([1-9]\d*)",
        payload)
    if not branch:
        out.append("local-model/main.py 里没有一条「读到 refute 就返回非零」的分支："
                   "一个永远成功的载荷与一份正确的声明长得一模一样")
    # 2. 跑法必须真的声明那条必败用例。
    if "verify_refute" not in runner:
        out.append("local-model/run.sh 没有声明 verify_refute："
                   "载荷有能力失败，而没有人要求它展示")
    # 3. 跑法不许在默认路径上。一条要跑三分钟的守卫会被从默认路径里拿掉，
    #    然后就没有人跑它了 —— 那比没有守卫更糟，因为它看起来有。
    check_all = os.path.join(ROOT, "tools", "check_all.sh")
    if os.path.exists(check_all) and "local-model" in open(check_all, encoding="utf-8").read():
        out.append("tools/check_all.sh 里跑了 examples/local-model：一次调用约 160 秒，"
                   "一条要跑三分钟的守卫会被从默认路径里拿掉，然后就没有人跑它了")

    # 4. ★ 载荷自己的超时不许短于它声明的预算。
    #    短了的话，杀掉模型的是载荷而不是内核的硬时限 —— 于是「超时」这个判定
    #    永远不会由内核给出，而链上那条「预算是硬时限」的话就成了一句空话。
    m = re.search(r"timeout\s*=\s*(\d+)", payload)
    b = re.search(r'"budget"\s*:\s*\{\s*"ms"\s*:\s*(\d+)', runner)
    if m and b:
        payload_ms = int(m.group(1)) * 1000
        budget_ms = int(b.group(1))
        if payload_ms < budget_ms:
            out.append("local-model/main.py 的超时 %dms 短于它声明的预算 %dms："
                       "那样杀掉模型的是载荷而不是内核的硬时限" % (payload_ms, budget_ms))
    else:
        out.append("读不出 local-model 的超时或预算：那条判据就没人守了")
    return out

RESIDENT_PAYLOAD = os.path.join(ROOT, "examples", "local-model", "resident.py")
RESIDENT_RUNNER = os.path.join(ROOT, "examples", "local-model", "resident.sh")


def problems_for_resident():
    """常驻模型那一份：判据是「声明与载荷说的是同一件事」。"""
    out = []
    if not os.path.exists(RESIDENT_PAYLOAD) or not os.path.exists(RESIDENT_RUNNER):
        return ["没有 examples/local-model/resident.py / resident.sh"]
    payload = open(RESIDENT_PAYLOAD, encoding="utf-8").read()
    runner = open(RESIDENT_RUNNER, encoding="utf-8").read()

    # ★ 守卫读的是**源码**，而源码里的 artifact 是 `"'"$ARTIFACT"'"` ——
    # 第一版因此把 `'` 当成哈希值，报出一句关于引号的话。
    # 一个读不出被声明的东西的守卫，报的是它读的方式。
    # 所以先把变量代进去：`NAME="value"` 的赋值各代一遍。
    resolved = runner
    for name, value in re.findall(r'^([A-Z][A-Z0-9_]*)="([^"]*)"', runner, re.M):
        # 三种写法都要代：`$NAME`、`"$NAME"`、以及 shell 里跨引号的 `'"$NAME"'`
        # （单引号字符串闭、展开变量、再开一个单引号）。
        cross = "'" + '"$' + name + '"' + "'"
        resolved = resolved.replace(cross, value)
        resolved = resolved.replace('"$%s"' % name, value)
        resolved = resolved.replace("$%s" % name, value)

    branch = re.search(
        r"if\s+[^\n]*refute[^\n]*:\s*\n(?:[^\n]*\n){0,4}?\s*return\s+([1-9]\d*)",
        payload)
    if not branch:
        out.append("resident.py 里没有一条「读到 refute 就返回非零」的分支")

    # ★★ 声明与载荷必须说的是同一个套接字。
    # 分开了的话，`env.reaches` 声明的是 A，载荷连的是 B —— 而两个都会「通过」：
    # 商店只核清单，载荷只连它自己那个地址。**同一件事两处写，只更新了一处。**
    declared = re.findall(r'"socket"\s*:\s*"([^"]+)"', resolved)
    connected = re.findall(r'SOCKET\s*=\s*"http://([^/"]+)', payload)
    if not declared:
        out.append("resident.sh 没有声明 env.reaches 的 socket")
    elif not connected:
        out.append("resident.py 里读不出它连的套接字")
    else:
        # tcp:127.0.0.1:18911 ↔ 127.0.0.1:18911
        want = declared[0].split(":", 1)[1] if declared[0].startswith("tcp:") else declared[0]
        if want not in connected[0]:
            out.append("resident.sh 声明连 %s，而 resident.py 连的是 %s："
                       "商店只核清单、载荷只连自己那个地址，两个都会「通过」"
                       % (declared[0], connected[0]))

    # ★ artifact 必须是真哈希。它是**声明**（商店核不了那个文件），
    # 但一条「随便写点什么」的声明连复核的入口都没有。
    arts = re.findall(r'"artifact"\s*:\s*"([^"]+)"', resolved)
    if not arts:
        out.append("resident.sh 的 env.reaches 里没有 artifact")
    elif not re.fullmatch(r"[0-9a-f]{64}", arts[0]):
        out.append("resident.sh 的 artifact 不是 64 位十六进制哈希：%s" % arts[0])
    return out


def run():
    out = []
    if not os.path.exists(CASES):
        return ["没有 examples/negative-controls/cases.json"]
    doc = json.load(open(CASES, encoding="utf-8"))
    out += problems_for(doc)
    out += problems_for_local_model()
    out += problems_for_resident()
    if not os.path.exists(RUNNER):
        out.append("没有 examples/negative-controls/run.sh：光有数据没有跑法，"
                   "别人拿到它也不知道怎么用")
    else:
        text = open(RUNNER, encoding="utf-8").read()
        # ★ 跑法必须只用 curl 与 python3 跟内核说话 —— 那是这个项目的自由判据。
        for banned in ("pip install", "npm install", "go get"):
            if banned in text:
                out.append("run.sh 里有 %s：一个要先装东西才能跑的负控不是负控" % banned)
        if "curl" not in text:
            out.append("run.sh 没有用 curl：那它没有在走这个项目自己的自由判据")
    return out


def self_test():
    """两半：证明它还看得见，证明看见了会红。"""
    out = []
    good = {"cases": [{"id": "a", "catches": "x", "proves": "y", "skill_id": "s",
                       "interpreter": "/bin/sh", "budget_ms": 1000, "files": {"run.sh": "exit 0"},
                       "must_pass": {"assert": "exit == 0", "expect": "ok"},
                       "must_fail": {"assert": "exit != 0", "expect": "failed"}}]}
    if problems_for(good):
        out.append("SELF-TEST  一对合格的负控被判成有问题（误报）")
    same = json.loads(json.dumps(good))
    same["cases"][0]["must_fail"]["expect"] = "ok"
    if not problems_for(same):
        out.append("SELF-TEST  两半期望同一个判定没被抓到（它还看得见吗）")
    unknown = json.loads(json.dumps(good))
    unknown["cases"][0]["must_fail"]["expect"] = "unknown"
    if not problems_for(unknown):
        out.append("SELF-TEST  期望 unknown 却没说清在证明什么，没被抓到（它还看得见吗）")
    missing = json.loads(json.dumps(good))
    del missing["cases"][0]["must_fail"]
    if not problems_for(missing):
        out.append("SELF-TEST  少了一半没被抓到（它还看得见吗）")
    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


def main():
    if os.environ.get("TEPS_EXAMPLES_SELF_TEST") == "1":
        bad = self_test()
        if len(bad) != 1:
            for b in bad:
                print("  " + b)
            print("成对负控守卫自检失败：%d 处" % len(bad))
            return 1
        print("成对负控守卫自检：还看得见（4 类各一个样本），且看见了会红")
        return 1
    out = run()
    if not out:
        doc = json.load(open(CASES, encoding="utf-8"))
        n = len(doc["cases"])
        print("成对负控：%d 对，每对两半、两半判定不同、跑法只用 curl。" % n)
        return 0
    for p in out:
        print("  " + p)
    print("成对负控守卫：%d 处不合格。" % len(out))
    return 1


if __name__ == "__main__":
    sys.exit(main())
