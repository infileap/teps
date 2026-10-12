#!/usr/bin/env python3
"""核对 docs/PROVENANCE.md 第二批里的 2026 系统名。

`web_search` 在本环境返回 `HTTP 402 Insufficient Balance`，所以这份检查**不走搜索 API**。
它直接问两个有公开、稳定、可复现接口的索引：

  * GitHub 仓库搜索  https://api.github.com/search/repositories
  * arXiv            https://export.arxiv.org/api/query

三条纪律，每一条都来自第一批核对里踩到的那个坑：

1. **「存在」不是「它做了那件事」。**
   第一批的教训逐字是「真实的名字 + 真实的论文 + 不属于它的指标」——名字对得上，
   于是没人再查数字。所以每个名字要记下来的是**那个东西自己怎么描述自己**，
   而不是「找到了」。

2. **同名不是证据。**
   三件事必须长得不一样：`SAME-NAME`（有一个名字完全相同的东西）·
   `NEAR-NAME`（有命中，但没有一个名字相同）· `NONE`（一个命中都没有）。
   把后两者折叠成一个「没有」，就是把「查过、没有」和「没查成」混为一谈。

   ★ 而 `SAME-NAME` **只说明有个东西叫这个名字**。第一次跑这份检查时，
   33 个名字里有 14 个拿到 `SAME-NAME`，而那些东西分别是：
   `DS-LoRA` → 一块 LoRaWAN 开发板 · `SLoRA` → 一个纳米孔测序工具 ·
   `HELM` → Kubernetes 的包管理器 · `Lotus` → Filecoin 协议 ·
   `SuperBake` → 一个 CakePHP 仓库 · `EDGE` → 一个 .NET/Node.js 运行时 ·
   `SOLA` → Solana 区块链。
   **只按名字核对，这 14 个全都会「通过」。**

3. **失败要响。**
   一次 403 / 429 / 超时**绝不许**被记成「没找到」——那正是本项目的核心病症
   （一个静默失败伪装成一个正常结果）。出错时脚本以非零码退出。

用法：
    python3 tools/check_provenance.py                 # 全部名字，写 docs/PROVENANCE-CHECK.md
    python3 tools/check_provenance.py --limit 3       # 只查前三个（省配额，用于试跑）
    python3 tools/check_provenance.py --out -         # 打到 stdout

它需要网络。本仓库的其余检查都不需要。
"""

import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

# docs/PROVENANCE.md 第二节列出的名字。脚本持有这份清单，因为它就是这次检查的输入；
# 输出里会记下它到底问过哪些名字，所以「问过的」和「列出来的」不会漂移。
NAMES = [
    "AION Hard Bypass", "ASCENT", "MOPD", "AGNN", "MMP", "SlimInfer", "DualPath",
    "KnapSpec", "SPEE", "EDGE", "Lotus", "OxyGent", "JoyAgent",
    "Infinite-Parameter LLM", "SOLA", "Parameter Updater Experts", "ESSA",
    "In-Place TTT", "决策论 TTT", "TTT-Discover", "PaRSP", "RectLoRA", "SLoRA",
    "Meta-UCF", "DS-LoRA", "RB-LoRA",
    "Titans Fast-Weight Neural Memory Adapter", "δ-mem", "SuperBake",
    "Kimi K3", "Nemotron 3", "Titans++", "HELM",
]

# docs/PROVENANCE.md 第一节里那份文档**引用**的 arXiv 编号，以及它把每一个引用成了什么。
# 右边那一列是「声称」，不是结论——这张表要问的正是「那个编号实际上是什么」。
CITED = [
    ("2605.03190", "Spartacus-1B / Causal Monoid State Compression"),
    ("2605.05699", "（同一节的 KV Cache 压缩依据）"),
    ("2601.10402", "（第一批其余引用）"),
    ("2603.07770", ""), ("2603.10342", ""), ("2603.18030", ""),
    ("2607.14144", ""), ("2607.17181", ""), ("2608.15383", ""),
    ("2608.20622", ""), ("2608.23953", ""), ("2609.00006", ""),
    ("2609.02292", ""), ("2609.13486", ""),
]

# ★ 对照编号。**没有它，一个坏掉的探针和一次「什么都没找到」长得一模一样。**
# 第一次跑这份检查时批量接口对 14 个编号只回了 10 个 entry（而 totalResults 说 14），
# 我差一点把 4 篇真实存在的论文记成「不存在」。
CONTROL_IDS = ["1706.03762", "2303.08774"]

