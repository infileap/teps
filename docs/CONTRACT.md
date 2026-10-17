# 契约

内核与外设之间、以及宿主与内核之间的**唯一**接口。

**契约一旦定下，判据不许定在盘上。** 见 A8。

---

## 一、公理

| | 公理 | 含义 |
|---|---|---|
| **A1** | 服务端拥有状态权威 | 客户端**永不**提供 `seq`/`rev`/`head`/`balance`/`committed` |
| **A2** | 一切变更幂等 | 客户端生成幂等键；重发不产生第二次效果 |
| **A3** | 拒绝是值不是异常 | 拒绝是结构化返回值 `{decision, reason}` |
| **A4** | 内核不含宿主词汇 | 不出现 dsh / cordis / harness / plugin |
| **A5** | 不可变优先 | 共享产物内容寻址 |
| **A6** | 证据等级随行 | E0–E4 在传递中**不可丢失、不可提升** |
| **A7** | 预算显式 | 每一步的预算在动作之前声明 |
| **A8** | **失败语义也是契约** | 「哈希不匹配」必须是 `recovery_required`，**从不是** `ok` |

**A8 是后来补的，补的理由值得记下**：格式契约只能约束"写进去的形状"，
约束不了"失败时怎么写"。一条 append-only 日志如果允许把失败记成成功，
**它会永久保留一个假事实，而且改不掉。**

### A1 的推论：`client_authority`

客户端若真的提供了 `seq`/`rev`，**内核必须拒绝**，返回 `client_authority`。
这不是宽容度问题——**一旦接受客户端序号，服务端就不再是权威了。**

### A3 的推论：幂等键标识"一次逻辑尝试"

**键标识一次逻辑尝试，不标识一次网络请求。**

- 传输层重试 ⇒ **复用**同一个键；
- 超时之后重发 ⇒ 那是新的 outcome ⇒ **新的 attempt** ⇒ **新键**。

（把键当成网络请求 ID，会在超时重试时把"可能已经执行了"的第二次执行
误判为重复而**静默跳过**——这是最危险的失败模式：静默的错。）

---

## 二、帧

- **NDJSON**：一行一帧，UTF-8，单帧上限 1 MiB（超出 ⇒ `frame_too_large`）；
- 传输可换：`stdio` / `HTTP POST /k`；
- 公共信封：`{t, v, id, key, seq, rev, err}`。

**传输不是盘上格式。** 所以 HTTP 适配器是一个进程，不是内核的一部分。

---

## 三、消息集

★ **这一节现在有一台守卫**（`tools/check_frames.py`）：适配器分派的每一个帧类型，
这里都必须有；这里写了的，必须有人分派。**它第一次运行就抓到 12 处不一致**——
8 个前缀族文档里根本没提，4 个帧文档在承诺而没人实现。

它防的是本项目第四次付过的那笔学费：**帧类型是全局命名空间**。我新加的
`compose.build` 与 `organs/composer` 抢了同一个帧名，于是 composer 段七条断言全红。
撞名的特别之处在于**两边都能跑**，只有把两边放在一起看才知道撞了。

### 已实现的帧（按前缀族）

