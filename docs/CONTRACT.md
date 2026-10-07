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

| 消息 | 要点 |
|---|---|
| `hello` / `welcome` | `welcome` 带 `epoch`；服务端重启后客户端靠它察觉 ⇒ `stale_epoch` |
| `session.open/send/stream/done` | 同会话**严格串行** |
| `session.capsule` | 只读快照，带 `max_bytes` |
| `session.fork` | 带 `kind`：`reasoning` 无租约；`execution` **必须**带 `resource` + lease |
| `session.merge` | `decision` **没有默认值**，必须显式 |
| `escalation.quote` | **不消耗、不占配额** |
| `escalation.decide` | 占 grant |
| `skill.list/invoke/publish` | `invoke` **钉死** `ref` 的内容哈希 |
| `memory.write/retrieve/supersede` | 写入后不可变，纠正只能 supersede |
| `audit.tail` | 追加式日志的尾部 |
| `metrics.get` | 暴露 `beta: [α, β]` |
| `bye` | 关闭 |

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
| `stale_epoch` | 服务端已重启，客户端的世界观过期了 | ❌ **必须先重新 hello** |
| `budget_exhausted` | 本次预算用完 | ❌ 本动作 |
| `backlog_full` | 背压 | ✅ 退避后重试 |
| `tainted_input` | 输入带不可信标，不许直接产生执行动作 | ❌ |
| `quota_exhausted` | 配额用完 | ❌ 本周期 |
| `license_conflict` | 许可证闭包不兼容 | ❌ |
| `lease_held` | 该资源已被别的执行支持有 | ✅ 排队后重试 |
| `shadow_nested` | 执行型分支不许再嵌套 | ❌ |
| `frame_too_large` | 单帧超 1 MiB | ❌ |
| `bad_request` | 帧能解析，但请求本身不合法（未知类型、非法迁移、策略超限） | ❌ 改了再发 |
| `recovery_required` | 盘上状态与哈希链不符，拒绝继续 | ❌ 需人工裁决 |

★ **`stale_epoch` 与 `recovery_required` 曾经在表里是 ✅，而它们是 ❌。**
两者都"看起来可恢复"——但恢复它们的方式**不是重发这个请求**。
**一个重试标志绝不许承诺一次永远不会成功的重试**：链不匹配，下次开还是不匹配。
这就是把这一列的定义钉死成"能不能原样重发"的原因。

**这张表与 `core/refusal.go` 的 13 个常量必须逐字一致**，且「能原样重发吗」必须
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

`kind` 是**执行形态的调度键**：`algorithm` | `code` | `script` | `llm_api` | `rl_policy` | `human`。
**"热"是状态不是类型**——一个 `script` 会因为调用频次上升到 `code`。

组合技能另有三条（见 [../store/README.md](../store/README.md)）：
数据流接线、`evidence = min(子技能)`、可靠性是乘积 `∏P_i`。

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
// apply 之前写
{"rec": "intent", "trace": ..., "attempt": ..., "session": ..., 
 "task_class": ..., "skill_ref": "<内容哈希>", "strategy": ...,
 "budget": {...}, "action_hash": ...}

// apply 之后写
{"rec": "resolution", "of": "<intent head 哈希>",
 "outcome": "ok|error|timeout|refused|degraded|unknown",
 "verified_by": "verifier", "verify_assert": ...,
 "gain": ..., "actual": {...}}
```

**重放规则：**

> **`intent` 之后没有同名 `resolution` ⇒ `outcome = "unknown"`，
> 必须显式裁决，不许默认成 `ok`。**

`unknown` 是**诚实的第三态**。把它并入 `ok` 会制造假成功；
并入 `error` 会惩罚一次可能真的成功的执行。**两者都是撒谎。**

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

| 项 | 状态 |
|---|---|
| 公理 A1–A8 | ✅ 已定 |
| 帧格式 / 消息集 | ✅ 已定 |
| 拒绝码表 | ✅ 已定 |
| 四个 ABI | ✅ 已定 |
| 执行记录（intent/resolution） | ✅ 已定 |
| 状态机与守卫 | ✅ 已定 |
| 实现 | ⬜ 未写一行 |
| 自由判据脚本（curl 驱动全流程） | ⬜ 未写——**它是本契约唯一的验收方式** |