UA = "teps-provenance-check/1.0 (+local reproduction; not a crawler)"
# GitHub 未认证搜索是 10 次/分钟。留出余量。
GITHUB_GAP_S = 7.0
ARXIV_GAP_S = 3.0
TIMEOUT_S = 25


class CheckError(Exception):
    """一次查询没能得到答案。它必须区别于「没有命中」。"""


def fetch(url, accept):
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": accept})
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT_S) as resp:
            return resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        raise CheckError("HTTP %s from %s" % (e.code, url)) from e
    except Exception as e:  # noqa: BLE001 — 任何传输层失败都是「没查成」
        raise CheckError("%s from %s" % (type(e).__name__, url)) from e


def github(name):
    """返回 (verdict, rows)。verdict ∈ SAME-NAME / NEAR-NAME / NONE。

    这一列量的是**同名**，不是核实。一个叫 EDGE 的仓库与「EDGE 做了那件事」
    之间没有任何关系——同名的东西满地都是。
    """
    q = urllib.parse.quote(name)
    raw = fetch(
        "https://api.github.com/search/repositories?q=%s&per_page=5" % q,
        "application/vnd.github+json",
    )
    data = json.loads(raw)
    items = data.get("items") or []
    rows = [
        {
            "full_name": it.get("full_name", ""),
            "description": (it.get("description") or "").strip(),
            "stars": it.get("stargazers_count", 0),
            "created_at": (it.get("created_at") or "")[:10],
            "html_url": it.get("html_url", ""),
        }
        for it in items[:5]
    ]
    want = re.sub(r"[^a-z0-9]", "", name.lower())
    for r in rows:
        short = r["full_name"].split("/")[-1]
        if re.sub(r"[^a-z0-9]", "", short.lower()) == want:
            return "SAME-NAME", rows
    return ("NEAR-NAME" if rows else "NONE"), rows


def arxiv(name):
    """返回 (verdict, rows)。判定词的含义与 github() 相同：同名，不是核实。"""
    # 带引号的短语查询；空格与标点交给 arXiv 自己分词。
    phrase = '"%s"' % name.replace('"', "")
    q = urllib.parse.quote('all:%s' % phrase)
    raw = fetch(
        "https://export.arxiv.org/api/query?search_query=%s&max_results=5" % q,
        "application/atom+xml",
    )
    ns = {"a": "http://www.w3.org/2005/Atom"}
    root = ET.fromstring(raw)
    rows = []
    for e in root.findall("a:entry", ns):
        title = " ".join((e.findtext("a:title", "", ns) or "").split())
        published = (e.findtext("a:published", "", ns) or "")[:10]
        link = e.findtext("a:id", "", ns)
        rows.append({"title": title, "published": published, "url": link})
    want = re.sub(r"[^a-z0-9]", "", name.lower())
    for r in rows:
        if want in re.sub(r"[^a-z0-9]", "", r["title"].lower()):
            return "SAME-NAME", rows
    return ("NEAR-NAME" if rows else "NONE"), rows


def arxiv_titles(ids):
    """按编号取标题。返回 (titles, note)。

    ★ 两条纪律：
    1. **对照编号先跑。** 一个对已知存在的编号也回「没有」的探针是坏的，
       而它报出来的东西与「这些编号不存在」一模一样。
    2. **批量与 totalResults 对不上就逐个补，并且说出来。** arXiv 的接口
       在第一次跑这份检查时对 14 个编号只回了 10 个 entry，而 totalResults 写着 14——
       **一个声称 14 条、返回 10 条、不报错的接口，正是本项目一直在抓的那种病。**
       静默截断绝不许被记成「不存在」。
    """
    notes = []

    def batch(group):
        q = urllib.parse.urlencode({"id_list": ",".join(group)})
        raw = fetch("https://export.arxiv.org/api/query?" + q, "application/atom+xml")
        ns = {"a": "http://www.w3.org/2005/Atom", "o": "http://a9.com/-/spec/opensearch/1.1/"}
        root = ET.fromstring(raw)
        total = root.findtext("o:totalResults", "", ns)
        out = {}
        for e in root.findall("a:entry", ns):
            aid = e.findtext("a:id", "", ns).rsplit("/", 1)[-1]
            out[aid.split("v")[0]] = " ".join((e.findtext("a:title", "", ns) or "").split())
        return out, total

    # 对照
    ctl, ctl_total = batch(CONTROL_IDS)
    if not ctl:
        raise CheckError(
            "the control ids %s resolved to nothing: this probe is broken, and a broken probe "
            "cannot tell 'no such paper' from 'no answer'" % CONTROL_IDS
        )
    notes.append("control ok: %s" % "; ".join("%s = %s" % (k, v[:40]) for k, v in sorted(ctl.items())))

    titles, total = batch(ids)
    missing = [i for i in ids if i not in titles]
    if missing:
        notes.append(
            "★ the batch said totalResults=%s and returned %d entries; %d ids were resolved one by one "
            "rather than recorded as missing" % (total, len(titles), len(missing))
        )
        for i in missing:
            one_try, _ = batch([i])
            if i in one_try:
                titles[i] = one_try[i]
    return titles, notes