| 前缀族 | 帧 | 要点 |
|---|---|---|
| （无前缀） | `hello` / `welcome` | `welcome` 带 `epoch`；服务端重启后客户端靠它察觉 ⇒ `stale_epoch` |
| （无前缀） | `put` `spend` `status` `drain` `bye` | 参考传输的最小面 |
| `session.` | `session.open` `session.event` `session.observe` `session.retry` `session.status` | 同会话**严格串行**；非法迁移在 append 之前就拒 |
| `skill.` | `skill.publish` `skill.resolve` `skill.list` `skill.promote` `skill.invoke` `skill.compose` | `invoke` **钉死** `ref` 的内容哈希 | **`verify_refute` 是提级的必要条件**：只有必过用例的声明停在 E0。
| `compose.` | `compose.propose` | **只读**：按类型搜出一条链，不运行任何东西 |
| `memory.` | `memory.write` `memory.supersede` `memory.retrieve` | 写入后不可变，纠正只能 supersede |
| `escalation.` | `escalation.quote` `escalation.decide` | ★ **问价不消耗**：`quote` 不写链、不扣预算；`decide` 占 grant，且必须带上它依据的 `quote_id`——价格一动，那条报价就作废 |
| `arbiter.` | `arbiter.observe` `arbiter.rank` `arbiter.state` `arbiter.fix` | 后验是**从链上折出来的投影**，不另存一份 |
| `reflex.` | `reflex.observe` `reflex.match` `reflex.state` | 唯一一层零上下文的回答 |
| `task.` | `task.intake` `task.stats` | 任务入口——**率的分母** |
| `distill.` | `distill.apply` | 一次成功的完整路径 → 一个可固化的提案 |
| `audit.` | `audit.tail` | ★ **唯一一个不向任何器官提问的帧**：其余每个 op 回的都是某个器官的**读法**（会话、记忆、后验），这个回的是那些读法**读的东西本身** |
| `metrics.` | `metrics.get` | ★ **唯一一个报告「这个数有没有定义」的帧**：能力是乘积，一个因子未定义则乘积未定义；同一个 `0` 可以表示「从没收过任务」也可以表示「一个都没答上」，那是两个事实
| `ops` | `ops` | ★ **唯一一个回答「你能收哪些帧」的帧**，而它回的就是分派用的那张表（`adapters/shell/ops.go` 的 `claims()`）——**分派和回答是同一张表，所以不可能漂移**。它报的是**族**不是每个 op：族里面的 op 是族自己的事 |
| `pool.` | `pool.register` `pool.lease` `pool.release` `pool.quote` `pool.state` | 算力登记表与租约。**容量是租约不是计数**（拒绝点名持有者）· **租约必须过期**（否则持有者一死容量就永久漏掉）· **免费是一个价格不是没有价格**（一千台空闲机器的前提要写成零价格加备注，不是缺字段）· 租约记录存**绝对过期时间**，所以重放同一条链得到同一个池 **`skill.invoke` 的 `resource` / `pick` 两字段让池决定一次执行跑在哪**：占不到槽，执行就不发生。 **`arbiter.rank` 的 offer 也可以点名一个资源**：钱由池算，而不是调用方填——一个数不能有两个来源。
| `breaker.` | `breaker.state` | ★ **脚本宿主监督的另一半**：超时早就在了（进程不按时回来就杀掉），熔断此前没有——**一个连续失败的宿主一直被 spawn**，每次烧掉声明的预算、拿到同一个答案。**只有宿主失败算数**（没起来 / 没回来）；技能自己判 `failed` 不算，那是技能在如实报告它做不到 |

### 契约承诺但**尚未实现**的帧

**空表。** 这张表曾经有五行，现在是零行：`escalation.quote` `escalation.decide` `audit.tail` `metrics.get` 都实现了，而 `metrics.get` 原来那条「暴露 `beta: [α, β]`」是同一扇门的第二个门牌，所以它拿到了一个真正新的内容。

表空着比删掉它好：**它是 `tools/check_frames.py` 反方向那一半的落点**——契约写了的必须有人分派。删掉这张表，下一个「文档在承诺一个不存在的入口」就没有地方可标。

**`quote` 与 `decide` 分开是一个刻意的设计**：
先问价不花钱，才能在不消耗配额的前提下做比较。合成它们会导致
"想过一次就等于花过一次"，从而让系统不敢比较。

---

## 四、拒绝码

拒绝是**值**。每个码带「可重试」列，适配器作者据此写循环。
这一列的含义只有一个，不许被"大概能恢复"污染：
**客户端能不能原样重发同一个请求？**

