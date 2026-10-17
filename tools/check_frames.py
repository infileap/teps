#!/usr/bin/env python3
"""帧守卫：适配器分派的每一个帧类型，契约里都必须有；反之亦然。

这条守卫的来历和 check_contract.py 是同一条，只是换了对象。
本项目已经四次栽在「同一个事实写了两遍、只有一处被更新」上：

  1. 文档的拒绝码表与 core/refusal.go 漂移        → 逼出了 check_contract.py
  2. Manifest.Validate 与 SolveLicense 对同一未知许可证给两个码
  3. 组合与 skill.invoke 的 attempt 键后缀不一致  → 回放守卫有个看不见的洞
  4. 我新加的 compose.build 与 organs/composer 抢同一个帧名 → composer 段七条断言全红

第 4 条就是这条守卫要防的：**帧类型是全局命名空间**。同一个词在同一个系统里指
两件事，代价总会有人付。而且它比前三条更难发现——两边都能跑，只有把两边放在
一起看才知道撞了。

只查「存在性」，不查语义：分派了但没写进契约 = 别人没法知道有这个 op；
写进契约但没人分派 = 文档在承诺一个不存在的入口。
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ADAPTERS = os.path.join(ROOT, "adapters")
CONTRACT = os.path.join(ROOT, "docs", "CONTRACT.md")

# strings.HasPrefix(f.T, "session.") —— 前缀族
# 前缀名允许数字与下划线：第一版只认 [a-z]+，于是把 reflex. 改成 reflex2. 之后
# 守卫**根本看不见这个前缀族**，注入测试里那个方向因此漏网。
PREFIX_RE = re.compile(r'HasPrefix\(f\.T,\s*"([a-z][a-z0-9_]*\.)"\)')
# case "session.open": 以及 case "memory.write", "memory.supersede": —— 具体帧。
# 多值 case 必须一起抓：只抓单值形式会漏掉 memory.write/supersede，
# 而漏掉的那两个正好是「写入不可变」这条公理的入口。
CASE_RE = re.compile(r'case\s+((?:"[a-z]+\.[a-z_]+"\s*,?\s*)+):')
CASE_TOK = re.compile(r'"([a-z]+\.[a-z_]+)"')
# if f.T != "skill.invoke" { —— 也是认领，只是写法不同
EQ_RE = re.compile(r'f\.T\s*[!=]=\s*"([a-z]+\.[a-z_]+)"')
# 契约里的 `x.y` 形式的帧名。
# 排除文件名：`drive.sh`、`check_kernel.py` 与帧名长得一模一样，
# 而守卫的第一次运行确实把它当成了一个帧 —— 假阳性会让真信号被淹没。
DOC_RE = re.compile(r"`([a-z]+\.[a-z_]+)`")
# 裸前缀 `session.` 也是在文档一个前缀族。
DOC_FAM_RE = re.compile(r"`([a-z]+\.)`")
FILE_EXT = (".sh", ".py", ".md", ".go", ".json", ".yml", ".yaml", ".txt")

# ★★★ 分派表现在是一张**表**（`adapters/shell/ops.go` 的 `claims()`），
# 而上面那三个正则扫的是**处理器源码**。两者在 2026-10 之后不是同一个东西了：
# `main.go` 的分派是 `strings.HasPrefix(f.T, c.Prefix)` —— 变量，不是字面量，
# 所以 PREFIX_RE 一个字都看不见。
#
# 后果实测过（注入 `{Prefix: "teleport.", Handle: s.handleSession}`）：
#   · check_frames.py 报「帧类型一致」，退出码 0
#   · drive.sh 的 ops 段报「ops 报的每一个族都真的被分派认领（18 个）」
# 而 `teleport.` 下面的任何一帧，`handleSession` 都会返回 false ——
# **ops 在宣传一个对什么都答「unknown op」的族，两台守卫都说它验过了。**
#
# 根因：那台运行时守卫的判据是「回答里没有『no family claims this prefix』」，
# 而那句话说的是**分派匹配到了前缀**，不是**处理器认得前缀下面的东西**。
# 一个匹配得上、却什么都不认的前缀，回的是不带那句话的 "unknown op ... in the
# family ..."，于是照样通过。**存在性判据又一次假扮成一致性判据** —— 上一次
# 是「认领 vs 没认领」，这一次是「认领且有人管 vs 认领但是空的」。
#
# 所以这里加一条只能静态算的规则：**表声明的每一个前缀，它的处理器必须至少认得
# 该前缀下面的一个具体帧**。一张声明了却什么都认不出的族，是一个承诺。
CLAIM_RE = re.compile(
    r'\{(?:(?:Prefix):\s*"([^"]+)"|(?:Exact):\s*"([^"]+)")\s*,'
    r'\s*Note:\s*"[^"]*"\s*,\s*Handle:\s*s\.([A-Za-z0-9_]+)\s*\}'
)
FUNC_RE = re.compile(r'func \(s \*server\) ([A-Za-z0-9_]+)\(')
# ★ `ops` 是一个**不带点**的帧名：`handleOps` 的判据是 `f.T != "ops"`。
# 上面两个正则都要求 `[a-z]+\.[a-z_]+`，所以它们一个字都看不见它 ——
# 而「不带点的帧名」在这个系统里是合法的（`ops`、以及协议层的 `hello`/`put`）。
BARE_EQ_RE = re.compile(r'f\.T\s*[!=]=\s*"([a-z][a-z0-9_]*)"')
BARE_CASE_RE = re.compile(r'case\s+"([a-z][a-z0-9_]*)"\s*:')


def claims_table():
    """返回 [(kind, value, handler)]，kind 是 'prefix' 或 'exact'。

    这是**分派自己的声明**，而不是处理器源码里长什么样。两者必须对得上，
    而在此之前没有任何东西在管这件事。
    """
    path = os.path.join(ADAPTERS, "shell", "ops.go")
    if not os.path.exists(path):
        return []
    out = []
    for line in open(path, encoding="utf-8"):
        m = CLAIM_RE.search(line)
        if not m:
            continue
        prefix, exact, handler = m.group(1), m.group(2), m.group(3)
        out.append(("prefix" if prefix else "exact", prefix or exact, handler))
    return out


def handler_frames():
    """每个处理器**自己认得**的帧：从它的函数体里扫 case 与 HasPrefix。

    这是「处理器真的管什么」，与表声明它管什么，是两件事。
    """
    bodies = {}
    for fn in sorted(os.listdir(os.path.join(ADAPTERS, "shell"))):
        if not fn.endswith(".go") or fn.endswith("_test.go"):
            continue
        body = open(os.path.join(ADAPTERS, "shell", fn), encoding="utf-8").read()
        marks = [(m.start(), m.group(1)) for m in FUNC_RE.finditer(body)]
        for i, (pos, name) in enumerate(marks):
            end = marks[i + 1][0] if i + 1 < len(marks) else len(body)
            bodies.setdefault(name, body[pos:end])
    out = {}
    for name, body in bodies.items():
        frames = set()
        for pre in PREFIX_RE.findall(body):
            frames.add(pre + "*")
        for group in CASE_RE.findall(body):
            for frame in CASE_TOK.findall(group):
                frames.add(frame)
        for frame in EQ_RE.findall(body):
            frames.add(frame)
        for frame in BARE_EQ_RE.findall(body):
            frames.add(frame)
        for frame in BARE_CASE_RE.findall(body):
            frames.add(frame)
        out[name] = frames
    return out


def check_claims_are_inhabited():
    """表声明的每一个前缀族，它的处理器必须至少认得族里的一个具体帧。

    ★ 这条规则管的是 `ops` 会不会宣传一个空族。`ops` 的回答直接来自这张表，
    所以一个「声明了、却什么都认不出」的族会以「我收这些帧」的口气出现在
    接入方的眼前 —— 而它答的每一帧都是 unknown。**那不是一个拒绝，那是
    看起来做到了。**
    """
    problems = []
    handlers = handler_frames()

    def concrete_under(frames, prefix):
        # 一个具体帧（不是通配 `x.*`）落在 prefix 下面。
        # 前缀不带点时（`ops`）按相等算：`ops` 这个帧名就是它自己。
        if prefix.endswith("."):
            return [f for f in frames if not f.endswith("*") and f.startswith(prefix)]
        return [f for f in frames if not f.endswith("*") and f == prefix]

    def recognizes_prefix(frames, prefix):
        # 这个处理器**认**这个前缀：族里有一个具体帧，或者它整族认领（`x.*`），
        # 或者（不带点时）它认那个名字本身。
        return bool(concrete_under(frames, prefix)) or (prefix + "*") in frames \
            or (not prefix.endswith(".") and prefix in frames)

    # 包内所有处理器认得的全部帧 —— 用于「这个族是不是空的」。
    everything = set()
    for fs in handlers.values():
        everything |= fs

    for kind, value, handler in claims_table():
        if kind != "prefix":
            continue
        frames = handlers.get(handler, set())
        if handler not in handlers:
            problems.append(
                f"表声明 {value} 归 {handler}，但这个包里没有这个函数"
                f"（它认什么无从判断）")
            continue
        # (a) 包内**有人**认得这个前缀下面的东西。这一条抓的是幽灵族：
        #     声明了一个谁都认不出的前缀。
        if not concrete_under(everything, value):
            problems.append(
                f"表声明前缀族 {value}，而这个包里的任何一个处理器都不认得 "
                f"{value} 下面的具体帧 —— ops 会宣传一个对什么都答 unknown 的族")
            continue
        # (b) 被点名的那个处理器自己也认这个前缀。这一条抓的是**指错了人**：
        #     族是真的，但表把它挂在了一个不管它的处理器上 —— 那一帧会走到
        #     这个处理器、被回 false，然后落到 default，而 default 说
        #     「没有族认领这个前缀」，那是假话。
        if not recognizes_prefix(frames, value):
            problems.append(
                f"表声明前缀族 {value} 归 {handler}，而 {handler} 自己不认这个前缀"
                f" —— 这一族会落到 default，然后被告知「没有族认领它」")
    return problems


# 一条明确标了「未实现」的帧不是静默漂移，是**已声明的缺口**。
# 守卫的职责是防静默，不是禁止诚实。
PENDING_MARK = ("未实现", "⬜")


def dispatched():
    """每一个被适配器认领的帧类型，以及它在哪个文件里被认领。"""
    found = {}
    for dirpath, _, files in os.walk(ADAPTERS):
        for fn in sorted(files):
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            path = os.path.join(dirpath, fn)
            rel = os.path.relpath(path, ROOT)
            body = open(path, encoding="utf-8").read()
            for pre in PREFIX_RE.findall(body):
                # 前缀族本身不是帧，但它承诺了「这个前缀下的帧归我」，
                # 所以契约里至少要提到这个前缀。
                found.setdefault(pre + "*", rel)
            for group in CASE_RE.findall(body):
                for frame in CASE_TOK.findall(group):
                    found.setdefault(frame, rel)
            for frame in EQ_RE.findall(body):
                found.setdefault(frame, rel)
    return found


def documented():
    """返回 (文档提到过的帧, 明确标为未实现的帧)。"""
    if not os.path.exists(CONTRACT):
        return set(), set()
    body = open(CONTRACT, encoding="utf-8").read()
    seen, pending = set(), set()
    for line in body.splitlines():
        fams = set(DOC_FAM_RE.findall(line))
        seen |= fams
        for t in DOC_RE.findall(line):
            if t.endswith(FILE_EXT):
                continue
            seen.add(t)
            if any(mark in line for mark in PENDING_MARK):
                pending.add(t)
    return seen, pending


# ★★★ 请求体一律走 decodeBody，不走 json.Unmarshal。
#
# `json.Unmarshal` **静默丢掉不认识的字段**。于是客户端把 `outcome` 拼成 `ok`
# 时，`arbiter.observe` 不拒它 —— 它把这句话**悄悄改成另一句**再照做。实测：
#
#   ① {"attempt":"x1","skill":"s.a","task_class":"task_exec","ok":true}
#      -> {"seq":1,"rev":1,"body":{"head":"cac2ac…","open":1}}   **被当成一次 intent**
#   ② {"attempt":"x1","skill":"s.a","task_class":"task_exec","outcome":"ok"}
#      -> {"err":{"code":"bad_request","reason":"an outcome needs verified_by: …"}}
#
# 两句请求做的事**完全不同**，而**拼错的那一句成功了**。
#
# 契约 A3 说「拒绝是一个值，不是异常」。一个拼错的字段名既不是拒绝，也不是它
# 字面上的那个请求 —— 它是一句被悄悄改成别的话的请求。
#
# 判据是**可算的**：适配器里不许出现 `json.Unmarshal(f.Body` ——
# `f.Body` 就是「这一帧的请求体」，而它只有一扇门（`decodeBody`）。
# 日志回放（`e.Body`）与响应（`out.Body`）不是请求体，不受这一条管。
REQUEST_DECODE = re.compile(r'json\.Unmarshal\(f\.Body')


def check_request_bodies_go_through_decodebody():
    problems = []
    adir = os.path.join(ROOT, "adapters", "shell")
    for name in sorted(os.listdir(adir)):
        path = os.path.join(adir, name)
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        rel = os.path.relpath(path, ROOT)
        for i, line in enumerate(open(path, encoding="utf-8").read().splitlines(), 1):
            if REQUEST_DECODE.search(line):
                problems.append(
                    f"{rel}:{i} 请求体绕过了 decodeBody：json.Unmarshal 静默丢掉不认识的"
                    f"字段，于是一个拼错的字段名不会拒，它会把这句请求改成另一句再照做")
    return problems


def main():
    live = dispatched()
    doc, pending = documented()

    families = {f[:-1] for f in live if f.endswith("*")}
    concrete = {f for f in live if not f.endswith("*")}

    # 具体帧必须**按名字**出现在契约里。
    # 第一版让「前缀族被文档提过」替它下面的每一个帧背书，于是新增一个
    # reflex.teleport 照样通过 —— 注入测试证明那条捷径把守卫的牙拔了。
    # 前缀族只用于另一个方向：认领了整个前缀却一个字都没提。
    missing_doc = sorted(f for f in concrete if f not in doc)
    missing_family = sorted(f for f in families if f not in doc and f.rstrip(".") not in doc)

    # 反向：契约里写了、但没有任何适配器认领它。
    # 明确标了未实现的帧不算「承诺一个不存在的入口」——它承诺的是「这里还没有」。
    undocumented_live = sorted(
        f for f in doc
        if f not in concrete
        and f not in pending
        and not f.endswith(".")
    )

    bad = 0
    if missing_doc:
        print("契约缺这些帧（适配器分派了，但文档没写）：")
        for f in missing_doc:
            print(f"  {f}   ({live[f]})")
        bad += len(missing_doc)
    if missing_family:
        print("契约缺这些前缀族（适配器认领了一整个前缀，但文档没提）：")
        for f in missing_family:
            print(f"  {f}")
        bad += len(missing_family)
    if undocumented_live:
        print("契约写了但没人分派（文档在承诺一个不存在的入口）：")
        for f in undocumented_live:
            print(f"  {f}")
        bad += len(undocumented_live)

    # ★ 第三条：**表**声明的东西也要在契约里。上面那条用的是处理器扫出来的
    # 集合，而 `ops` 的回答直接来自表 —— 表里多一条、契约里没有，接入方就会从
    # `ops` 那里看见一个契约没承诺过的入口。三边（表 / 处理器 / 契约）必须都通。
    # 契约全文，用于**不带点**的帧名：`ops` 这种名字没有点，DOC_RE 与
    # DOC_FAM_RE 都要求有点，所以它一个字都看不见 —— 而它在契约里是写着的
    # （`docs/CONTRACT.md:80` 的 `ops` 那一行）。判据取「被反引号包着的那个
    # 名字本身」，而不是把它塞进一个更松的正则里：一个能匹配任何小写单词的
    # 正则会让这条规则变成永远通过。
    contract_body = open(CONTRACT, encoding="utf-8").read() if os.path.exists(CONTRACT) else ""

    table_undocumented = []
    for kind, value, _handler in claims_table():
        if kind == "prefix":
            if value in doc or value.rstrip(".") in doc:
                continue
            if not value.endswith(".") and f"`{value}`" in contract_body:
                continue
            table_undocumented.append(value)
        elif value not in doc:
            table_undocumented.append(value)
    if table_undocumented:
        print("分派表声明了、而契约没写的帧/族（ops 会把它报给接入方）：")
        for f in sorted(table_undocumented):
            print(f"  {f}")
        bad += len(table_undocumented)

    empty = check_claims_are_inhabited()
    if empty:
        print("表声明的族里，处理器一个具体帧都不认（ops 会宣传一个空族）：")
        for e in empty:
            print(f"  {e}")
        bad += len(empty)

    # ★★★ 第四条：请求体只有一扇门。
    bypass = check_request_bodies_go_through_decodebody()
    if bypass:
        print("这些地方直接 json.Unmarshal 了请求体（绕过 decodeBody）：")
        for b in bypass:
            print(f"  {b}")
        bad += len(bypass)

    if bad:
        print(f"\n共 {bad} 处不一致。帧类型是全局命名空间：撞了名两边都能跑，只有放在一起看才知道。")
        return 1
    table = claims_table()
    print(f"帧类型一致：适配器分派 {len(concrete)} 个具体帧 + {len(families)} 个前缀族，契约里都有；"
          f"分派表声明的 {len(table)} 条认领，每一条的处理器都认得族里的东西；"
          f"请求体全部走 decodeBody。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