def render_citations(titles, notes):
    L = []
    L.append("# 第一批：那份文档引用的 arXiv 编号，实际上是什么")
    L.append("")
    L.append("由 `tools/check_provenance.py --citations` 生成，**不走搜索 API**。")
    L.append("")
    L.append("| 编号 | 被引用为 | 它实际上是什么 |")
    L.append("|---|---|---|")
    for aid, claim in CITED:
        t = titles.get(aid)
        L.append("| `%s` | %s | %s |" % (aid, claim or "—", (t or "**没解析到**").replace("|", "\\|")))
    L.append("")
    L.append("## 探针本身")
    L.append("")
    for n in notes:
        L.append("- %s" % n)
    L.append("")
    L.append("## 怎么读这张表")
    L.append("")
    L.append("**编号能解析 ≠ 引用是对的。** 这张表回答的是「那个编号是哪篇论文」，")
    L.append("而它足以回答那个真正的问题：**被引用为 A 的编号，是不是 A？**")
    L.append("")
    L.append("反过来也一样：**编号解析不到 ≠ 那篇论文不存在。** arXiv 的接口会静默截断")
    L.append("（`totalResults` 说 14、返回 10 条），所以没解析到的编号是**逐个补查**过的；")
    L.append("补查仍拿不到，才记成没解析到。而且有一组**对照编号**先跑——")
    L.append("**一个对已知存在的编号也回「没有」的探针是坏的，它报出来的东西和「不存在」一模一样。**")
    L.append("")
    return "\n".join(L)


def one(name, gap_state):
    """查一个名字。任何一层失败都抛出去，绝不降级成 NONE。"""
    out = {"name": name}
    time.sleep(gap_state["github"])
    out["github"], out["github_rows"] = github(name)
    time.sleep(gap_state["arxiv"])
    out["arxiv"], out["arxiv_rows"] = arxiv(name)
    return out