| 码 | 含义 | 能原样重发吗 |
|---|---|---|
| `client_authority` | 客户端提供了服务端才有的字段 | ❌ 改了再发 |
| `no_session` | 会话不存在 | ❌ |
| `stale_epoch` | 服务端已重启，客户端的世界观过期了 | ❌ **必须先重新 hello** · ⬜ **未实现：这个码目前没有任何生产点** |
| `budget_exhausted` | 本次预算用完 | ❌ 本动作 |
| `backlog_full` | 背压 | ✅ 退避后重试 · ⬜ **未实现：这个码目前没有任何生产点** |
| `tainted_input` | 输入带不可信标，不许直接产生执行动作 | ❌ · ⬜ **未实现：这个码目前没有任何生产点** |
| `quota_exhausted` | 配额用完 | ❌ 本周期 · ⬜ **未实现：这个码目前没有任何生产点** |
| `license_conflict` | 许可证闭包不兼容 | ❌ |
| `lease_held` | 该资源已被别的执行支持有 | ✅ 排队后重试 |
| `circuit_open` | ★ **宿主熔断**：这个解释器连续 N 次没有回来，冷却期内不再 spawn | ✅ 冷却后重试（理由里写明还要等多少毫秒） |
| `shadow_nested` | 执行型分支不许再嵌套 | ❌ · ⬜ **未实现：这个码目前没有任何生产点** |
| `frame_too_large` | 单帧超 1 MiB | ❌ |
| `bad_request` | 帧能解析，但请求本身不合法（未知类型、非法迁移、策略超限） | ❌ 改了再发 |
| `recovery_required` | 盘上状态与哈希链不符，拒绝继续 | ❌ 需人工裁决 |

★★ **一个没有生产点的拒绝码是一个承诺。**

这张表里曾有 **5 个码没有任何地方会返回**（`stale_epoch` `backlog_full`
`tainted_input` `quota_exhausted` `shadow_nested`）。而这张表的用法是
「适配器作者据此写循环」—— 于是 `backlog_full` 那一个 `✅ 退避后重试`
在承诺一次**永远不会到来**的重试，而 `stale_epoch` 那一个
`❌ 必须先重新 hello` 在承诺一次**永远不会发生**的重启察觉。

这与帧表里「写进契约但没人分派 = 文档在承诺一个不存在的入口」是同一条，
所以处置也同一条：**明确标出来的缺口不是静默漂移**。上面 5 行因此都带上了
那个缺口标记（`tools/check_contract.py` 会检查每一个码要么有生产点、
要么带着它）。

★ **`stale_epoch` 与 `recovery_required` 曾经在表里是 ✅，而它们是 ❌。**
两者都"看起来可恢复"——但恢复它们的方式**不是重发这个请求**。
**一个重试标志绝不许承诺一次永远不会成功的重试**：链不匹配，下次开还是不匹配。
这就是把这一列的定义钉死成"能不能原样重发"的原因。

**这张表与 `core/refusal.go` 的 14 个常量必须逐字一致**，且「能原样重发吗」必须
等于 `retryable` 表里的布尔值。`tools/check_contract.py` 就是为此存在的——
它是在一次真实漂移之后写的：`drive.sh` 断言 `unknown_session`，内核返回 `no_session`，
文档也写着 `unknown_session`，而**两边都没有报错**。

**能力协商不静默降级**：客户端声明需要的某个能力而服务端没有 ⇒ **显式拒绝**。
静默降级会制造"以为在做 A、其实在做 B"的执行史——而那正是最贵的一类错。

---

## 五、四个 ABI

### Skill ABI

```
skill_id  version  kind  input_schema  output_schema
preconditions  postconditions
verify.assert          ← 核验器据此独立判定 outcome
on_failure
budget{ms, tokens, money}
effects[]  evidence  idempotent  license
deps[hash]             ← 内容哈希，不是版本号
provenance{source, url, commit, captured_at}
```

