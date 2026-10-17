#!/usr/bin/env python3
"""契约守卫：文档里的拒绝码表必须与 core/refusal.go 逐字一致。

这条守卫是被一次真实漂移逼出来的——
drive.sh 断言 `unknown_session`，内核返回 `no_session`，而文档也写着 `unknown_session`。
两边分头演化时谁都不会报错，除非有人把它们放在一起比。
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

CONST_RE = re.compile(r'(Code\w+)\s*=\s*"([a-z_]+)"')
RETRY_RE = re.compile(r"(Code\w+):\s*(true|false)")
ROW_RE = re.compile(r"^\|\s*`([a-z_]+)`\s*\|([^|]*)\|\s*(✅|❌)", re.M)


def read(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
        return f.read()


def go_files():
    """仓库里所有非测试的 .go 文件，跳过 .toolchain 与 tools/。"""
    out = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in (".git", ".toolchain")]
        for fn in filenames:
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            out.append(os.path.join(dirpath, fn))
    return sorted(out)


# ★★★ 一个没有生产点的拒绝码是一个承诺。
#
# 这张表的用法是「适配器作者据此写循环」。于是 `backlog_full` 那一个
# `✅ 退避后重试` 在承诺一次**永远不会到来**的重试 —— 因为没有任何地方会
# 返回这个码。实测：14 个码里有 **5 个**生产点是 0（`stale_epoch`
# `backlog_full` `tainted_input` `quota_exhausted` `shadow_nested`）。
#
# 这与帧表里「写进契约但没人分派 = 文档在承诺一个不存在的入口」是同一条。
# 处置也同一条：**明确标出来的缺口不是静默漂移** —— 契约那一行带上
# `未实现` 或 `⬜` 就不算违规，守卫防的是**静默**，不是禁止诚实。
#
# 判据是**可算的**：`CodeXxx` 这个标识符在 core/refusal.go 之外出现过吗。
# 一个只出现在常量块与 retryable 表里的码，没有任何地方会返回它。
PENDING_MARK = ("未实现", "⬜")


def producers(names):
    """每个码名在 core/refusal.go 之外被引用的次数。"""
    counts = {name: 0 for name in names}
    refusal = os.path.join(ROOT, "core", "refusal.go")
    for path in go_files():
        if os.path.abspath(path) == os.path.abspath(refusal):
            continue
        body = open(path, encoding="utf-8").read()
        for name in names:
            counts[name] += body.count(name)
    return counts


# ★★★ 契约点名过的每一个清单字段，要么**有人执行**，要么**标明没人执行**。
#
# 这一条是被 `idempotent` / `effects` / `preconditions` / `postconditions` /
# `on_failure` 逼出来的：它们都在 `docs/CONTRACT.md` 的 Skill ABI 那一行里，
# 而实测（非测试代码里的读次数）是 **0**。
#
# 一个字段被点名却没人执行，只有**写下来**才算「已声明的缺口」，否则它读起来
# 像一条承诺 —— 与帧表那条（「契约写了但没人分派 = 文档在承诺一个不存在的
# 入口」）是同一条，只是对象换成了字段。
#
# 判据是**可算的**：
#   1. 从清单结构体里取每一个 `json:"名字"` 标签；
#   2. 把它转成 Go 字段名，数非测试代码里的读（`.Name` 后面不是 `=`）；
#   3. 读次数为 0 时，契约的 ABI 段里必须有一个带 `⬜` 的行提到那个 json 名字。
MANIFEST_TAG = re.compile(r'^\t([A-Z]\w*)\s+[^`]*`json:"([^",]+)')


def manifest_fields():
    """清单结构体里的 (Go 字段名, json 名字) 对。"""
    src = read("store/manifest.go")
    out = []
    inside = False
    for line in src.splitlines():
        if re.match(r'^type Manifest struct \{', line):
            inside = True
            continue
        if inside and line.startswith("}"):
            break
        if not inside:
            continue
        m = MANIFEST_TAG.match(line)
        if m:
            out.append((m.group(1), m.group(2)))
    return out


def unenforced_manifest_fields():
    contract = read("docs/CONTRACT.md")
    # ABI 那一段：从 `### Skill ABI` 到下一个 `###`
    start = contract.find("### Skill ABI")
    end = contract.find("###", start + 1)
    abi = contract[start:end] if start >= 0 else ""
    # ★★★ 「标了」必须是**那一行带 ⬜**，不是「那个名字在这一段里出现过」。
    #
    # 第一版取的是整个 ABI 段里的反引号名字 —— 而字段清单本身就写着
    # `effects[]`，于是**去掉表格里那一行的 ⬜ 之后守卫照样绿**：
    # 判据被字段名自己满足了。实测确认过（删掉 effects 那一行的 ⬜，退出码仍是 0）。
    #
    # **一个被「名字出现过」满足的判据，检查的是存在性，不是那件事。**
    # 与 Round 43 的「认领了却没处理」是同一条：claimed-vs-unclaimed
    # 变成了 claimed-and-handled-vs-claimed-and-empty。
    marked = set()
    for line in abi.splitlines():
        if "\u2b1c" not in line:
            continue
        marked |= set(re.findall(r'`([a-z_]+)[^`]*`', line))
    problems = []
    for go_name, json_name in manifest_fields():
        reads = 0
        # ★ 定义它的那个文件也要算 —— `Manifest.Validate` 就住在
        # `store/manifest.go` 里，而 `m.Version == ""` 是一次**读**。
        # 第一版把整个文件排除掉了，于是 `Version` 被误报。
        # **这与 Round 55 的 `check_exported_functions_have_callers` 是同一个坑**：
        # 排除定义文件会把「同一个文件里的使用者」当成不存在。
        # 结构体声明那一段要抹掉，否则每个字段都会「用到」自己。
        for path in go_files():
            body = open(path, encoding="utf-8").read()
            if path.endswith("store/manifest.go"):
                body = re.sub(r'^type Manifest struct \{.*?^\}', '', body, flags=re.M | re.S)
            for m in re.finditer(r'\.' + go_name + r'\b', body):
                tail = body[m.end():m.end() + 3].lstrip()
                if tail.startswith("=") and not tail.startswith("=="):
                    continue
                reads += 1
        if reads > 0:
            continue
        if json_name in marked:
            continue
        problems.append(
            f"清单字段 {go_name}（json:{json_name}）在非测试代码里一次都没被读，"
            f"而契约的 Skill ABI 那一段没有用 `\u2b1c` 标出这件事")
    return problems


# ★★★ 一个存储**恰好有两个通道**，而契约此前一次都没提过它们。
#
# `store/refs.go` 声明了 `ChannelCanary = "canary"` 与 `ChannelStable = "stable"`，
# `store/register.go:399-402` 的 `Catalog()` 按 **stable 先、canary 后** 解析 ——
# 而这个顺序此前只写在 `Catalog()` 的**注释**里：
#
#	// The stable pointer answers first, because that is what a caller would get.
#
# **一条只活在注释里的决定，是一条契约没有做出的决定。**
#
# 判据是**可算的**：`store/refs.go` 里每一个 `Channel\w+ = "值"` 的值，
# 都必须在 `docs/CONTRACT.md` 里出现。
CHANNEL_CONST = re.compile(r'^\tChannel\w+\s*=\s*"([a-z0-9-]+)"', re.M)


def undocumented_channels():
    src = read("store/refs.go")
    contract = read("docs/CONTRACT.md")
    out = []
    for m in CHANNEL_CONST.finditer(src):
        ch = m.group(1)
        if ch not in contract:
            out.append(f"通道 {ch} 在 store/refs.go 里被声明，而 docs/CONTRACT.md 一次都没提过它"
                       f"（通道决定 resolve 给哪一个 ref，它不是一个实现细节）")
    return out


# ★★★ 一个只给了范围的词汇表，等于没有词汇表。
#
# 契约 A6 那一行写的是「E0–E4 在传递中不可丢失、不可提升」—— 一个**范围**。
# 而实测：`E1` / `E2` / `E3` 在 `docs/CONTRACT.md` 里**一次都没出现过**
# （`E0` 出现 2 次、`E4` 出现 1 次）。唯一说到的具体等级是 E0。
#
# ★ 而 E1 是那扇门：`Manifest.Verified()` 就是 `evidence.AtLeast(m.Evidence, E1)`，
# 而 `Store.Promote` 对低于它的一律拒绝（"unverified work must not be frozen"）。
# **本文档此前没有说出「哪一档才允许把工作固化」**，而那是这个词汇表里唯一
# 一个承重的刻度。
#
# 判据与通道那条同形：`evidence/evidence.go` 里每一个 `L\d Level = "E\d"` 的值，
# 都必须在 `docs/CONTRACT.md` 里出现。
LEVEL_CONST = re.compile(r'^\tL\d\s+Level\s*=\s*"(E\d)"', re.M)


def undocumented_levels():
    src = read("evidence/evidence.go")
    contract = read("docs/CONTRACT.md")
    out = []
    for m in LEVEL_CONST.finditer(src):
        lv = m.group(1)
        if lv not in contract:
            out.append(f"证据等级 {lv} 在 evidence/evidence.go 里被声明，而 docs/CONTRACT.md "
                       f"一次都没提过它（A6 只给了 E0–E4 这个**范围**，而范围不是定义；"
                       f"E1 是 Promote 那道门）")
    return out


# ★★★ 一个消费者照着契约写解析器，会认不出真正会出现的那两个值，
# 而去处理两个永远不会出现的值。
#
# 契约此前那一行写的是 `"outcome": "ok|error|timeout|refused|degraded"`，而写进
# 链上的 outcome **逐字取自 `Verdict`**（`organs/executor/verify.go:58-65` 的
# `func (v Verdict) Outcome() (string, bool)`，`VerdictUnknown` 走 default 回
# `("", false)` —— **没有 resolution 本身就是 unknown**）。实测：
#
#   `error`    不存在 —— 写进去的是 `failed`（drive.sh 里 8 次，error 0 次）
#   `degraded` 不存在 —— 那是 session.State（organs/session/state.go:27）
#   `failed`   缺
#   `unknown`  缺（而它是契约**自己那条重放规则**给出的值）
#
# 判据是**两向**的：`Outcome()` 的 case 列表里每个常量解析出的值都必须在契约
# 那一行里出现；而契约那一行里的每个值也必须在 case 列表里。
VERDICT_DECL = re.compile(r'^\t(Verdict\w+)\s+Verdict\s*=\s*"([a-z]+)"', re.M)
OUTCOME_CASE = re.compile(r'^\tcase ([^:]+):', re.M)
OUTCOME_LINE = re.compile(r'"outcome":\s*"([a-z|]+)"')


def written_outcomes():
    """`Verdict.Outcome()` 真的会写出去的那些值。"""
    src = read("organs/executor/verify.go")
    decls = dict(VERDICT_DECL.findall(src))
    i = src.find("func (v Verdict) Outcome()")
    if i < 0:
        return None
    body = src[i:i + 400]
    m = OUTCOME_CASE.search(body)
    if not m:
        return None
    out = []
    for name in m.group(1).split(","):
        name = name.strip()
        if name in decls:
            out.append(decls[name])
    return out


def outcome_vocabulary_mismatches():
    got = written_outcomes()
    if got is None:
        return ["Verdict.Outcome() 读不出来：判据的前提（那个函数与它的 case 列表）变了"]
    contract = read("docs/CONTRACT.md")
    m = OUTCOME_LINE.search(contract)
    if not m:
        return ['docs/CONTRACT.md 里找不到 `"outcome": "…"` 那一行']
    listed = [x for x in m.group(1).split("|") if x]
    out = []
    for v in got:
        if v not in listed:
            out.append(f"outcome {v} 会被写进链上，而契约那一行没有列它")
    for v in listed:
        if v not in got:
            out.append(f"契约那一行列了 outcome {v}，而 Verdict.Outcome() 从不写它 —— "
                       f"照着这一行写解析器的人会去处理一个永远不会出现的值")
    return out


# ★★★ 契约描述了一条**不存在**的记录。
#
# 此前那一段「拆成两条记录」写的是：
#
#	{"rec": "intent", "trace": ..., "attempt": ..., "session": ...,
#	 "task_class": ..., "skill_ref": "<内容哈希>", "strategy": ...,
#	 "budget": {...}, "action_hash": ...}
#	{"rec": "resolution", "of": "<intent head 哈希>",
#	 "outcome": "ok|failed|timeout|refused",
#	 "verified_by": "verifier", "verify_assert": ..., "gain": ..., "actual": {...}}
#
# 而实测（skill.invoke 一个 exit 0 的技能，然后读盘）：
#
#	key=a1:intent
#	  {"attempt": "a1", "rec": "execution", "skill": "e.one", "strategy": "s1", "task_class": "task_exec"}
#	key=a1:resolution
#	  {"attempt": "a1", "latency": 2, "ok": true, "rec": "execution", "rules": "<哈希>",
#	   "skill": "e.one", "strategy": "s1", "task_class": "task_exec", "verified_by": "verify_assert"}
#
# **除了 attempt / task_class / strategy / verified_by，每一个字段名要么是错的、
# 要么根本不存在。** 而 `intent` / `resolution` 是 **key 的后缀**，不是 `rec` 的值；
# `outcome` 是一个**渲染出来的**值，链上存的是 `ok`（一个布尔）。
#
# 判据：`observeBody` 的每一个 json 标签，加上写入时补上的那几个
# （`rec` / `ok` / `latency`），都必须在契约里出现。
OBSERVE_TAG = re.compile(r'\t\w+\s+[\w\.\[\]\*]+\s+`json:"([a-z_]+)')
# ★ 写入时由 s.observe 补上的、不在 observeBody 里的键。
OBSERVE_ADDED = ["rec", "ok", "latency"]
# ★ 契约此前描述过、而实际不存在的字段。写下来，免得它们再长回去。
PHANTOM_FIELDS = ["trace", "session", "budget", "action_hash", "skill_ref",
                  "gain", "actual", "verify_assert", "of"]


def chain_record_mismatches():
    # ★ 写出去的那个结构体是 `executionRecord`，**不是** `observeBody`：
    # `observeBody` 是 `arbiter.observe` 的**请求**形状，而
    # `arbiterObserve` 把它转成 `executionRecord` 再落盘。第一版读错了那个结构体，
    # 于是报出「链上真的会写字段 cost」—— 而 `cost` 在 `observeBody` 里确实有、
    # 在 `executionRecord` 里也有（带 `omitempty`，所以我的转储里没出现）。
    src = read("adapters/shell/decision.go")
    i = src.find("type executionRecord struct")
    if i < 0:
        return ["executionRecord 找不到了：判据的前提变了"]
    j = src.find("\n}", i)
    fields = OBSERVE_TAG.findall(src[i:j])
    contract = read("docs/CONTRACT.md")
    # ★ 只看「拆成两条记录」那一段里的 **jsonc 代码块** —— 我的对照表
    # 也写着 trace / session / … 这些**旧名字**，整段去查会把那张表当成主张。
    # **一个把「被否定的旧名字」当成「主张」的判据，会把纠正它的人判成错的。**
    a = contract.find("### 拆成两条记录")
    b = contract.find("###", a + 1)
    sect = contract[a:b] if a >= 0 else contract
    m = re.search(r'```jsonc(.*?)```', sect, re.S)
    block = m.group(1) if m else ""
    # ★★ 而「键」与「值」必须分开认：第一版查的是 `"verify_assert"`，
    # 而块里那一行是 `"verified_by": "verify_assert"` —— **`verify_assert` 是那个
    # 键的「值」**，于是判据把一个正确的键报成了幽灵字段。
    # **一个分不清键与值的判据，会把一个值判成一个字段名。**
    def has_key(name):
        return re.search(r'"' + name + r'"\s*:', block) is not None

    out = []
    for f in fields:
        if not has_key(f):
            out.append(f"链上真的会写字段 {f}，而「拆成两条记录」的 jsonc 块里没有它")
    for f in PHANTOM_FIELDS:
        if has_key(f) and f not in fields:
            out.append(f"「拆成两条记录」的 jsonc 块里列了 {f}，而链上从不写它")
    return out


# ★★★ 契约按例子描述记录，却从来没有列过 `rec` 的词汇表 —— 而每一条链上记录
# 都带着它。实测：`adapters/` 里写出去的 `rec` 恰好七个，而契约里以引号形式
# 出现过的只有 `"execution"` 与 `"session"` —— 另外五个一次都没出现过。
#
# 一个读链的人看见 `{"rec": "reflex"}`，**没有任何依据说它是合法的还是写错了的。**
#
# 判据是**两向**的：`Rec: "..."` 的每个字面量都必须在契约那一节里以
# **表格行首**的形式出现；而那一节表格里列的每个值也必须是代码真的会写的。
REC_LITERAL = re.compile(r'Rec:\s*"([a-z_]+)"')
REC_ROW = re.compile(r'^\| `([a-z_]+)` \|', re.M)
# ★ 而 `rec` 的字面量只出现在适配器里；内核不认识这个字段。
REC_SECTION = "### rec：链上每一条记录属于哪个子系统"


def rec_vocabulary_mismatches():
    written = set()
    for path in go_files():
        # ★ `go_files()` 返回的是**绝对路径**，而第一版写的是
        # `path.startswith("adapters/")` —— 它一次都没匹配上，于是判据报的是
        # 「一个字面量都找不到」，而那不是「链上没有 rec」，是**判据看错了地方**。
        # **一个拿绝对路径去比相对前缀的判据，永远匹配不到，而它报出来的
        # 是「没有东西可查」。**
        if not os.path.relpath(path, ROOT).startswith("adapters" + os.sep):
            continue
        body = open(path, encoding="utf-8").read()
        written |= set(REC_LITERAL.findall(body))
    if not written:
        return ["一个 Rec: \"...\" 字面量都找不到：判据的前提变了"]
    contract = read("docs/CONTRACT.md")
    a = contract.find(REC_SECTION)
    if a < 0:
        return [f"契约里找不到那一节（{REC_SECTION}）"]
    b = contract.find("###", a + 1)
    sect = contract[a:b]
    # ★★ 而表**头**那一行也是 `| \`rec\` | 写它的地方 | 是什么 |` ——
    # 第一版把它当成了一个 `rec` 的值，于是报「契约列了 rec=rec」。
    # **一条只看「第一个单元格是不是反引号名字」的判据，会把表头读成一行数据。**
    # 判据改成只看**表头分隔行之后**的那些行。
    sep = re.search(r'^\|[\s\-:|]+\|\s*$', sect, re.M)
    body = sect[sep.end():] if sep else sect
    listed = set(REC_ROW.findall(body))
    out = []
    for v in sorted(written):
        if v not in listed:
            out.append(f"链上真的会写 rec={v}，而契约那一节的表里没有它")
    for v in sorted(listed):
        if v not in written:
            out.append(f"契约那一节的表里列了 rec={v}，而适配器里没有一处写它")
    return out


# ★★★ 契约的状态图里有两个死胡同与一个错名字。
#
# 此前那四条缩写的箭头写的是：
#
#	ready → routing → {executing | planning}
#	executing → verifying → {done | retrying | escalating}
#	escalating → waiting → {executing | timeout→degraded | deny→rearbitrate}
#	rearbitrate → {planning | degraded}
#
# 而实测（organs/session/state.go:73-85 的 var transitions map[State]map[Event]State）：
#
#	rearbitrate  -> 实际叫 rearbitrating（这个名字在契约里 0 次）
#	planning     -> 只有入边，没有出边；实际 Planning: {Planned: Executing}
#	retrying     -> 只有入边，没有出边；实际 Retrying: {Rerun: Executing}
#
# **两个状态在契约的图里是死胡同，而它们在代码里都能走出去。**
# 而代码那一侧是有守卫的（TestEveryPairIsAccountedFor 枚举所有 states × events）——
# **漂移的是契约那一侧。**
#
# 判据：从 transitions 里推出每一条边（`from --event--> to`），
# 都必须在契约那段代码块里出现；而块里的每一条边也必须在表里。
STATE_DECL = re.compile(r'^\t(\w+)\s+State\s*=\s*"([a-z]+)"', re.M)
EVENT_DECL = re.compile(r'^\t(\w+)\s+Event\s*=\s*"([a-z]+)"', re.M)
TRANS_ROW = re.compile(r'^\t(\w+):\s*\{([^}]*)\},?\s*$', re.M)
TRANS_EDGE = re.compile(r'(\w+):\s*(\w+)')
DIAGRAM_EDGE = re.compile(r'^([a-z]+)\s+--([a-z]+)-->\s+([a-z]+)$', re.M)


def transition_edges():
    """(from, event, to) 三元组，全部解析成字面值。"""
    src = read("organs/session/state.go")
    states = dict(STATE_DECL.findall(src))
    events = dict(EVENT_DECL.findall(src))
    i = src.find("var transitions = map[State]map[Event]State{")
    if i < 0:
        return None
    j = src.find("\n}", i)
    out = []
    for m in TRANS_ROW.finditer(src[i:j]):
        frm = states.get(m.group(1))
        if frm is None:
            continue
        for ev, to in TRANS_EDGE.findall(m.group(2)):
            e, t = events.get(ev), states.get(to)
            if e and t:
                out.append((frm, e, t))
    return out


def state_diagram_mismatches():
    edges = transition_edges()
    if not edges:
        return ["transitions 读不出来：判据的前提（那个表或它的常量）变了"]
    contract = read("docs/CONTRACT.md")
    m = re.search(r'```\n(ready\s+--.*?)```', contract, re.S)
    if not m:
        return ["契约里找不到那张状态图（以 `ready --` 开头的那段代码块）"]
    block = m.group(1)
    drawn = set(DIAGRAM_EDGE.findall(block))
    out = []
    for e in edges:
        if e not in drawn:
            out.append(f"代码里 {e[0]} --{e[1]}--> {e[2]} 是一条真的边，而契约的状态图里没有它")
    for d in sorted(drawn):
        if d not in edges:
            out.append(f"契约的状态图里画了 {d[0]} --{d[1]}--> {d[2]}，而代码里没有这条边")
    return out


# ★★★ `kind` 是执行形态的调度键，而契约那张表少列了一个。
#
# `store/manifest.go` 声明七个 Kind（algorithm / code / script / llm_api /
# rl_policy / human / **compose**），而契约那一行此前只列六个 ——
# 而 `Manifest.Validate` 里**四条**判据都以 `kind=compose` 为条件。
# **整篇契约把 `compose` 只当作帧族名用，从没说过它是一个 kind。**
#
# 判据：`Kind\w+ Kind = "…"` 的每个值都必须在契约那一行的反引号列表里；
# 而那一行里的每个值也必须是代码真的声明的。
KIND_DECL = re.compile(r'^\tKind\w+\s+Kind\s*=\s*"([a-z_]+)"', re.M)
KIND_LINE = re.compile(r'`kind` 是\*\*执行形态的调度键\*\*：(.+?)。', re.S)
KIND_NAME = re.compile(r'`([a-z_]+)`')


def kind_vocabulary_mismatches():
    declared = set(KIND_DECL.findall(read("store/manifest.go")))
    if not declared:
        return ["一个 Kind 声明都找不到：判据的前提变了"]
    contract = read("docs/CONTRACT.md")
    m = KIND_LINE.search(contract)
    if not m:
        return ["契约里找不到 `kind` 是执行形态的调度键那一行"]
    listed = set(KIND_NAME.findall(m.group(1)))
    out = []
    for v in sorted(declared):
        if v not in listed:
            out.append(f"kind {v} 在 store/manifest.go 里被声明，而契约那一行没有列它")
    for v in sorted(listed):
        if v not in declared:
            out.append(f"契约那一行列了 kind {v}，而 store/manifest.go 没有声明它")
    return out


# ★★★ 契约写了 arbiter 的四帧，却从来没有列过仲裁器可以选的那些动作。
#
# `organs/arbiter/escalation.go:45-61` 声明**恰好六个**，而契约里以单词边界出现
# 的次数是 —— local **0** · ask_human **0** · degrade **0** · escalate **1**
# （而那一次是状态机的事件 `verifying --escalate--> escalating`，**不是动作**）。
#
# **一个读契约的人看不出仲裁器会在哪几个动作之间选。**
#
# 判据：`Action\w+ Action = "…"` 的每个值都必须在契约那一节的表里；
# 表里列的每个值也必须是代码真的声明的。
ACTION_DECL = re.compile(r'^\tAction\w+\s+Action\s*=\s*"([a-z_]+)"', re.M)
ACTION_ROW = re.compile(r'^\| `([a-z_]+)` \|', re.M)
ACTION_SECTION = "### Action ABI"


def action_vocabulary_mismatches():
    declared = set(ACTION_DECL.findall(read("organs/arbiter/escalation.go")))
    if not declared:
        return ["一个 Action 声明都找不到：判据的前提变了"]
    contract = read("docs/CONTRACT.md")
    a = contract.find(ACTION_SECTION)
    if a < 0:
        return [f"契约里找不到那一节（{ACTION_SECTION}）"]
    b = contract.find("###", a + 1)
    sect = contract[a:b]
    # ★ 表**头**那一行也是 `| \`Action\` | 含义… |` —— 只看表头分隔行之后的行。
    sep = re.search(r'^\|[\s\-:|]+\|\s*$', sect, re.M)
    body = sect[sep.end():] if sep else sect
    listed = set(ACTION_ROW.findall(body))
    out = []
    for v in sorted(declared):
        if v not in listed:
            out.append(f"动作 {v} 在 organs/arbiter/escalation.go 里被声明，而契约那一节的表里没有它")
    for v in sorted(listed):
        if v not in declared:
            out.append(f"契约那一节的表里列了动作 {v}，而代码里没有声明它")
    return out


# ★★★ 两个数漂了而没人查，与「一个只被写、从不被读的字段」是同一条 ——
# 只不过这里的字段是文档里的一个数。
#
# docs/KERNEL.md:65 说「`Refusal` + **13** 个拒绝码常量」，而 core/refusal.go 里是
# **14** 个；:69 说「**456** 行（Gate 1 上限 3000）」，而 core/ 的非测试 .go 是
# **661** 行。`check_contract.py` 数拒绝码时**只看 docs/CONTRACT.md**。
#
# ★ 而 456 那一个数是 **Gate 1** 的声明 —— 内核有多小，是这一条在说的。
#
# ★★ 判据只认**主张**的写法，不认引号里的旧值：这一节里就引着 `13 个拒绝码常量`
# 与 `456 行` 这两个旧数（它们是那段说明的一部分）。
# **一个把「被否定的旧值」当成主张的判据，会把纠正它的人判成错的**（Round 76 的坑）。
CLAIM_CODES = re.compile(r'\| Refusal \| `Refusal` \+ (\d+) 个拒绝码常量')
CLAIM_LINES = re.compile(r'，(\d+) 行（Gate 1')


def _code_count():
    src = read("core/refusal.go")
    i = src.find("const (")
    j = src.find("\n)", i)
    return len(re.findall(r'^\tCode\w+\s*=\s*"', src[i:j], re.M))


def _core_lines():
    n = 0
    for dp, dn, fs in os.walk(os.path.join(ROOT, "core")):
        for f in fs:
            if f.endswith(".go") and not f.endswith("_test.go"):
                n += sum(1 for _ in open(os.path.join(dp, f), encoding="utf-8"))
    return n


def doc_number_mismatches():
    out = []
    want_codes = _code_count()
    want_lines = _core_lines()
    if want_codes == 0 or want_lines == 0:
        return ["读不出 core 的拒绝码数或行数：判据的前提变了"]
    for rel in ["docs/KERNEL.md"]:
        body = read(rel)
        for m in CLAIM_CODES.finditer(body):
            if int(m.group(1)) != want_codes:
                out.append(f"{rel} 说 {m.group(1)} 个拒绝码常量，而 core/refusal.go 里是 {want_codes} 个")
        for m in CLAIM_LINES.finditer(body):
            if int(m.group(1)) != want_lines:
                out.append(f"{rel} 说 {m.group(1)} 行（Gate 1），而 core/ 的非测试 .go 是 {want_lines} 行")
    return out


def main():
    src = read("core/refusal.go")

    # The const block gives name -> code; the retryable map keys by name,
    # so it has to be joined through that mapping rather than read directly.
    names = dict(CONST_RE.findall(src))
    codes = set(names.values())

    retry = {}
    for name, val in RETRY_RE.findall(src):
        if name in names:
            retry[names[name]] = (val == "true")

    contract = read("docs/CONTRACT.md")
    rows = ROW_RE.findall(contract)
    doc_codes = {c for c, _, _ in rows}
    doc_retry = {c: (mark == "✅") for c, _, mark in rows}

    # 每一行原文，用来判断它有没有被标成已声明的缺口。
    row_line = {}
    for line in contract.splitlines():
        m = ROW_RE.match(line)
        if m:
            row_line[m.group(1)] = line

    bad = 0

    for c in sorted(codes - doc_codes):
        print(f"  文档缺拒绝码：{c}")
        bad += 1
    for c in sorted(doc_codes - codes):
        print(f"  文档多拒绝码：{c}（core/refusal.go 里没有）")
        bad += 1

    for c in sorted(codes & doc_codes):
        if c not in retry:
            print(f"  core 的 retryable 表里没有 {c}")
            bad += 1
            continue
        if doc_retry[c] != retry[c]:
            print(f"  可重试列不一致：{c} 文档={doc_retry[c]} core={retry[c]}")
            bad += 1

    # ★★★ 每个码要么有生产点，要么在契约里被标成已声明的缺口。
    name_of = {v: k for k, v in names.items()}
    counts = producers(name_of.values())
    orphan = []
    for c in sorted(codes):
        name = name_of[c]
        if counts.get(name, 0) > 0:
            continue
        if any(mark in row_line.get(c, "") for mark in PENDING_MARK):
            continue
        orphan.append(c)
    if orphan:
        print("  这些拒绝码没有任何地方会返回（要么加生产点，要么在契约里标 ⬜ 未实现）：")
        for c in orphan:
            print(f"    {c}")
        bad += len(orphan)

    # ★★★ 文档里的一个数也是一个主张。
    for u in doc_number_mismatches():
        print(u)
        bad += 1

    # ★★★ 仲裁器的动作表必须与真的 Action 声明对得上。
    for u in action_vocabulary_mismatches():
        print(u)
        bad += 1

    # ★★★ kind 那张表必须与真的 Kind 声明对得上。
    for u in kind_vocabulary_mismatches():
        print(u)
        bad += 1

    # ★★★ 契约的状态图必须与那张真的转换表对得上。
    for u in state_diagram_mismatches():
        print(u)
        bad += 1

    # ★★★ 链上每一条记录都带着 rec，而契约从来没有列过它的词汇表。
    for u in rec_vocabulary_mismatches():
        print(u)
        bad += 1

    # ★★★ 链上真的写的字段，与契约描述的那两条记录，必须对得上。
    for u in chain_record_mismatches():
        print(u)
        bad += 1

    # ★★★ outcome 的词汇表必须是**两向**对齐的。
    for u in outcome_vocabulary_mismatches():
        print(u)
        bad += 1

    # ★★★ 契约没提过的等级，等于契约没做出的决定。
    for u in undocumented_levels():
        print(u)
        bad += 1

    # ★★★ 契约没提过的通道，等于契约没做出的决定。
    for u in undocumented_channels():
        print(u)
        bad += 1

    # ★★★ 契约点名过的每一个清单字段，要么有人执行，要么标明没人执行。
    unenforced = unenforced_manifest_fields()
    if unenforced:
        print("这些清单字段被契约点名、而没有任何地方读它们（要么接上，要么在 ABI 段里标 ⬜）：")
        for u in unenforced:
            print(f"  {u}")
        bad += len(unenforced)

    print(f"  拒绝码 {len(codes)} 个，文档列出 {len(doc_codes)} 个。")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
