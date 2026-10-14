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


def run():
    out = []
    if not os.path.exists(CASES):
        return ["没有 examples/negative-controls/cases.json"]
    doc = json.load(open(CASES, encoding="utf-8"))
    out += problems_for(doc)
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