★★★ **上面这一行里有四个字段是「点名过、没人执行」的**，它们在这里留名，但
**契约必须说清这一点** —— 一个接入方读着 ABI 看见 `preconditions`，
无从知道它是什么形状、更不知道它会不会被检查：

| 字段 | 状态 |
|---|---|
| `idempotent` | ✅ **被检查**（`Manifest.Validate` 拒 `false`：A2 靠 `attempt` 键做到幂等，声明非幂等是在声称一件内核不会兑现的事） |
| `effects[]` | ⬜ **只被报出来，没有人执行它** —— 而「这个技能会碰什么」这件事是 `Env.Network` / `Env.Reaches` 在做，**同一个事实的两个入口，只有后一个被检查** |
| `preconditions` / `postconditions` | ⬜ **只被报出来**，而且**没有定义过形状**：这两个名字在整个仓库里只出现在清单结构体的那一行声明里，连文档注释都没有 |
| `on_failure` | ⬜ **只被报出来** |

★ 处置与本文档里「契约承诺但**尚未实现**的帧」那一张表一致：
**明确标出来的缺口不是静默漂移。** 一个字段被点名却没人执行，只有写下来才算
「已声明的缺口」，否则它读起来像一条承诺。

`kind` 是**执行形态的调度键**：`algorithm` | `code` | `script` | `llm_api` | `rl_policy` | `human`。
**"热"是状态不是类型**——一个 `script` 会因为调用频次上升到 `code`。

组合技能另有三条（见 [../store/README.md](../store/README.md)）：
数据流接线、`evidence = min(子技能)`、可靠性是乘积 `∏P_i`。

### Evidence ABI

★★★ **这一段此前不存在，而 A6 却把它当成定义过的。** 上面 A6 那一行写的是
「E0–E4 在传递中**不可丢失、不可提升**」—— 一个**范围**。而实测：`E1` / `E2` /
`E3` 这三个名字在本文档里**一次都没出现过**（`E0` 出现 2 次、`E4` 出现 1 次）。
唯一说到的具体等级是 E0（「只有必过用例的声明停在 E0」）。

**一个只给了范围的词汇表，等于没有词汇表。**

五档（`evidence/evidence.go` 的 `L0`–`L4`，值就是它们的名字）：

| 等级 | 含义 |
|---|---|
| `E0` | model-generated, unverified — **never enters core memory** |
| `E1` | **ran once in a sandbox and succeeded** |
| `E2` | confirmed by a second, independent source |
| `E3` | external authority, or a human |
| `E4` | succeeded N times with no failure |

★★ **而 E1 是那扇门**：`Manifest.Verified()` 就是 `evidence.AtLeast(m.Evidence, E1)`
（`store/manifest.go:534`），而 `Store.Promote` 对低于它的一律拒绝，理由是
「unverified work must not be frozen」。也就是说 —— **本文档此前没有说出「哪一档
才允许把工作固化」**，而那是这个词汇表里唯一一个承重的刻度。

★ 两件事必须一起读：
- **未知等级不是 E0。** `evidence.Rank` 的注释逐字写着那件事不许发生：
  「An unknown level is deliberately **not** mapped to zero. Zero is a real level
  (E0), and folding "E9" onto it would turn a typo into a trust decision.」
- **E1 要两半。** 只有必过用例的声明停在 E0（上面 `skill.` 那一行）；
  一条断言既能通过、又能被证伪，才叫跑过一次且成功。

### Channel ABI

★★★ **这一段此前不存在。** `docs/CONTRACT.md` 在 Round 73 之前**一次都没有提到
`channel` / `canary` / `stable`** —— 而通道是决定「`resolve` 会给你哪一个 ref」的
那个东西：

```
canary   新技能落地的地方。skill.publish 总是落在这里（register.go:301）。
stable   固化过的地方。Promote 是一支指针移动 —— 而回滚也是，
         那正是这个存储被分成两半的全部理由。
```