def render(results, errors):
    L = []
    L.append("# 第二批 2026 系统名的核对结果（本地复现）")
    L.append("")
    L.append("由 `tools/check_provenance.py` 生成。**它不走搜索 API**——")
    L.append("`web_search` 在本环境返回 `HTTP 402`，所以它直接问 GitHub 仓库搜索与 arXiv 的公开接口。")
    L.append("")
    L.append("**怎么读这张表：**")
    L.append("")
    L.append("| 记号 | 意思 |")
    L.append("|---|---|")
    L.append("| `SAME-NAME` | 有一个**名字完全相同**的东西存在 |")
    L.append("| `NEAR-NAME` | 有命中，但**没有一个名字相同** |")
    L.append("| `NONE` | 一个命中都没有 |")
    L.append("| `ERROR` | **没查成**——不是「没有」 |")
    L.append("")
    L.append("## ⚠️ 这两列量的是「同名」，不是「核实」")
    L.append("")
    L.append("**`SAME-NAME` 只说明有个东西叫这个名字。** 第一次跑这份检查时，")
    L.append("33 个名字里有 14 个拿到 `SAME-NAME`，而那些东西分别是：")
    L.append("")
    L.append("`DS-LoRA` → 一块 LoRaWAN 开发板 · `SLoRA` → 一个纳米孔测序工具 ·")
    L.append("`HELM` → Kubernetes 的包管理器 · `Lotus` → Filecoin 协议 ·")
    L.append("`SuperBake` → 一个 CakePHP 仓库 · `EDGE` → 一个 .NET/Node.js 运行时 ·")
    L.append("`SOLA` → Solana 区块链 · `ESSA` → 一个博客系统 · `ASCENT` → 一个 HPC 可视化运行时 ·")
    L.append("`AGNN` → 一个视频目标分割网络 · `DualPath` → 2017 年的 Keras 网络 ·")
    L.append("`MMP` → mmpose · `RB-LoRA` → 一个 ComfyUI 的 LoRA 键名转换器 ·")
    L.append("`SPEE` → speech-to-speech")
    L.append("")
    L.append("**只按名字核对，这 14 个全都会「通过」。** 所以这张表里唯一有信息量的列是")
    L.append("最后一列——**那个东西自己怎么描述自己**。判定词是索引，不是结论。")
    L.append("")
    L.append("第一批的教训逐字是「真实的名字 + 真实的论文 + 不属于它的指标」。")
    L.append("这一批是它的反面：**真实的名字 + 真实的项目 + 完全不是那件事。**")
    L.append("")
    L.append("| 名字 | GitHub | arXiv | 找到的东西自己怎么说 |")
    L.append("|---|---|---|---|")
    for r in results:
        gh = "**ERROR**" if "github_error" in r else r["github"]
        ax = "**ERROR**" if "arxiv_error" in r else r["arxiv"]
        said = ""
        if r.get("github_rows"):
            top = r["github_rows"][0]
            said = "%s — %s" % (top["full_name"], top["description"] or "（无描述）")
        elif r.get("arxiv_rows"):
            top = r["arxiv_rows"][0]
            said = top["title"]
        if "github_error" in r or "arxiv_error" in r:
            said = "；".join(x for x in [r.get("github_error", ""), r.get("arxiv_error", "")] if x)
        said = said.replace("|", "\\|")
        L.append("| `%s` | %s | %s | %s |" % (r["name"], gh, ax, said[:300]))
    L.append("")
    if errors:
        L.append("## 没查成的")
        L.append("")
        for e in errors:
            L.append("- %s" % e)
        L.append("")
    n_exact = sum(1 for r in results if r.get("github") == "SAME-NAME" or r.get("arxiv") == "SAME-NAME")
    n_none = sum(
        1
        for r in results
        if "github_error" not in r
        and "arxiv_error" not in r
        and r["github"] == "NONE"
        and r["arxiv"] == "NONE"
    )
    L.append("## 计数")
    L.append("")
    L.append("- 查了 **%d** 个名字" % len(results))
    L.append("- 至少一侧 `SAME-NAME`（**同名，不是核实**）：**%d**" % n_exact)
    L.append("- 两侧都只有 `NEAR-NAME` 或更弱：**%d**" % (len(results) - n_exact))
    L.append("- 两侧都 `NONE`：**%d**" % n_none)
    L.append("")
    L.append("**这批计数不改变本项目的任何一个决定**——它们只是把「未核实」换成")
    L.append("「在某一天、用这两个索引、得到过这样的结果」。一个名字在这张表里是")
    L.append("`SAME-NAME`，说明**有个东西叫这个名字**；它做了什么，这张表没问，")
    L.append("**而且这个名字相同的概率与「它就是那件事」毫无关系**。")
    L.append("")
    return "\n".join(L)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=0, help="只查前 N 个（省配额）")
    ap.add_argument("--out", default="docs/PROVENANCE-CHECK.md")
    ap.add_argument("--citations", action="store_true", help="核对第一批引用的 arXiv 编号")
    args = ap.parse_args()

    if args.citations:
        try:
            titles, notes = arxiv_titles([a for a, _ in CITED])
        except CheckError as e:
            print("ERROR %s" % e, file=sys.stderr)
            return 1
        text = render_citations(titles, notes)
        if args.out == "-":
            print(text)
        else:
            with open(args.out, "w", encoding="utf-8") as f:
                f.write(text)
            print("wrote %s" % args.out, file=sys.stderr)
        return 0

    names = NAMES[: args.limit] if args.limit else NAMES
    gap = {"github": GITHUB_GAP_S, "arxiv": ARXIV_GAP_S}
    results, errors = [], []
    for i, name in enumerate(names, 1):
        try:
            r = one(name, gap)
            results.append(r)
            print(
                "[%2d/%2d] %-45s gh=%-8s arxiv=%s" % (i, len(names), name, r["github"], r["arxiv"]),
                file=sys.stderr,
            )
        except CheckError as e:
            # ★ 绝不降级成 NONE。没查成就是没查成。
            results.append({"name": name, "github_error": str(e)})
            errors.append("%s: %s" % (name, e))
            print("[%2d/%2d] %-45s ERROR %s" % (i, len(names), name, e), file=sys.stderr)

    text = render(results, errors)
    if args.out == "-":
        print(text)
    else:
        with open(args.out, "w", encoding="utf-8") as f:
            f.write(text)
        print("wrote %s (%d names, %d errors)" % (args.out, len(results), len(errors)), file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