**解析顺序：`stable` 先答，`canary` 后答**（`store/register.go:399-402` 的
`Catalog()`）。这个顺序是**承重的**：一个把技能提升到 `stable` 的发布者，
改变的是**每一个消费者**解析到的 ref。

★ 而这个顺序此前只写在 `Catalog()` 的**注释**里：

	// The stable pointer answers first, because that is what a caller would get.
	// A canary-only skill is still listed: hiding it would make the catalog
	// disagree with what skill.list shows.

**一条只活在注释里的决定，是一条契约没有做出的决定。**

★ 通道是**恰好两个**，不是「任何合法的名字」：`ValidChannel`（`store/refs.go`）
只认上面那两个常量，写进第三个通道的指针**没有任何读能得到它**（Round 71）。
理由说的是「未知通道」，而不是「没有这个文件」。

### Session ABI

```
enqueue(msg) → seq
snapshot(max_bytes) → capsule        只读
fork(kind, resource?) → shadow|lease
merge(shadow, decision) → rev        decision 无默认值
terminate(reason)
```

**不变量**：同会话严格串行；`execution` fork 必须带 `resource` + lease；
`merge` 必须显式，`defer` = 标为过期而**不合并**。

### Memory ABI

```
write / retrieve / supersede
```

- `retrieve` 是**按 score/长度贪心背包填预算**，不是 top-k；
- `min_evidence` 是**硬过滤**，不是打分项；
- `valid_from` / `superseded_by` **双时间轴**（否则系统越用越固执，且自己察觉不到）；
- 写入后不可变，纠正只能 `supersede`。

### Escalation ABI

```
quote   → 估价，不消耗
decide  → 占 grant
```

`U(a) = P(success|a) × value − cost − latency`；
`P(success) ~ Beta(α + 成功, β + 失败)`，按 **(skill, 任务类)** 分组。

⚠️ `U` 是**纯 opex**，单独使用会导致"永远临时应付"。
完整的决策见 [ARBITER.md](ARBITER.md)——那是**必须与 `U` 配对**的 capex 项。

---

## 六、执行记录（ExecutionRecord）

**这是本契约里最不显然、也最要紧的一部分。**

设计早期想用**顺序**解决一个问题：先追加 `outcome:"ok"`、再改状态。
但追加与改状态之间**不存在原子性**——若 apply 失败，
**链上会永久留一条"成功了"而状态没动，且 append-only 改不掉它。**

**⇒ 不能靠顺序解，只能靠分裂。**

### 拆成两条记录

```jsonc
// apply 之前写。key = "<attempt>:intent"
{"rec": "execution", "attempt": "a1", "skill": "e.one",
 "strategy": "s1", "task_class": "task_exec"}

// apply 之后写。key = "<attempt>:resolution"
{"rec": "execution", "attempt": "a1", "skill": "e.one",
 "strategy": "s1", "task_class": "task_exec",
 "ok": true, "verified_by": "verify_assert",
 "rules": "<那组断言的身份，一个内容哈希>", "latency": 2, "cost": 0}
```

★★★ **这一段此前描述的记录不存在。** 实测（`skill.invoke` 一个 `exit 0` 的技能，然后读盘）：

	key=a1:intent
	  {"attempt": "a1", "rec": "execution", "skill": "e.one", "strategy": "s1", "task_class": "task_exec"}
	key=a1:resolution
	  {"attempt": "a1", "latency": 2, "ok": true, "rec": "execution",
	   "rules": "a6c0e2387ff61ace52dab4a732ff56c0a8a23ef97dc2c3f90f6aa919ce3176e4",
	   "skill": "e.one", "strategy": "s1", "task_class": "task_exec", "verified_by": "verify_assert"}

逐字对照：

| 契约此前写的 | 实际 |
|---|---|
| `"rec": "intent"` / `"rec": "resolution"` | `"rec": "execution"` —— 而 `intent` / `resolution` 是 **`key` 的后缀**（`a1:intent`），不是一个 `rec` 值 |
| `"outcome": "ok\|failed\|timeout\|refused"` | **`"ok": true`** —— 一个**布尔** |
| `"trace"` · `"session"` · `"budget"` · `"action_hash"` | **都不存在** |
| `"skill_ref"` | `"skill"` |
| `"of": "<intent head 哈希>"` | **不存在** |
| `"verify_assert"` | `"rules"`（一个内容哈希，不是断言原文） |
| `"gain"` · `"actual"` | **都不存在** |
| （缺 `latency`） | 实际有 |

**这一段里除了 `attempt` / `task_class` / `strategy` / `verified_by`，每一个字段名要么是错的、要么根本不存在。**

★★ 而 `outcome` 是一个**被渲染出来的值**，不是被存下来的值：
`adapters/shell/execute.go:405-420` 的重放路径读的是 `rec.OK`（`*bool`），
`*rec.OK` 为真就渲染成 `"ok"`、否则渲染成 `"failed"`。**链上存的是那个布尔。**

★ 所以那条 `"outcome"` 的判据管的是**渲染出来的**词汇表（`Verdict.Outcome()`），
与**链上**的字段名是两件事 —— 两者都写下来，才不会把「渲染出来的」当成「存下来的」。

**渲染出来的 `outcome`（响应与 `arbiter.observe` 里用的那一个）：**

```jsonc
{"outcome": "ok|failed|timeout|refused"}
```

★ 而 `unknown` **不在这一行里**：它不是被写出去的值，而是**没有 resolution**
这件事本身（见上面那条重放规则）。

**重放规则：**

> **`intent` 之后没有同名 `resolution` ⇒ 读作 `unknown`，
> 必须显式裁决，不许默认成 `ok`。**

★★★ **上面那一行此前写的是 `"ok|error|timeout|refused|degraded"`，而它三个值都不对。**

实测：写进链上的 `outcome` **逐字取自 `Verdict`** —— `organs/executor/verify.go:58-65`：

	func (v Verdict) Outcome() (string, bool) {
		switch v {
		case VerdictOK, VerdictFailed, VerdictTimeout, VerdictRefused:
			return string(v), true
		default:
			return "", false        // ★ VerdictUnknown 走这里：unknown 不写
		}
	}

于是：

| 契约此前写的 | 实际 |
|---|---|
| `error` | **不存在** —— 写进去的是 `failed`（`drive.sh` 里出现 8 次，`error` 0 次） |
| `degraded` | **不存在** —— 那是 `session.State`（`organs/session/state.go:27`），不是一个 `Verdict`（`drive.sh` 里 0 次） |
| （缺 `failed`） | 4 个被写出去的值之一 |
| （缺 `unknown`） | **`unknown` 是被写出去的吗？不是** —— `VerdictUnknown` 让 `Outcome()` 回 `("", false)`，**没有 resolution 本身就是 unknown**。所以它属于**重放规则**，不属于这一行 |

**一个消费者照着这一行写解析器，会认不出真正会出现的那两个值，
而去处理两个永远不会出现的值。**

⇒ 这一行现在只列**真的会被写出去的那四个**；`unknown` 由上面那条重放规则给出，
因为它不是一个被写入的值，而是一个**缺失**。

`unknown` 是**诚实的第三态**。把它并入 `ok` 会制造假成功；
并入 `error` 会惩罚一次可能真的成功的执行。**两者都是撒谎。**

★ **`unknown` 是读出来的，不是写上去的。** 它曾经被列在上面那个 outcome 枚举里，
那是个错误——如果它是可以写的一个值，它就成了一条**终局**记录：
「这次尝试已了结，结论是我们不知道」。而留一条**开着的 intent** 说的是另一件事：
「这次尝试未了结」，它**仍然可被处置**——人可以后来裁决它，第二个核验器可以重判它，
它可以被重跑。

> **写下的 `unknown` 关上一扇门；开着的 intent 留着一扇门。**
> 同一个词，一个是终点，一个是待办。

因此 `adapters/shell/decision.go` 的 switch **拒收** `outcome:"unknown"`
（回 `bad_request`，理由逐字说明它是派生量），并且**从不产出**它。
「判不了」的落地方式是**不写 resolution**。

### 两个不可信字段要改名

| 旧名 | 新名 | 理由 |
|---|---|---|
| `actor` | `claimed_by` | 客户端自报，**不可信** |
| `role` | `claimed_role` | 同上 |
| — | `authorized_by` | **内核签发**，内核可校验 |

**只能当"谁声称的"，不能当"谁被授权的"。** 两者混为一谈是权限漏洞的经典来源。

### 判据不许定在盘上

盘上格式能**装**这些字段，但**一个都不能生产或检查它们**——
盘上格式不认识"技能""策略""增益""升级"。

> **契约能定在盘上，判据不能定在盘上。**

否则会得到一条**漂亮、完整、而且可能全是错的**执行史。
⇒ **必须有自带的核验器**，而且它在 `organs/` 里，不在内核里。

---

## 七、会话状态机与停滞守卫

```
ready → routing → {executing | planning}
executing → verifying → {done | retrying | escalating}
escalating → waiting → {executing | timeout→degraded | deny→rearbitrate}
rearbitrate → {planning | degraded}
```

守卫**纯计数**，不用模型判断：

| 守卫 | 定义 |
|---|---|
| `gain` | ∈ [0, 1]，每步信息增益 |
| `STALL` | 连续 3 步 `gain < 0.05` |
| `LOOP` | 滑窗 8 步动作哈希重复率 > 0.5 |
| `WASTE` | 升级之后仍然 degraded 的比例 |

**★ `retrying --same_strategy` 只允许一次。** 第二次必须换策略。

**这条把"防钻牛角尖"变成了状态机里一条不可绕过的边**——
而不是一句希望模型自觉的提示词。

---

## 八、持久化

> **事件流是真相，胶囊是缓存。**

- 恢复 = 重放日志；
- 共享产物 = 内容寻址；
- 重启 ⇒ `epoch` 自增；
- 提交分步且有**锚**（`state_hash` 与链对齐），`drain` 是持久化屏障。

**这条让"升级 = 换二进制 + 重放日志"成立**，也因而让内核可以是微小的。

---

## 九、借来的形状（借契约，不借代码库）

以下形状来自 DSH 与 Inimerse 的两个权威层，**它们的协议形状值得吸收**：

服务端分配序号 · 幂等键必填且空串被拒 · 拒绝是结构化返回值 ·
能力协商不静默降级 · 追加式哈希链 + 提交锚 · **失败语义也是契约** ·
会话逻辑与传输分离（因此加 HTTP 是加法，不是重写）。

**但不引入它们的进程、类型或生命周期。**

理由：把别人的抽象变成自己的地基，就是复制"依赖 DSH"那一类错误——
**而这一次，那个别人自己都在去 C 化、换类型系统。**
一个正在被改造的底座，不能当长期的底座。

---

## 十、当前状态

**本契约不维护状态表。** 状态只有一处：[../README.md](../README.md)。

这不是省事。这里原本有一张「实现 ⬜ 未写一行 / 自由判据脚本 ⬜ 未写」的表，
而它在写完十三个包、`drive.sh` 243 条断言之后**一个字都没变**——★ 这里原本还写着一个测试总数（249）。那是**当时**的全仓库数，而一个历史数没有任何东西能核它：它既不是现在的事实，也不是一条可以复跑的判据。**写不成判据的数，就不该写成数。** ——
因为代码不会让文档变红。

> **同一个事实写两遍，就会有一个地方没被改到。**
> 这个项目已经为它付过七次学费；把状态表复制进契约，就是第八次。

本契约负责的是**不随时间变的东西**：公理、帧格式、拒绝码、四个 ABI、执行记录的形状。
凡是有可能过时的，都不属于这里。
