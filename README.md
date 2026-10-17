# TEPS —— Tri-Experiential Praxis System

> **名字由项目所有者定案（2026-10-11）：TEPS = T·E·P·S = Tri-Experiential Praxis System。**
>
> `Tri-` 是三个因子的乘积，`Praxis` 是**实践**——这个系统靠做过而不是靠知道。
>
> **「三生万物：AI × 算力 × Skill」仍然是宗旨，不是名字。** 名字说这个系统**是什么**，
> 宗旨说它**要去哪**；把两者压成一个词，就是让名字去承担一个它承担不了的主张。

一个**宿主无关**的微内核 + 外设群：分布式、神经符号混合、可持续学习。

它**不依赖** DSH、Cordis、Inimerse 或任何宿主。**这不只是设计目标，是可执行的判据**：

> **删掉 DSH、删掉 Inimerse 之后，一段 curl NDJSON 脚本仍能驱动全流程。**

---

## 宗旨与验收

```
万物 = AI × 算力 × Skill          ← 乘法不是加法：任一因子为零则积为零
```

「无限拓展」的工程含义：**加一件事的边际成本是 O(1)，不是 O(n)**。
不存在"加一个技能要重构内核"的时刻。

| # | 验收 | 含义 |
|---|---|---|
| 1 | **AI 可换** | 换掉模型，内核与外设一行不改 |
| 2 | **算力可加** | 加一台机器 = 加一条注册表项 |
| 3 | **Skill 可长** | 新技能不改内核 |
| 4 | **组合可爆** ★ | 产生**未经人手编写**的新能力 |

第 4 条最难，**也是唯一能证伪宗旨的一条**：如果系统永远只能做人手写过的组合，
那它是"三生十"，不是三生万物。

### 就绪度不对称（决定启动顺序）

| 因子 | 就绪度 | 依据 |
|---|---|---|
| 算力 | 🟢 **已在闭环里** | `organs/pool/` 登记表 + 租约 + 准入；`arbiter.rank` 的钱由池算。**加一台机器 = 加一条注册表项** |
| Skill | 🟢 设计完整 | 本仓库的 ABI 与技能店规格；双边证据、规则身份、组合不洗白孩子 |
| **AI** | 🟢 **已闭环（本机，代价是钱与时间）** | ★ 这一格曾经是**两行**：一行说「🔴 不在回路里」（47 个文件里 0 处模型调用），一行说「🟡 有机制，无模型」——**同一个因子，两个记号，两句不同的话**，读的人分不出哪句是真的。合起来之后它是这样的：**内核里 0 处模型调用**（结构上 AI 不是依赖，`tools/check_nomodel.py` 扫 48 个引擎文件）、**模型是一个技能**（机制上 AI 可换：两个「模型」按普通技能注册、两个不同 ref、跑同一个执行器与核验器、**必败用例也跑**、两个 `skill_id` ⇒ 后验分开记 ⇒ 系统能学会哪个更好）、而**载荷现在真的会说话了**（`examples/local-model/`：本机 llama.cpp + Qwen2.5-7B，`run.sh` 160 秒拿 E1，`resident.sh` 33/4/1 秒拿 E1，**必败用例 `held: true`**）。⇒ 剩下的只是**代价**：模型是 3.8 GB 的本地文件、机器相关、常驻才能进一秒级。⚠️ `drive.sh` 里那两个「模型」仍然是**桩**（它们证明的是结构）；**真模型在 `examples/local-model/`**，它不进默认路径，因为要 3.8 GB 的模型与 33 秒预热。★ 两条规则长得像一条，而它们是两条：`check_nomodel.py` 的边界是**引擎**（`core/ organs/ adapters/ store/ evidence/`）——**内容可以调模型**；而 `store/manifest.go` 说**技能不许取网**（`env.network must be false`，理由是**可复现性**不是安全）。⇒ ★★ **一份调模型的载荷可以是内容，但它发布不成技能。** 所以「AI 先租（API）」那条路**走不通**——不是没做，是被一条设计规则挡住了：**在这个系统里，一个模型是一个本地程序，不是一次 API 调用。** |

★ **算力那一行此前写着「注册表还没有」，而它已经写了好几轮了**——又是同一个形状：
**一个事实写在文档里，代码往前走了。** 所以这张表现在也有一台守卫在看着（见下）。

★★ **AI 那一行的读法要精确**：「AI 可换」不是一句主张，它现在是**平凡的真**——
**这个仓库里没有模型可换。** 67 个 Go 文件里没有一处会调用模型；`net/http`
只用作内核自己的 `POST /k` 传输；`exec.Command` 只用在执行器里（那是技能，不是模型）。

⇒ 设计里的 `AI × 算力 × Skill`，**AI 那一项目前是常数 1，不是因子**。
这不是「未核实」，是「不在回路里」——两件事的修法完全不同。


### 一个本地套接字是可复现的，一个远端主机不是

`env.network` 曾是一个布尔值，而它在一个方向上错了。「连一个套接字」同时覆盖了两种情况，而它们在本项目唯一在意的那件事上不同：

| 情况 | 钉住了吗 | 该不该拒 |
|---|---|---|
| 远端主机 | 没有——答案取决于它今天说什么 | **拒**（理由是可复现性，不是安全） |
| 本机套接字 | 有——它背后的东西是本机状态的一部分，模型文件由自己的字节钉住 | **放行** |

旧规则把两者一起拒了，于是**一个本地模型发布不成技能**——恰好是这个项目需要的那一种。

现在 `env.reaches` 是一个列表，每条 `{socket, artifact}`：

- `socket` 必须是回环。判据是**白名单不是黑名单**：黑名单会放过 `tcp:0.0.0.0:8080` 与以后每一种可路由地址的写法。一个可路由的地址是**套着本地标签的远端**，理由里直接说「换 `env.network`，然后等着被拒」。
- `artifact` 是套接字背后那个东西的内容哈希。**没有它，「本地」是一句没人核的声明。** 商店核不了那个文件（它在商店之外），所以它是一条像 `provenance.source` 那样的**声明**：写下来、必须给、**永远不被当成检查**。
- 同一个套接字写两遍被拒（一个端点一条）。
⇒ ★★ **启动顺序要改**：原写的是「AI 先租（API）」，而 `store/manifest.go` 拒收 `env.network: true`——**技能不许在运行时取网，因为一个行为取决于远端今天说了什么的技能不是可复现的，而哈希仍然会声称它是。** ⇒ **租 API 那条路走不通**：一份调模型的载荷可以是内容（`check_nomodel.py` 只守引擎），但它**发布不成技能**。所以这里是「算力用几台开发机 · Skill 自己造 · **模型要本地的**」。

★★ **本机现在真的有一个本地模型可跑**（实测）：`tools/install_llama.sh` 把 llama.cpp 钉在 `b11617`（sha256 `bc9c8bd3488e5778137c2289d951af60a94077a6bea51b7e30461033b084e07f`）装进 `.toolchain/llama/`，模型是 `/mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf`。问它「什么是哈希链」，它答：**「哈希链是一种将一系列哈希值链接起来的数据结构，每个链节的哈希值依赖于前一个链节的哈希值，从而形成一个不可篡改的链条。」**

★ **实测代价**：**冷加载 ~93 秒**（模型在 `/mnt/d`，走 9p 文件系统）、**一次 8 token 的调用共 ~187 秒**。⇒ 这两条数说明**一次调用起一个进程是不行的**（93 秒里绝大部分是加载），而**让模型常驻就需要一个本地套接字**——而 `env.network` 现在是一个布尔值，它分不出「远端取网」与「本地套接字」。**那是下一件事。**
**MVP 今天就能开始，不必等一个 1B 模型。**

---

## 三条约束推出同一个结构

| 约束 | 出处 | 推出的东西 |
|---|---|---|
| 三生万物 | 项目所有者 | 因子必须能独立替换/扩展 ⇒ **外设化** |
| **内核必须微小** | 项目所有者 | 只有"唯一权威/原子/必经"进内核 ⇒ **四概念** |
| 自由判据 | 本设计 | 删掉宿主仍能跑 ⇒ **内核之外全部走协议** |

```
微内核（4 概念） + 全部外设走同一协议
```

**如果这三条不能推出同一个结构，说明设计里还有没想清的地方。**

---

## 目录

```
core/       Frame · Log · Budget · Refusal      459 行 · 6 类型 · 零依赖 · 领域词 0
evidence/   证据阶梯 E0–E4                          唯一一处定义，store 与 memory 都从这里取
organs/     session（状态机 + 停滞守卫，已实现）
            composer（上下文合成器，已实现）· memory（双时间轴，已实现）
            arbiter（升级仲裁 + Beta 自校准，已实现）
            executor（规划 + 核验：判定不看执行者的意见，已实现）
            算力池                                其余外设，纯函数为主
store/      cas（内容寻址）· manifest（Skill ABI）· license（闭包代数）
            refs（可变的那一半）· compose（组合代数）· register（八步流水线）
adapters/   shell（参考传输：stdio + HTTP，拥有内核）
            mcp（翻译器：JSON-RPC over stdio，不 import 内核）
            没有 dsh/ —— DSH 走 MCP，所以 mcp 就是它
docs/       规格
tools/      check_kernel.py                      四条内核守卫，可跑
            check_contract.py                    文档 ↔ 代码的拒绝码不许漂移
            check_adapters.py                    翻译器不许 import 内核
            check_frames.py                      帧类型 ↔ 契约不许漂移
            check_organs.py                      器官 ↔ 两份文档的状态表不许漂移
            check_docs.py                        任何一份 .md 里四条：⬜ 不指向存在的路径 · 状态表只在一处 · ✅ 点名了真实产物 · ★★ 「N 个测试」必须与实测一致
            check_assertions.py                  ★ 断言里的 JSON 值片段不许互相是前缀
            check_nomodel.py                     ★ 自由判据的 AI 那一半：删掉模型还能跑吗
            check_clients.py                     ★ 接入面：server.json / CLIENTS.md / SKILL.md 三份文件对同一件事的说法
            check_examples.py                    ★ 成对的负控：只有 failed 算被驳倒，其余三种各说清在证明什么
            ef_ledger.py                         ★ 收件箱分账：能回 / 等对方 / 回不了，且**不从前缀里猜**
            check_all.sh                         ★ 上面这些的唯一入口（含守卫自检）
            check_format.sh                      gofmt 干净，别让 diff 里混进重排
            drive.sh                             ★ 自由判据：只有 curl 驱动全流程
            drive_mcp.sh                         ★ 自由判据：只有 printf 的 JSON-RPC
            go · install_go.sh                   自包含工具链：包装器 + 重建
            check_provenance.py                  ⚠️ **不是守卫**：要联网，核对 PROVENANCE 里的名字
.toolchain/ go1.27.1                             gitignore，可重建
```

## 规格

| 文件 | 内容 |
|---|---|
| [docs/KERNEL.md](docs/KERNEL.md) | ★ 微内核边界：进核判据、四概念、出核清单 |
| [docs/ARBITER.md](docs/ARBITER.md) | ★ capex 仲裁：为什么"永远临时应付"是设计缺陷 |
| [docs/CONTRACT.md](docs/CONTRACT.md) | 八公理 A1–A8 · 帧格式 · 消息集 · 拒绝码 · 四个 ABI |
| [docs/PROVENANCE.md](docs/PROVENANCE.md) | 选型证据审查：为什么模型层不能作依据 |

---

## 当前状态：**P0–P5 已落地，执行器已落地**

诚实地写清楚，免得后来者读成"已经做完了"：

| 项 | 状态 |
|---|---|
| 内核边界（四概念） | ✅ 已定 |
| 协议契约（公理/帧/消息/拒绝码） | ✅ 已定 |
| 四个 ABI + 执行记录 | ✅ 已定 |
| capex 仲裁 | ✅ 已定 |
| 四条内核守卫 + 契约守卫 + 格式守卫 + 适配器守卫 + 帧守卫 + 器官守卫 | ✅ **可跑，已验**（领域词泄漏、文档漂移、格式漂移、适配器边界、帧类型、器官清单各有注入测试） |
| **Go 工具链** | ✅ **go1.27.1 已装**（项目内自包含、sha256 校验、可重建） |
| **P0 内核** | ✅ **四概念已写成类型**（459 行 / 6 公开类型 / 领域词 0） |
| **P0 shell 适配器** | ✅ **stdio + HTTP，curl 可驱动** |
| **P1 会话状态机 + 停滞守卫** | ✅ **`organs/session/`，纯函数，22 个测试** |
| **P1 会话落在链上** | ✅ **重启后从日志重放；适配器内存里没有真相** |
| **P2 技能店：内容寻址** | ✅ **`store/cas.go`——读时重算哈希，不符即拒** |
| **P2 技能店：许可证闭包** | ✅ **`store/license.go`——闭包等级 = 成员最大值** |
| **P2 技能店：注册流水线** | ✅ **`store/register.go`——八步，`ref` 最后才动** |
| **P2 技能店：可变的一半** | ✅ **`store/refs.go`——先写历史再移指针** |
| **P2 组合代数** | ✅ **`store/compose.go`——可靠性是乘积；剪枝要报告丢掉了什么** |
| **自由判据** | ✅ **`tools/drive.sh` 164 项断言全过，只用 curl** |
| **P3 证据阶梯** | ✅ **`evidence/`——唯一一处定义；未知等级传播，不被排序** |
| **P3 上下文合成器** | ✅ **`organs/composer/`——按 score/单位长度背包；年表段不许被重排** |
| **P3 记忆** | ✅ **`organs/memory/`——双时间轴；写入不可变；E0 写得下但默认看不见** |
| **P3 两个器官落在同一条链上** | ✅ **memory 与 session 各自投影自己的记录，互不 import** |
| **P4 升级仲裁** | ✅ **`organs/arbiter/`——按价格取名不按层级取名** |
| **P4 后验自校准** | ✅ **带指数遗忘的折叠：技能腐烂后 17 次失败内被自动弃用** |
| **P4 capex 固化闸门** | ✅ **同一笔节省，E1 拒绝固化而 E4 固化** |
| **P4 执行记录落在链上** | ✅ **intent/resolution 拆两条；执行者不许自证** |
| **P5 MCP 翻译器** | ✅ **`adapters/mcp/`——一个工具，不 import 内核，拒绝原样交回** |
| **P5 适配器按协议命名** | ✅ **没有 `adapters/dsh/`：DSH 走 MCP，所以 mcp 就是它** |
| **P5 适配器边界守卫** | ✅ **参考传输必须 import，翻译器必须不 import；两个方向都有注入测试** |
| **执行器 + 独立核验器** | ✅ **`organs/executor/` 纯函数 + `adapters/shell/execute.go` 脏半边；56 个测试，4 个变异全被抓住** |
| **执行者不许自证** | ✅ **`Judge` 的入参里没有「成功了吗」这个位置；判不了就不写 resolution** |
| **证据只由真实执行提升** | ✅ **E0→E1，且不碰 `Promote` 的 E0 闸门** |
| **组合（执行图）** | ✅ **`organs/executor/compose.go`——serial/parallel 算成执行图；另外四个算子各自说明缺什么；9 个变异全被抓住** |
| **组合接进适配器** | ✅ **`skill.compose` 按图 spawn、串行步的 stdout 接下一步的 stdin；16 项断言，4 个变异全被抓住** |
| **组合跑通了** ★ | ✅ 第一次由 curl 跑出**未经人手编写**的执行：两个孩子都没有的行为（`CHAINED`）出现了 |
| **组合是递归的** ★ | ✅ **`kind:"compose"` 给组合一个普通的内容地址**，于是它进得了技能店、当得了另一个组合的孩子。按 `ref` 跑；孩子本身是组合就展开成它的孩子（`maxComposeDepth=8`，那是**资源账不是环检测**——环早已不可能，因为 manifest 装的是孩子的哈希）。后验按**它的 skill_id** 分组（不是 ref：升级会换 ref，而按 ref 分组会在技能被提升的那一刻重置，抹掉促成那次提升的运行记录） |
| **组合搜索（规划器）** ★★ | ✅ **`organs/planner/`——从类型里搜出没人写过的链，按可靠性剪枝、报告被丢掉的路。`compose.propose` 只提议不运行；提议用 `skill.publish` 注册（组合就是技能，没有第二个发布口）** |
| **组合可爆** ★★★ | ✅ **第一次闭环：规划器找到 `typ.split;typ.tag`（没人告诉它），注册成 `auto.tag`，跑通。同一个输入下 `typ.tag` 单跑印 `SOLO`、链上印 `CHAINED`——一个任何单个技能都不产生的行为** |
| **反射层（零上下文的回答）** | ✅ **`organs/reflex/`——建立/可改/可消退；一次观测永远不够（从 0.3 起步，阈值 0.6），失败比成功贵。有一条测试直接读源码，断言它不 import 任何上下文管线** |
| **任务入口（率的分母）** | ✅ **`organs/intake/`——率终于有了分母。没记层的到达也算进分母：丢掉它会靠忘记不顺利的那些来提高率** |
| **经验蒸馏** | ✅ **`organs/distill/`——把一次成功变成一条候选规则。只有成功、且只有花了成本的成功可以蒸；已经零上下文答过的东西再蒸一次就是把同一次成功数两遍** |
| **升级定价（问价 / 付价）** | ✅ **`escalation.quote` 不写链、不扣预算；`escalation.decide` 占 grant，且必须带上它依据的 `quote_id`——价格一动那张报价就作废。契约里唯一一处「先问价不花钱」** |
| **度量（这个数有没有定义）** | ✅ `metrics.get` 不新增任何事实，新增的是**「它有没有定义」**：同一个 `0` 可以表示「从没收过任务」也可以表示「一个都没答上」。三个因子各自从拥有它的器官取，`reliability` 同时报 `mean` 与 `weakest`（乘积里最弱的一环才是那个数） |
| **算力（宗旨里唯一零代码的因子）** | ✅ **`organs/pool/`——登记表与租约都是链的投影。容量是租约不是计数（拒绝点名持有者）；租约必须过期（否则持有者一死容量就永久漏掉）；免费是一个价格不是没有价格。价格与预算同单位，所以仲裁器不用加第二种货币。租约记录存**绝对过期时间**，重放同一条链得到同一个池** |
| **准入（容量是一条真的约束）** ★★ | ✅ **池第一次决定一次执行能不能跑**：`skill.invoke` 可以点名一个资源，或让池从本机资源里挑最便宜的；**占不到槽，执行就不发生**。不点名就照旧（不凭空造出一个没人登记过的资源）；执行器只认得本机的资源，点名远端被明确拒绝——把 `gpu.far` 记成一次执行发生的地方就是在链上写假话。顺序是有意的：**先占槽、再扣钱、最后 spawn**，占不到槽的执行不该留下已付费的痕迹 |
| **报价的钱从池里取** ★★ | ✅ `arbiter.rank` 的 offer 可以点名一个资源，**钱由池算**；同时给 `resource` 和 `cost` 被拒——**一个数不能有两个来源**，而它们不一致时谁也不会发现。**占不到的报价被排除出排序，但照样被报出来**：静默丢弃的剪枝无法被审查。★ 这里也踩了一次「同一个事实两份拷贝」：每条报价的账原先用 `action` 当键，而两个 offer 可以都是 `skill` 动作 ⇒ 后一个把前一个的账盖掉，响应看起来完全正常；改用 offer 自己的不透明 `Tag` |
| **双边证据（E1 需要必败用例）** ★★★ | ✅ 声明的断言**必须是双边的**：一份只有必过用例的 `verify_assert` 证明的是「它通过了」，**没证明「它有能力失败」**——`exit == 0` 的技能只要永远返回 0 就能拿到 E1。现在 `verify_refute` 是提级的**必要条件**，而且**只有 `failed` 才算被驳倒**：`timeout` 是关于时间的事实、`refused` 说明进程没起来、`unknown` 是判不了，三者都不是「这条断言被证明为假」。必败用例的结果**在每条提级路径上都跟着走** |
| **探针沉默 ≠ 拿到但是空** ★★ | ✅ 两件事的 stdout 都是空字符串——**一个只读 stdout 的读者看到的完全一样**，所以判定结果必须不同。★ 顺着查出真缺陷：**空针匹配一切，所以它什么都没断言**——`stdout contains ""` 在一个什么都不打印的进程上是 `true` ⇒ 判定 `ok` ⇒ 这个技能拿到 E1。一条**不可能失败的**断言与一条没有断言之间，差别只在读的人以为自己检查过什么。现在它是 `unevaluable`。对照：`stdout == ""` 仍然成立（「它什么都没打印」是合法声明，而它也能是假）。**沉默与超时互斥**也由构造保证并两侧断言 |
| **组合不许洗白孩子的证据等级** ★★ | ✅ 组合不产生任何观测，所以它不能声称比最弱的那部分更高的信任。★ 这条规则**在文档里写了很久、在代码里从来没被核过**——`MinEvidenceAll` 是个从没被非测试代码调用的前向器。而 `min(children)` 是**天花板不是等级**：注册时组合从未作为一条链跑过，所以入账仍是声明值（我第一次改错，被既有断言抓住）。★ 补它的时候又栽一次：**夹具里孩子等级都相同** ⇒ 「取第一个」与「取最小」分不出来，变异活了下来 |
| **组合的两边性继承自孩子** ★★ | ✅ **组合的提级是真的**：一次链式运行确实产生一个观测（每个孩子都在这次链产出的数据上成立、顺序对、线接对了），而**它能失败**。规则是：一个孩子是两边的当且仅当它声明了 `verify_refute`；**任何一个孩子是单边的，整条链就是单边的**。四道拒绝各说清一个不同的事实（内联没有清单 / 链没通过 / 有孩子单边 / 已在天花板）。★ 旗舰组合 `auto.tag` 因此**提不了级**——`typ.split`/`typ.tag` 只声明了必过用例。**这是真实的能力缺口，不是拒绝的借口：要让组合能提级，先让每个孩子能证明自己会失败。** |
| **组合第一次到了天花板** ★★★ | ✅ **闭环第一次完整**：`typ.split`/`typ.tag` 原先只有 `exit == 0` 而载荷永远退出 0——那份声明**不可能被证伪**。现在它们分辨一个标记，喂 `REFUSE` 就退出非零，各带一条必败用例。于是 `auto.tag` 的提级真的成功：`to=E1`、新 ref、提级前那一份仍是 E0。★ 变异抓到一个**我这一轮刚写下的**真 bug：`elevateCompose` 只数 `len(man.Refute) == 0`，于是「**声明了**但驳不倒」的孩子也算两边的——**存在性判据冒充一致性判据**。现在真的跑一遍，只有**确实被驳倒**才算数 |
| **规划器找到的链第一次走完全程** ★★★ | ✅ **找到 → 发布 → 跑通 → 提级 → 冻结成 stable**。提级到 E1 只是拿到了进 stable 的**资格**；这一条把资格用掉：`auto.tag` 提级后的 ref 进 stable，stable 上那份解析出来是 `kind=compose` 的 `E1` 制品。**这条链没有任何人写过，也没有任何人告诉过系统它存在** |
| **同一批记录的两个读数** ★★ | ✅ 后验的 `mean` 是**带遗忘折出来的**（`DefaultDecay = 0.98`），原始成功率是不折的。两者不同是设计使然，但在此之前**没有任何地方报过这个差**——「这个数是折过的」只活在注释里，读这个数的人分不出来。`arbiter.state` 与 `metrics.get` 都报 `raw_rate` 与 `decay_gap`。★ 补断言时连栽两次：先是一条变异靠「只断言 `raw_rate` 出现过」漏网（**存在性判据冒充一致性判据，第七次**）；再是 `gap == mean − raw` **只验了算术**——`meanOf` 一坏两者一起偏移，那条等式照样成立。**与自己一致不等于正确，要抓住它需要外部参照**（`mean` 必须等于它自己报出来的那些 `posterior` 行的平均值） |
| **组合的 `elevate` 不再被静默忽略** ★★ | ✅ `composeBody` 有 `Elevate` 字段，而 `composeResult` 里没有 `elevation`、代码里也没有任何地方读它——传了 `elevate: true`，拿到 200，**什么都没发生**。那是最坏的一种 API 谎言：不是拒绝，也不是做到，是**看起来做到了**。现在明确拒绝并说清三件事：为什么（组合没有自己的断言）、上限是多少（`ceiling` = 孩子的最小值）、真正的路在哪 |
| **判定规则的身份** ★★ | ✅ **一个后验不能在两套规则上折**。后验按 `(skill_id, task_class)` 分组，而 `skill_id` 与版本无关 ⇒ 同一句 `skill_id` 用不同断言重新发布后，新旧运行被折进同一个键，报出来的数是**两个不同问题的平均值**（`0.5` 可以是「一个程序一半时间能用」，也可以是「两个程序，一个总行一个从不行」）。`Plan.Rules = RuleID(asserts)`——**排序后**逐条写字节 + `0x00` 分隔符的 sha256：顺序故意不算身份（每条断言都必须成立，重排不可能改变判定），**身份应该追踪「什么改变了答案」，不是「什么改变了文本」**。`mixed_skills > 0` 时能力乘积**未定义** |
| **一个词两个意思：`Fires`** ★★★ | ✅ `organs/reflex/` 里 `Fires` 同时是一个**字段**（计数）与一个**函数**（谓词「强度够不够回答」），而那个字段此前数的是**观测次数**不是**开火次数**（`Reinforce` 与 `Weaken` 都无条件 `r.Fires++`）。后果是实测出来的：一条新规则（起点 `0.3`、阈值 `0.6`，够不到开火线）被观测一次 `failed` 之后，`reflex.state` 报 `{"extinguished":1,"rules":[{"fires":false,"firings":1,...}]}`——**同一份 JSON 里两个字段差一个词、说的是相反的事**，而那句判据旁边的注释逐字写着它要区分开的正是「从没强到能开始」。改法：那个字段只说一件事——**它答过几次**，所以只在一个观测**落在一条正在答的规则上**时加一，判据取**削弱之前**的强度。★ 一条没到阈值的规则被报为失败，是「它没被问到」，不是「它答错了」——把前者记成后者，会让一条**从不回答**的规则看起来像一条**每次回答都错**的规则。★ 沿途被自己的守卫抓两次：`check_docs.py` 报我改了测试数没改文档；`check_assertions.py` 报我新写的 `"strength":0` 是 `"strength":0.7` 的真前缀——**一条读起来像一致性判据的存在性判据**，于是那条断言改成只证 `fires`/`firings`。变异 4/4 行为证据 |
| **一个看不见分母的比例** ★★★ | ✅ `session.status` 的 `flags.waste` 是一个**比例**，而它的分子与分母都不在 JSON 里。两次探针：「两次升级都被拒后放弃」与「一次升级被 grant、一次被拒后放弃」都报 `"waste":1` —— **读不出是 2/2 还是 1/1**；而 `waste: 0` 更读不出是 0/1 还是 0/50（`has_waste_evidence` 只分得开「一次升级都没有」这一种）。改法：`Machine.Flags` 增加 `Escalations`/`EscalationsDegraded`，与 `Waste` **同一次读取**，`flagsJSON` 报出来。★ 沿途钉住一处三方不一致：`Machine.EscalationsDegraded` 的字段注释写着「the subset that ended in Degraded **without a Grant in between**」，而 `Step` 写的是 `out.Escalations`（全部），`docs/CONTRACT.md:278` 写的是「升级之后仍然 degraded 的比例」——**代码与契约一致，注释是烂掉的那一份**。变异 4/4 |
| **`ops` 在宣传一个空族** ★★★ | ✅ 分派是**表驱动**的（`adapters/shell/ops.go` 的 `claims()`，`main.go:210-230` 用 `strings.HasPrefix(f.T, c.Prefix)`——**变量不是字面量**），而 `tools/check_frames.py` 的三个正则扫的是**处理器源码里的字面量** ⇒ **表对守卫是隐形的**。实测：注入 `{Prefix: "teleport.", Handle: s.handleSession}` 之后，`check_frames.py` 报「帧类型一致」退出码 0，`drive.sh` 的 ops 段报「每一个族都真的被分派认领（18 个）」——**两台守卫都绿，而 `ops` 在宣传一个对什么都答 unknown 的族**。运行时那条为什么也漏：它的判据是回答里没有 `no family claims this prefix`，而前缀**匹配上了**就会回不带那句话的 `unknown op` —— **存在性判据又一次假扮成一致性判据**（上次是「认领 vs 没认领」，这次是「认领且有人管 vs 认领但是空的」）。改法：`check_frames.py` 新增 `claims_table()` / `handler_frames()` / `check_claims_are_inhabited()` 两条子句（**包内有人认得** + **被点名的处理器自己也认**），并加 `BARE_EQ_RE`/`BARE_CASE_RE` 收**不带点**的帧名（`ops` 是 `if f.T != "ops"`）。变异 4/4 |
| **一个窗口必须报出它的窗口** ★★★ | ✅ `audit.tail` 此前只回 `entries`/`head`/`seq`/`epoch`：链上 25 条、不传 `limit` 时回 20 条，而回答里**没有任何一处说这件事** —— 调用方分不出「这 20 条是我要的」还是「服务端替我决定的」，也分不出「这就是链」还是「这是链的尾巴」。与 `metrics.accuracy` 报 `traced`/`chains_run`/`unrun`、与 `waste` 报它的两个计数是同一条：**一个数是某个窗口里的数时，窗口是它的一部分。** 现在报 `limit`（生效的那个，0 表示请求没给）/ `returned` / `first_seq` / `last_seq` / `truncated` / `basis`。★ 而且 drive.sh 那条断言的标签写着「不是无声地全给」，它查的却是 `len(entries) == 20` —— **一条查行数的断言配了一句承诺报默认值的标签**，标签改成它真的查的东西，另外补了报默认值的那条。变异 5/5 |
| **一个叫 `open` 的数数的是「不是闭的」** ★★★ | ✅ `breaker.state` 此前是 `if st != breaker.Closed { open++ }` —— 一个叫 `open` 的数数的是 **`open + half-open`**。实测（同一个宿主，三次宿主失败之后）：
| **一个把容量说成预算的拒绝** ★★★ | ✅ `organs/pool/pool.go` 的 `Cheapest` 无论为什么找不到候选都回同一个 `budget_exhausted`，理由是一句 `"every one is either at capacity or costs more than N money"`。实测（一个 `capacity:1` 的资源，预算给足 `1000000`）：它回的仍然是 `{"code":"budget_exhausted","retryable":false}` —— 而**那句话是假的**，那个资源要 0 money / 0 tokens，调用方刚刚证明了它有 1000000；挡住它的是**容量**，而容量是暂时的。`core/refusal.go:50` 早就为这件事准备了 `CodeLeaseHeld: true`，旁边写着「the condition is temporary and the reason string names the wait」；而 `CodeBudgetExhausted: false` 告诉调用方「重试没用」。**一个把容量说成预算的拒绝，把一个会自己变好的条件报成了不会。** 改法：四个事实四个答案 —— ① 空登记表 ⇒ `bad_request` ② 只有容量 ⇒ `lease_held`（可重试）+ 点明等多久 ③ 只有预算 ⇒ `budget_exhausted` + 说出最便宜的要多少、调用方有多少 ④ 两个都挡着 ⇒ 码取预算那一个（容量会自己好，预算不会），理由把两个都说出来。★ `drive.sh` 里有**两条断言把那个缺陷写进了期望**（一个期望 `budget_exhausted`、一个标签写着「链上没有留下痕迹」而它查的是拒绝文本）——改完它们红了，**红得对**。变异 4/4 |
| **只算了一次价，而记录把它挂在了另一个主体上** ★★★ | ✅ `adapters/shell/escalation.go` 的 `escalationDecide` 写进链里的 `skill` 与 `task_class` 取的是**请求**，而 `utility` / `p` / `grant_cost` / `grant_ms` 取的是 arbiter 选中的那一条。实测（两条都是 `skill` 动作的 offer：`read.file` cost 1 / latency 5，`write.file` cost 50 / latency 900）：报价选的是 `read.file`（`grant_cost=1`），而拿这张报价把 `skill` 报成 `write.file` 之后链上写下了 `{"action":"skill","grant_cost":1,"grant_ms":5,"p":1,"rec":"escalation","skill":"write.file","utility":8.5}` —— **「关于 write.file 做了一次决定，它花 1、效用 8.5」，而这三件事没有一件是关于 write.file 的**（它花 50、延迟 900、效用是负的）。`priceOf` 的注释写着「Two computations of the same price is how a system ends up quoting one number and acting on another」；这里比那更糟：**只算了一次价，而记录把它挂在了另一个主体上。** ★ `task_class` 是同一件事的第二个入口：`priceOf` 根本不读它（后验是按 `offer.TaskClass` 查的），所以把 `task_exec` 换成 `chat` 之后 `quote_id` **照旧匹配**，唯一的防线是一条显式检查。改法：记录的四行全部来自 `chosen.Offer`；调用方**说了就必须一致**（不一致 ⇒ `bad_request` 并点名两边），**没说就用被定价的那一条** —— 一个不点名的决定没有错，一个点名点错的必须被拒，而不是被静默改写成对的那个。变异 4/4 |
| **把 `ok` 检查了，然后把未知照样折到 E0** ★★★ | ✅ `adapters/shell/knowledge.go` 的 `compose.build` 此前是 `rank := 0.0; if r, ok := evidence.Rank(evidence.Level(c.Evidence)); ok { rank = float64(r)/4.0 }` —— **`ok` 被检查了，然后未知的那一种照样落到 `0.0`**，也就是 `E0`。实测（两条只有 `evidence` 不同的候选，预算只够留一条）：`a=E4 b=E0 → kept=["a"]` 与 `a=E4 b=E9 → kept=["a"]` **逐字相同**；`a=E9 b=E4 → kept=["b"]` 与 `a=E0 b=E4 → kept=["b"]` 也逐字相同。**`E9` 与 `E0` 在这里没有区别。** 而 `evidence.Rank` 的注释逐字写着那件事不许发生：「An unknown level is deliberately **not** mapped to zero. Zero is a real level (E0), and folding "E9" onto it would turn a typo into a trust decision.」★ 同一份代码库里另一扇门不这样：`memory.retrieve` 对 `min_evidence:"E9"` 回 `bad_request` /「unknown min_evidence: E9」——**一个事实两扇门两个答案**，而这里是那个更糟的答案：它不报错，它按**最弱**的等级静默处理。改法：`compose.build` 用 `evidence.Known` 拒，理由点名第几条候选、它的 id、那个读不懂的等级，并说清「读不懂」与「最弱」是两件事。★ 顺带修了 `store/manifest.go` 的 `evidenceRank`：它是 `r, _ := evidence.Rank(l)` —— **一个前向器把它所前向的那个函数、写在注释里的唯一一条禁令，恰好执行反了**。今天还不可达（`Validate` 拦住未知的声明值），但不需要等到可达才算错。现在它返回 `(秩, 在梯子上吗)`，而 `Register` 的天花板检查要求**两边都读得懂**才比较。变异 4/4 |
| **同一份前向器写了三遍，而两句注释都说只有一扇门** ★★★ | ✅ 同一个 `r, _ := evidence.Rank(l)` 在项目里被写了**三遍**：`store/manifest.go` 的 `evidenceRank` · `adapters/shell/compose.go` 的 `rankOf` · `adapters/shell/knowledge.go` 里内联的一段。而两句注释都声称自己读的是**唯一那扇门**：`rankOf` 写着「reads the ladder through one door. The store has its own forwarder for the same reason」，`evidenceRank` 写着「the store reads the ladder through exactly one door (manifest.go)」。**当时有三扇，其中两扇犯的是同一个错。** 更糟的是那句理由本身就不成立：`knowledge.go` 早就直接 `import "teps/evidence"`，适配器并不需要什么前向器。★ Round 48 修了两处，这一轮删掉 `rankOf` 并让 `elevateCompose` **两边都读得懂才比**。★ 真正的修法不是注释，是守卫：`tools/check_adapters.py` 新增第 4 条规则 —— `evidence.Rank` 的第二个返回值不许被丢掉（判据可算：一个 `_` 就是一个被丢掉的事实）。★★ 而这条守卫的**第一版在它自己的文档上报警**：`store/manifest.go` 里那句「它此前丢掉了 `Rank` 的第二个返回值：`r, _ := evidence.Rank(l)`」是**注释**。**一个读注释的守卫报的是散文，不是代码。** 加了 `strip_comments`（真的扫一遍，字符串字面量里的 `//` 不算注释）。★ 坦白：`elevateCompose` 那个分支**今天不可达**（`Validate` 拦住未知的声明值，孩子又都过了 `Validate`）—— 这一轮的价值是**去掉第三份拷贝**与**把契约钉住**，不是修一个活的错。变异 4/4 |
| **搜索的分母不在回答里** ★★★ | ✅ `adapters/shell/propose.go` 的 `compose.propose` 只回一个 `"catalog": len(catalog)` —— **登记表条数**，而真正交给规划器的技能是 `len(skills)`，两者之间差着 `untyped` 那一批。实测（登记表 4 个技能、2 个不声明类型）：返回字段是 `[catalog, dropped, proposals, states, truncated, untyped, unusable]` —— **参与搜索的技能数没有这个字段**，读的人只能自己做减法 4 − 2 = 2。而这不是一个可有可无的数：`proposals: []` 在一个「大半技能不会声明类型」的登记表上，与在一个「全都能参与搜索、但确实没有路」的登记表上，是两个完全不同的事实 —— 一个是**问不出来**，一个是**问过了，没有**。`catalog` 这个名字本身也读错方向：它读起来是「这就是登记表」，而它是一个数。改法：报 `searched`（交给规划器的技能数）+ `untyped_count` + `basis`（说清 `catalog = searched + untyped_count`，以及「问不出来」与「问过了没有」是两件事），并在 `drive.sh` 里**用 python 算出**那个等式与 `untyped_count == len(untyped)`。变异 4/4 |
| **总数是一个读法，它的分项是另一个** ★★★ | ✅ `organs/composer/composer.go` 的 `Result` 有 `Allocated map[string]int`（**逐段**预算）与 `Used int`（一个**总和**）。读的人看到 `allocated: {retrieval: 35, recent: 15, system: 15}` 与 `used: 20`，**分不出那 20 来自哪一段** —— 而「哪一段还有余量」正是调用方决定「还能不能再塞一条」时唯一需要的那个数。一个只报总数的回答，把「哪一段满了」这个问题留给了猜。改法：`Result` 增加 `UsedBySection map[string]int`，`compose.build` 报 `used_by_section` + `basis`（说清 `used` 是分项之和），并在 `drive.sh` 与 `organs/composer/composer_test.go` 里**算出** `used == sum(used_by_section)` 且每一段都没超过自己的预算。★ 那条测试刻意让两个段各放一半：只放一段的话分项与总数恰好相等，「报的是总和还是某一段」就分不出来 —— 那正是这一轮要抓的东西。变异 4/4 |
| **`fires: true` 旁边放着一句「它现在还不会回答」** ★★★ | ✅ `adapters/shell/distill.go` 的 `distillApply` 无条件回同一句话：「established, not confirmed: it will answer only after the same stimulus is seen working once more」。而蒸馏**同一个刺激两次**会让 `applyRecord` 对已有的那一行再 `Reinforce` 一次：`0.3 + 0.2 = 0.5` 变成 `0.7`，越过 `FireThreshold = 0.6`。实测第二次蒸馏的同一份 JSON：`{"strength": 0.7, "fires": true, "note": "established, not confirmed: …"}` —— **同一份 JSON 里两个字段说的是相反的事**，与 Round 41 那个 `fires: false` 配 `firings: 1` 是同一个形状。★ 而 `drive.sh` 那条断言（`expect "★ 现在它会回答了" "$r" '"fires":true'`）只查了 `fires`，**从来没有查过那句话** —— **一句话只要不随状态变，就没有任何东西会让它变红。** 改法：`note` 由 `fires` 推出来（两句话两支），并把 `threshold` 一起报出来 ——没有它，`strength: 0.7` 与 `fires: true` 之间的那条线只活在读的人的印象里。变异 4/4 |
| **一个只约束一个方向的上限不是一个上限** ★★★ | ✅ `core/log.go` 的 `MaxFrameBytes` 注释写着「caps **one frame**」，而它只在 `ParseFrame` 里管**入口**。`Log.Tail` 只受 `MaxTail`（**条数**）约束 —— 两个数各自独立选出来，于是条数上限能造出一个违反帧上限的回答。实测（写 5 条 300 KiB 的记录、每条都在帧上限之内，再 `audit.tail` 要 `limit: 1000`）：回答是 **1537486 字节 = 1.47 MiB**。受害的是客户端：那条上限告诉它「一帧最多 1 MiB」，它按这个开缓冲区，然后在一个 1.47 MiB 的行上断掉。改法：`Tail` 从最新往回收，收到装不下为止（`budget := MaxFrameBytes - tailFrameReserve`，`tailFrameReserve = 8<<10` 留给 `head`/`seq`/`epoch`/`basis`/`first_seq`/`last_seq`）。实测改后 922704 字节、`returned: 3`、`truncated: true`、`last_seq == seq`。★ 判据里的 `len(out) > 0 &&` 是承重的：没有它，一条装不下窗口预算的最新记录会让窗口变成空的 —— 而「尾巴是空的」与「链是空的」在回答里长得一样，客户端因此**根本读不了这条链**。 |
| **一个会倒退的计数器不是一个计数器** ★★★ | ✅ `core/log.go` 的 `bumpEpoch` 是 `n, _ = strconv.ParseUint(...)` —— 解析错误被丢掉。于是 epoch 文件里只要有一个字节不是数字，`ParseUint` 回 `(0, err)`、`n = 0`、`n++` ⇒ **1**。实测（第一次启动 `epoch=1`、第二次 `2`，然后把文件写成 `abc` 再启动）：**epoch 变成 1**。而 `Epoch` 的注释逐字写着它的用途：「It grows on every Open, so a client holding a stale epoch can tell that the process it was talking to is gone.」一个能**缩小**的 epoch 让那句话反过来：持着 2 的客户端看到 1，于是它以为自己对，而它其实在跟另一个进程说话。改法：坏文件不是「从头开始」而是**读不懂** ⇒ `recovery_required`，理由里 `strconv.Quote` 出它读到的东西；空文件仍然算「还没写过」。变异 4/4 |
| **一个没有生产点的拒绝码是一个承诺** ★★★ | ✅ 拒绝码表有 14 个码，其中 **5 个生产点是 0**：`stale_epoch` `backlog_full` `tainted_input` `quota_exhausted` `shadow_nested`。而这张表的用法是「适配器作者据此写循环」—— 于是 `backlog_full` 那一个 `✅ 退避后重试` 在承诺一次**永远不会到来**的重试，而 `stale_epoch` 那一个 `❌ 必须先重新 hello` 在承诺一次**永远不会发生**的重启察觉（`docs/CONTRACT.md:67` 逐字写着「客户端靠它察觉 ⇒ `stale_epoch`」）。这与帧表里「写进契约但没人分派 = 文档在承诺一个不存在的入口」是同一条，处置也同一条：**明确标出来的缺口不是静默漂移**。改法：5 行都标上缺口标记，`tools/check_contract.py` 新增规则 —— 每个码要么在 `core/refusal.go` 之外有生产点、要么带着那个标记。★ 顺带改掉一句错的散文：契约说「这张表与 `core/refusal.go` 的 **13** 个常量必须逐字一致」，而表里与 `core` 里都是 **14** 个。变异 4/4 |
| **一个没有调用点的导出函数是一个承诺** ★★★ | ✅ `organs/arbiter/capex.go` 的 `ShouldFix` 注释逐字写着那个比较为什么必须**严格**（「The comparison is strict. Breaking even is not a reason to accumulate something: a skill that costs exactly what it saves adds a moving part, a licence obligation and a poison surface for no return.」），而 `adapters/shell/decision.go:514` 的 `arbiterFix` **自己重写了一遍 `v > 0`** —— **器官那一份是死的，活着的那一份在适配器里**；两份今天恰好一致，而「今天恰好一致」正是这个项目付过很多次学费的那种说法。实测：`organs/` `store/` `evidence/` 里有 **13 个导出函数在任何非测试代码里都没有调用点**（`Rate` `Stuck` `Edges` `Chain` `DefaultCompileCriteria` `ShouldCompile` `Choose` `Layers` `Distillable` `HalfOpenProbe` `Has` `ReliabilityOf` `Prune`）。改法：`arbiterFix` 改成问 `arbiter.ShouldFix`；`tools/check_organs.py` 新增规则 —— 导出函数要么在全仓非测试代码（**含定义文件，但抹掉定义行**）里有调用点，要么上方文档注释块里有 `DEAD-API:` 与一句理由，13 个各加一行。★ 两个自己的坑：第一版**排除定义文件** ⇒ 同文件调用者（`ShouldFix` 调 `FixValue`）被误报；第一版**没剥注释** ⇒ 每个函数的文档注释都成了调用点 ⇒ 报 0 个孤儿。**一个读注释的守卫报的是散文，不是代码**（与 Round 49 的 `check_adapters.py` 同一个坑）。变异 4/4 |
| **链读不回来的记录不是记录** ★★★ | ✅ `Log.Append` 对写出去的那一行**一个字节都不查**。而 `Replay` 与 `load` 都用 `bufio.Scanner`，上限是 `sc.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes+1)`。于是一条更大的记录会让 `Replay` 回 `recovery_required: bufio.Scanner: token too long`，而 `load` 在 `Open` 时跑 —— **服务起不来，链再也读不回来**；那个失败是**永久的、整条的**，不是这一条读不到。`ParseFrame` 那条入口上限让这件事从适配器走不到，但那是**调用方**守的规矩，而 `Append` 是内核的公开入口 —— **一个只有调用方遵守的上限不是一个上限**：下一个人从另一个入口写一条大记录，代价由整条链付。改法：`Append` 在写之前查 `len(line) > MaxFrameBytes` ⇒ `CodeFrameTooLarge`，理由说清「写进去会让整条链读不回来，而不只是这一条」。★ 这条是**上一轮的测试逼出来的**：我写 `TestTailReturnsTheNewestRecordEvenWhenItAloneIsTooBig` 时用了 2 MiB 的记录，测试回的就是那句 `bufio.Scanner: token too long`。 |
|  | `冷却 30s 内` | `{"open":1, hosts:[{"state":"open"}]}` |
|  | `冷却过去之后` | `{"open":1, hosts:[{"state":"half-open"}]}` |
|  | **同一个 1，两个状态** | 而这两个状态在操作上是**相反的**：`open` 拒绝一切，`half-open` **放一个探针进去** —— 一个读 `open: 1` 以为「不会有东西被尝试」的人，正好读反了 |
改法：三个状态三个计数（`open` / `half_open` / `closed`）+ `hosts_total` + `basis`，并断言三个计数**划分**那些宿主、且与每一行自己的 `state` 对得上（**算出来**的，不是数一行里的字面量）。变异 4/4 |
| **内核自报它能收哪些帧** ★★ | ✅ `ops` 回的就是**分派用的那张表**（`adapters/shell/ops.go` 的 `claims()`）——分派与回答是同一张表，所以不可能漂移；再写一份 op 清单给它报就是**第二份拷贝，而第二份拷贝就是会烂掉的那一份**。★ 这张表立刻暴露一个潜伏缺陷：`handleExecute` 认领**三个**帧类型而只有两个被写下来过，`compose.propose` 可达**仅仅因为它恰好排在 `handleKnowledge` 之前**——**分派的顺序是承重的，而这件事此前没有任何地方写下来**。★ 第二个发现：族认领了前缀、族内未知 op 时处理器回 `false`，帧落到 `default`，而 `default` 说「没有族认领这个前缀」——**那是假话**。两件事两句话：`unknown op <t> in the family <prefix>` vs `unknown frame type <t>: no family claims this prefix`。★ 第三个发现：我那条「第一个族是会话」的断言用 `expect` 查子串，**无论那个族排在第几都通过**——一条「把整张表倒过来」的变异因此活了下来（**第八次**同一种失效） |
| **随时接入** ★★ | ✅ `server.json`（MCP 2025-12-11 的 `ServerDetail` 形状）+ `docs/CLIENTS.md`（唯一的接入说明）+ `.agents/skills/teps/SKILL.md`。★ 照 `rea` 的教义：**技能供说明，服务器供工具，两件事分开**——`SKILL.md` 明说「装了这个技能**不等于**接上了服务器」。★ 一份说「怎么接」但命令跑不起来的文档，与一个「接受了参数却从不读」的字段是同一种失败：**不是拒绝，也不是做到，是看起来做到了**。所以有 `tools/check_clients.py` 核三份文件对同一件事的说法（含 `SKILL.md` 里必须有「does not register」那句） |
| **成对的负控（离线可跑）** ★★ | ✅ `examples/negative-controls/`——**4 对**，每对必过 + 必败，只用 `curl` 与 `python3` 现建内核跑。★ 一条**只在必过那一半成立**的断言不是检查，是形式：它分辨不出任何东西。★ **只有 `failed` 算被驳倒**——`timeout` 是关于时间的事实、`refused` 说明进程根本没起来、`unknown` 是判不了；把三者算作驳倒，就是让一个坏掉的技能靠「跑不起来」拿到双边证据。所以每条自己声明期望哪一种，并因此说清它在证明什么。★ 负控**不是**「换个输入再跑一次看 `outcome`」，它是 `verify_refute` 的一条声明，结果在 `elevation.refutations[].held` 里——第一版读两次 `outcome`，两半拿到同一个数 |
| **模型常驻：加载一次，之后一秒级** ★★★ | ✅ `examples/local-model/resident.py` 连一个已经在跑的 `llama-server`：**加载一次 33 秒**，第一次调用 **4 秒**、第二次 **1 秒**（对照：每问一次起一个进程约 **160 秒**）。★ **它发布不成技能，直到 `env.reaches` 出现**——旧规则里 `env.network` 是一个布尔值，把「连远端主机」（不可复现，该拒）与「连本机套接字」（由自己的字节钉住，可复现）合成了一件。`{socket, artifact}` 就是那条分界线，而这份载荷是它的第一个真实用户。★ **声明与载荷必须说的是同一个套接字**：分开了两个都会「通过」（商店只核清单、载荷只连自己那个地址），所以守卫专门核这一条 |
| **一份真的会说话的技能** ★★★ | ✅ `examples/local-model/`——**端到端**：发布一份会调本机 llama.cpp（Qwen2.5-7B Q3_K_M）的技能 · 内核跑它 · 核验器判 `ok`（**160 秒**）· **必败用例真的被驳倒（`held:true`）** · **提级到 E1**。★ 三条不显然的地方：模型与运行时的路径**钉在载荷的字节里**（于是换机器不可复现，而那是实话，**由哈希链记着不由注释承诺着**）· **你能断言的是一个「受限的」回答，不是一个「好的」回答**（所以提示词被约束成有唯一可查答案的形式）· **它必须有能力失败**。★ 它**不接进 `check_all.sh`**：一次调用约 160 秒，而**一条要跑三分钟的守卫会被从默认路径里拿掉，然后就没有人跑它了**——与「一台跑不了的守卫不是守卫」是同一条纪律的两面 |
| **内容哈希是一个三元组** ★★★ | ✅ **算法 / 版本 / 归一化**。`Get` 用 sha256 重算并比对，而**算法是隐含的**——一个别的 64 位十六进制算法产出的摘要能通过形状检查，重算不符，于是调用方被告知 `recovery_required`「这些字节损坏了」。真话是「本店没有实现那个算法」。**两个不同的事实，一个码**，而第二个把读者送去一个不存在损伤的地方找损伤。现在摘要可以点名自己的算法（裸 64 位十六进制＝sha256，即磁盘与线上的形式），`Get` 与 `Has` 走同一个判定，`GetJSON` 还查**第三个腿**（字节完好但写在另一套归一化下＝同一个值在另一个身份下）。★ 顺着查出 `Resolve` 把**四个**事实压成同一句话：不存在 / 字节损坏 / 算法没有 / 归一化不同——只有第一个意味着「你要的东西不在这里」 |
| **执行的花费落到链上** ★★ | ✅ 准入算出钱、仲裁器取了价，但 resolution 记录里的 `cost` 一直是 `0`——**链在声称每一次执行都是免费的**。现在它带上准入算出的那个数，`arbiter.state` 与 `metrics.get` 都报 `spent`，并有一条断言钉住**两处必须是同一个数**。报一个价不等于付那个价；**付了却不记下来同样是一句假话** |
| **报价回答的是哪个问题** ★★ | ✅ 报价带 `expectation`：`attempt`（一次尝试）或 `success`（成功一次）。同一对报价按两个口径**排出不同的赢家**（A `0.585`/`0.576` vs B `0.650`/`0.550`）——两个数都不是错的，不问清就没法读。`cost` 永远是**真正进了效用函数**的那个数；`cost_per_attempt` 与 `cost_per_success` 都报出来。一个一次也不成功的动作**没有价格**，因为它什么都买不到 |
| **脚本宿主监督（熔断）** ★★ | ✅ **`organs/breaker/`——超时是这一半，熔断是另一半**：进程不按时回来就杀掉（早就有），而**一个连续失败的宿主一直被 spawn**——每次烧掉声明的预算、拿到同一个答案。三态：闭 / 开 / 半开，**半开只放一个探针**（放行全部就是让一个还坏着的宿主在第一次探针时吃下它背后所有技能的全部流量）。**只有宿主失败算数**（没起来 / 没回来）；技能自己判 `failed` 不算——那是技能在如实报告它做不到，是系统在工作 |
| **每次成功花多少钱** ★★ | ✅ 花费与成功率各自都有定义，但**「每次成功花了多少钱」是第三个事实**，而它恰恰是唯一能回答「这件事值不值得做」的那个。`metrics.get` 报 `cost{spent, successes, per_success}`。**分母为零时它没有定义**——一个没有成功的每次成功成本，是一个装扮成数字的除零；那条断言只能放在驱动脚本**最开头**问，因为一旦有东西跑成功过，那个状态就再也回不来了 |
| **提议 → 运行的链接** ★★ | ✅ 提议的**形状就是它的身份**（`planner.ShapeID` = `sha256(op ‖ 孩子…)`），而这个哈希**能从任何已发布的清单重算出来**——不另存一份身份。`accuracy` 因此第一次有定义；能力乘积第一次有值：`0.5714 × 1.0 × 0.5542 = 0.3167` |
| **那个数到底数的是什么** ★★★ | ✅ `accuracy` 原来报 `ok`，读起来是「这条链成功了」。实测：一条链跑一次成功、一次失败，未衰减的均值正好 `0.5`，而衰减（`DefaultDecay = 0.98`）把它压到 **`0.4974`** ⇒ **它不算被计中**。所以 `p.Mean() >= 0.5` 读起来是「至少一半」，行为是「**严格多于一半，再加一点衰减余量**」。改名 `favoured`，并报出 `threshold` 与一段 `basis`（逐字含 `strictly harder`）——**同一批记录里真实的率在 `reliability.mean`，这里报的是一个过了阈值的计数。** ★ 顺带发现那条自洽判据 `value == favoured/observed` 在原来的数据点上**检查不了任何东西**（那里 `favoured == observed == 1`），补了一个 `favoured < observed` 的数据点之后它才能失败。★★ 这与 `armedSchedules` 是同一个形状：**一个读起来像运行时事实的名字，底下是一个别的事实。** |
| **`decay_gap` 里 99% 不是衰减** ★★★ | ✅ 它原来是 `mean − raw_rate`，而 `mean` 是**按行不加权**的衰减后平均、`raw_rate` 是**按观测加权**的成功率——那个差因此把三件事混在一起：① 衰减 ② 加权方式 ③ `Beta(1,1)` 先验（均值 0.5）把低观测数的行拉向中间。而名字只说 decay。**实测**（一个技能观测 1 次成功、另一个观测 9 次失败）：`mean 0.3762` · `mean_undecayed 0.3788` · `raw_rate 0.1` ⇒ **旧 `decay_gap` = +0.2762**（正的，而衰减只会把一行拉向先验），**新 `decay_gap` = −0.0026**。现在它是**同一批记录折两次之差**，`raw_rate` 保留为自己的一个数——**三个量、三个名字** |
| **乘积不告诉你它被谁压着** ★★★ | ✅ `capability.value` 乘的是 `reliability.mean`，而它上面那条注释写着「对一个产品来说重要的是**最弱的一环**：一条链和它最差的一步一样可靠，而平均值恰好掩盖了那件事」——**注释和代码对同一个数说了相反的话**。★ 实测两个候选因子相差 **1.6 倍**（`mean 0.5445` vs `weakest 0.3311`）。★ 但**错的是注释，不是代码**：`weakest` 是**整张后验表所有行的最小值**，不是某条链的某一步——**表不是一条链**，组合那条教义在这里不成立；而且一行只观测一次且失败时 `mean ≈ 0.1`，把它当因子会让**一次坏观测支配整个乘积**。⇒ 注释改成说清它是「最差的那一行」（一个诊断），并新报 **`capability.weakest_factor` / `weakest_name`**——**乘积自己的最弱因子**，那才是那条注释真正想说的东西（三个因子确实是一条链：覆盖率 → 准确率 → 可靠性）。★ 并列时按**排好序的名字**取，不按 map 顺序：**一个会飘的数不是一个读数** |
| **文档里的数字没有任何守卫在管** ★★★ | ✅ `check_docs.py` 的前三条管的是「**做没做**」，**没有一条管数字**——而一个写在文档里的数在代码往前走之后不会自己变红。★ 加了规则 4 之后实测：**8 条「N 个测试」里有 6 条是旧的**（`composer` 说 21 实测 25 · `memory` 说 15 实测 20 · `pool` 说 15 实测 16 · `reflex` 说 16 实测 17 · `planner` 说 15 实测 16 · `executor` 说 21 实测 56）。★ 判据是**可算的**：那个包真的跑一遍数 `=== RUN`，不查表——**一张「器官 → 测试数」的对照表就是被检查之物的第二份拷贝，而第二份拷贝就是会烂掉的那一份**。★ 一个数必须**点名**它数的是什么，而且只能点名一个；配不起来时报错，不猜。`docs/CONTRACT.md` 里一个历史数（当时全仓库 249）改成不写成数：**写不成判据的数，就不该写成数。** |
| **两个层级共用一个词** ★★★ | ✅ `accuracy` 那一块的字段名此前不说它数的是**链**还是**运行**。同一个响应里还有 `reliability.observations`（**运行**的条数），两个名字差一个字母、隔着几行、数的东西差两个层级——**实测同一次调用：链计数 `1`，运行条数 `82`**。★ 还有第二处同形状：`chains` 列的是**全部被追踪的**链（含一次都没跑过的），而 `value` 的分母是**跑过的**链 ⇒ **同一个对象里用 `favoured/observed` 与用 `favoured/len(chains)` 能算出两个不同的数，而没有任何一处说该用哪个**。⇒ 把层级写进名字：`chains_traced` / `chains_run` / `chains_favoured`，并在 `basis` 里逐字写明「**counts chains, not runs**」。★ 顺带加了一条判据：**一份清单与它自己的计数必须对得上**（`len(chains_traced) == traced`）——清单比计数长是正常的（含未跑过的），但清单与它声称的那个计数必须相等 |

### 证据（可复跑）

```sh
bash tools/check_all.sh            # ★ 九台守卫 + 测试 + 守卫自检，一条命令
bash tools/drive.sh                # 503/503，全程只有 curl
bash tools/drive_mcp.sh            # 21/21，全程只有 printf 的 JSON-RPC
```

`check_all.sh` 里的八台，单独跑也行：

```sh
bash tools/check_format.sh         # gofmt 干净
python3 tools/check_kernel.py      # 四条内核守卫
python3 tools/check_contract.py    # 文档与代码的拒绝码不许漂移
python3 tools/check_adapters.py    # 翻译器不许 import 内核
python3 tools/check_frames.py      # 帧类型与契约必须一一对上
python3 tools/check_organs.py      # 器官与两份文档的状态表必须一一对上
python3 tools/check_docs.py        # ★ 文档里四条：做没做三条 + 「N 个测试」必须与实测一致
python3 tools/check_assertions.py  # ★ 存在性判据不许冒充一致性判据
python3 tools/check_nomodel.py
python3 tools/check_clients.py     # 接入面：三份文件对同一件事的说法
python3 tools/check_examples.py    # 成对的负控：每一对两半，两半判定必须不同
```

★ **为什么要有 `check_all.sh`**：守卫此前只有一份**清单**，而清单要靠人一条一条敲。
少敲一条，那台守卫就静悄悄地不再守了——**而它的输出看起来和「全部通过」一样，
因为它根本没有输出。** 一个只在有人记得时才跑的检查，报不出「没有人记得」。

`check_all.sh` 里还有一项**守卫自检**：注入一处违规，`check_nomodel.py` 必须变红。
**一台永远绿的守卫与一台从来没跑过的守卫，输出一模一样。**

守卫读数：**516 / 3000 行 · 0 / 0 依赖 · 6 / 12 公开类型 · 0 / 0 领域词**。

**上面全部在本地跑，不需要网络。** 另有一条**不是守卫**、只用于核对外部说法的工具：

```sh
python3 tools/check_provenance.py  # ⚠️ 要联网，约 5 分钟；核对 PROVENANCE 里的名字
```

它不进守卫清单，理由是：**一台跑不了的守卫不是守卫。** 一条只有在网络通的时候
才给答案的检查，不能承担「这个仓库现在是不是对的」这件事。

内核只有六个公开类型：`Frame` `Refusal` `Log` `Entry` `Budget` `Account`。
`Account` 是**唯一持有可变值**的东西，理由是"检查与扣减必须是不可分割的一步"；
其余全部是无状态或只追加的。

### P4 的证伪点（这条才证明"系统自己发现"是性质而不是声称）

```sh
bash tools/drive.sh   # 找那一行：技能腐烂后第 N 次失败起被自动弃用
```

一个 30 次成功（后验 0.9996）的技能，在没有人通知仲裁器的情况下，
**第 10 次失败起就不再被选中**——单元测试里的界是 17 次（阈值 25）。

关键不是"后验会下降"，而是**下降得多快由遗忘因子决定，而遗忘因子是显式参数**。
负对照测出了这一点：25 次失败后，无遗忘的均值还有 **0.8855**，有遗忘的只剩 **0.5992**；
500 次观测后无遗忘的计数是 **502**，有遗忘的是 **50.0**。

另一条同样重要：**上界永远高于均值**。没有探索项，一个被误判的技能就再也回不来——
一次坏补丁会永久杀死一个好技能，因为再也没人调它。测试里它从 0.023 恢复到了 0.645。

**`drive.sh` 验的是判据本身，不是功能。** 它断言：序号由服务端分配、客户端自供
`seq`/`rev`/`head` 一律 `client_authority`、同键跨重启仍幂等、**被拒的请求一条都没写进链**、
链头跨重启一致、预算全有或全无、未知类型只拒绝不断连接；**非法状态迁移被拒且不动链**、
**会话跨重启从日志里重放出来**；技能店的**内容寻址**、**许可证闭包**、
**E0 不许固化**、**同内容重发布幂等**、**被拒时 CAS 里零 blob**；记忆的
**写不是编辑**、**E0 写得下但默认检索不到**、**两个时钟各管一段**（t=115 还没学到、t=135 已知且旧值已走）；
合成器的**年表段不许被重排**、**近重复进 dropped 而不是消失**、**装不下的长候选不许挡住后面的短候选**。

单元测试另有五条 `drive.sh` 做不到的：

- **篡改中间一条记录后重开，链必须拒绝**（`recovery_required`）——追加式哈希链的价值全在这一条上。
- **CAS 里被改过的 blob，读的时候必须被拒绝**——「一个无法担保自己返回的字节的存储不是内容寻址，只是内容贴标」。
- **许可证闭包沿依赖传播**：先放宽策略注册一个 GPL 依赖，再用默认策略注册依赖它的 MIT 技能 → `license_conflict`。
- **未知证据等级不许被折成 E0**——E0 是真实等级，把 `"E9"` 折成它是把打字错误变成信任决定。
- **SimHash 半径必须真的分开两个总体**（测试打印 `radius 12 separates near=6 from far=32`，让漂移可见）。

### 五条守卫各自被注入测试证过

**`tools/check_frames.py`（帧守卫）** —— 它第一次运行就抓到 12 处不一致：8 个前缀族
文档里根本没提，4 个帧文档在承诺而没人实现。它防的是本项目第四次付过的那笔学费：
我新加的 `compose.build` 与 `organs/composer` 抢了同一个帧名，composer 段七条断言全红。
**撞名的特别之处在于两边都能跑**，只有把两边放在一起看才知道撞了。

而它自己的三处漏洞也全是注入测试逼出来的：

| 注入 | 第一版 | 修法 |
|---|---|---|
| 分派一个没写进契约的帧 | ✗ 漏网 | 第一版让「前缀族被文档提过」替它下面每个帧背书 |
| 删掉一个已文档化帧的分派 | ✗ 漏网 | 反方向同样漏；两个方向都改成**按全名核对** |
| 把 `reflex.` 换成 `reflex2.` | ✗ 漏网 | `PREFIX_RE` 只认 `[a-z]+`，守卫**看不见带数字的前缀** |

契约表格里的缩写（`observe` `match` `state`）也因此换成了全名。
**帧名是全局命名空间，在一张本该权威的表里缩写它们，正是守卫要防的那种含糊。**

| 守卫 | 它防什么 | 注入测试 |
|---|---|---|
| `check_kernel.py` | 领域词 / 宿主词泄漏进内核 | `type skillRef struct{ TotalGain int }` → 2 处报警 |
| `check_contract.py` | 文档与代码的拒绝码漂移 | 改一个 ✅、删一行、给常量改名 → 三种全被抓住 |
| `check_format.sh` | 混进 diff 的几百行重排 | 第一次运行就判出 `core/refusal.go` 与 `adapters/shell/session.go` 是脏的 |
| `organs/session` 的穷尽测试 | 状态机漏边、阈值漂移 | 10 个变异（删边、改阈值、改比较符）全部被抓住 |
| `store` 的双门一致性测试 | 同一事实被两道门判成两个码 | 把 `Validate` 改回 `bad_request` → `one fact, two codes` |
| `organs/arbiter` 的证伪点 | 「系统自己发现」变成一句声称 | 8 个变异（去掉遗忘、去掉探索、去掉 margin、毒化概率）全部被抓住 |
| `organs/executor` 的四条纪律 | 执行者自证、判不了默认成功、无时限也放行 | 4 个变异全部被抓住（各 1–6 条 FAIL） |
| `organs/reflex` 的 `Fires` | 一个词两个意思：字段数观测、函数答谓词 | 4 个变异（`Weaken` 无条件加一 / `Reinforce` 无条件加一 / 判据取削弱之后的强度 / `extinguished` 丢掉「现在不答」那一半）全部被抓住 |
| `organs/session` 的浪费率 | 一个比例没有分母：`waste: 0` 是 0/1 还是 0/50 | 4 个变异（不报分母 / `Flags()` 不带计数 / 只数「中间没有 Grant 的子集」 / 把分子当比例）全部被抓住 |
| `adapters/shell` 的分派表 | 表声明了一个谁都认不出的族，两台守卫都说验过了 | 4 个变异（幽灵族 / 指错了人 / 不带点的名字指错人 / 表里整条删掉一个族）全部被抓住——★ 最后一条静态守卫看不见，**运行时守卫 FAIL=14 抓得住** |
| `adapters/shell` 的 `audit.tail` | 一个窗口不报自己的窗口 | 5 个变异（不报 `limit` / 不报 `returned` / `truncated` 恒真 / `returned` 报成请求的 limit / 两端对调）全部被抓住 |
| `adapters/shell` 的熔断表 | 一个叫 `open` 的数数的是「不是闭的」：`open + half-open` | 4 个变异（`open` 数「不是闭的」 / `half_open` 把闭着的也算进去 / `hosts_total` 报成 trip 条数 / `open` 与 `half_open` 对调）全部被抓住 |
| `organs/pool` 的 `Cheapest` | 一个把容量说成预算的拒绝：会自己变好的条件报成了不会 | 4 个变异（容量挡着时也回 `budget_exhausted` / 用对了码但不点明等多久 / 登记表空着也走预算分支 / 两个都挡着时只说一个原因）全部被抓住 |
| `adapters/shell` 的 `escalation.decide` | 只算了一次价，而记录把它挂在了另一个主体上 | 4 个变异（记录的 `skill` 回到请求 / 记录的 `task_class` 回到请求 / 删掉 skill 一致性检查 / 删掉 task_class 一致性检查）全部被抓住 |
| 证据阶梯的未知等级 | 把 `ok` 检查了，然后把未知照样折到 E0 | 4 个变异（未知折回 E0 / 拒绝时说成「最弱」而不是「读不懂」 / `evidenceRank` 又丢掉 `ok` / 天花板上的比较符从 `>` 变成 `>=`）全部被抓住——★ 最后一条是替换上去的：原来那条「天花板读不懂时不再拒」**不可达**，一条不改变答案的变异报「漏网」报的是变异没生效，不是守卫没抓住 |
| 证据阶梯的读法 | 同一份前向器写了三遍，而两句注释都说只有一扇门 | 4 个变异（`elevateCompose` 又丢掉 `ok` / 读不懂的上限不再被拒 / 比较符从 `>=` 变成 `>` / 守卫不剥注释了）全部被抓住 |
| `adapters/shell` 的 `compose.propose` | 搜索的分母不在回答里：`catalog` 是登记表条数，不是参与搜索的技能数 | 4 个变异（不报 `searched` / `searched` 报成登记表条数 / `untyped_count` 报成 0 / `catalog` 与 `searched` 对调）全部被抓住（go 测试与 `drive.sh` 各有一半） |
| `organs/composer` 的用量 | 总数是一个读法，它的分项是另一个 | 4 个变异（不累计分项 / 分项全塞在同一个键上 / 分项被后一段覆盖 / 适配器不报分项）全部被抓住 |
| `adapters/shell` 的 `distill.apply` | `fires: true` 旁边放着一句「它现在还不会回答」 | 4 个变异（`note` 退回常量 / `note` 的两支对调 / 不报阈值 / 阈值报成另一个数）全部被抓住 |
| `core` 的帧上限 | 一个只约束一个方向的上限不是一个上限：`Tail` 只受条数约束 · `Append` 不查记录大小 | 5 个变异（字节预算退回「不设」 / 字节预算连第一条也卡 / `Append` 不查记录大小 / 窗口预算超过帧上限 / 不把窗口翻回链序）全部被抓住 |
| `core` 的 epoch 与拒绝码表 | 一个会倒退的计数器不是一个计数器 · 一个没有生产点的拒绝码是一个承诺 | 4 个变异（epoch 的解析错误又被丢掉 / 空 epoch 文件被当成损坏 / 契约里 `stale_epoch` 的缺口标记被删掉 / 一个码的生产点被删掉）全部被抓住 |
| `organs` 的导出函数 | 一个没有调用点的导出函数是一个承诺；器官里那份判据是死的，适配器里那份是活的 | 4 条（`arbiterFix` 又自己判一次 / 删掉一个 `DEAD-API:` 标记 / 守卫把定义文件整个排除 / 守卫不剥注释）全部达到预期 —— ★ 后两条的判据是**反的**：守卫**保持绿**才算「洞被演示出来」，因为它们测的是守卫会不会放过一个它本该抓的孤儿 |
| `check_docs.py` 的规则 3（原 `check_organs.py` 的方向五）| 「✅ 已做」在证据消失之后不会自己变红 | **判据不能是「守卫变红」**——把一条检查删掉，它当然不会变红。判据是：删掉这条检查 **并且** 注入它本该抓住的那个违规 ⇒ 守卫**漏网**。漏网证明了它是唯一抓住它的东西。3/3 是行为证据（有它则红、删它则漏） |

★★ **「把一条检查删掉」这一类变异的判据**：不能是「守卫变红」。
**一个只在失败时说话的检查，与一个被删掉的检查，输出完全一样。**
所以判据要配成一对：**删掉这条检查 + 注入它本该抓住的违规 ⇒ 漏网**。
第一版变异脚本正是栽在这里（把「检查被我禁掉所以沉默」读成「漏网」），
而第一版的「整段删掉」还删错了地方（只改了注释、循环照跑，
于是报出「删掉之后仍然抓住」——**一个没被真正禁用的检查，与一个被禁用的检查，也会报出相反的话**）。

★ **`check_contract.py` 是在一次真实漂移之后写的**：`drive.sh` 断言 `unknown_session`，
内核返回 `no_session`，文档也写着 `unknown_session`——**两边都没有报错**。
它第一次诚实运行就抓出两处真冲突：`stale_epoch` 与 `recovery_required`
在文档里是 ✅，而它们**不可能靠原样重发恢复**。

★ **同一个教训在 P2 里又出现了一次，这次是在代码内部**：`Manifest.Validate` 对未知许可证
返回 `bad_request`，而 `SolveLicense` 对同一事实返回 `license_conflict`。两道门、一个事实、
两个码——客户端的重试循环会在一条路径上工作、另一条上不工作。修法是让两边都调用
**同一个构造点**（`store/license.go` 的 `UnknownLicenseRefusal`），并加一条测试钉住
"一个事实只有一个码"。

### 工具链：自包含，且可重建

本机 **`sudo` 不可用**——沙箱设了 `no new privileges`，`/usr/local` 只读，
`/var/cache/apt` 属主是 `nobody:nogroup`。⇒ **系统级安装这条路是死的。**

所以工具链落在项目内，且**版本与 sha256 钉死**：

```sh
tools/install_go.sh      # 重建（幂等：已是同版本则跳过）
tools/go build ./...     # 用包装器调用，它负责设好 GOROOT/GOPATH/GOCACHE
```

**为什么必须有包装器**：`$HOME` 也是只读的，而 Go 默认把 `GOCACHE` 与 `GOPATH`
放在 `$HOME` 下——不重定向的话 `go build` **直接失败**。
三者全部钉在 `.toolchain/` 内，工具链因此完全自包含、可随项目整体搬走。

已装：**go1.27.1 linux/amd64**（sha256 `63d339f0…8445`，282 MB，`.toolchain/` 已 gitignore）。
`build` / `vet` / `test` 均已实测通过。

## 语言：Go（已定案）

决定性理由是**交叉编译**：

- Rust 交叉编译 Windows 需要 mingw/xwin；**交叉编译 macOS 实际不可能**（需 macOS 主机 + SDK）；
- Go `GOOS=windows go build` 一条命令。

而这个系统明确要求三平台原生能力（UIAutomation / macOS Accessibility / Linux AT-SPI2）。

**已实测，不是声称**：在 Linux 上直接产出
`PE32+ executable for MS Windows` 与 `Mach-O 64-bit arm64 executable`；
`windows/amd64`、`darwin/amd64`、`darwin/arm64`、`linux/arm64` 四个目标全部通过。
`go list -m all` 只有它自己 —— **零外部依赖**。

**硬纪律：`go.mod` 的 `require` 必须为空。** 只用标准库——
框架、序列化、哈希、Ed25519、HTTP、进程控制全在 stdlib 里够用。
（`tools/check_kernel.py` 的 Gate 2 守这一条，`replace` 也算违规。）

## 与宿主的关系：借契约，不借代码库

DSH 与 Inimerse 各有一个形态相近的权威层，它们的**协议形状值得吸收**：

- 服务端分配序号，客户端永远不许提供；
- 幂等键必填，空串被拒；
- 拒绝是结构化返回值，不是异常；
- 能力协商**不静默降级**；
- 追加式哈希链日志 + 提交锚；
- **失败语义也是契约**（哈希不匹配是 `recovery_required`，从不是 `ok`）。

但**不引入它们的进程、类型或生命周期**。理由：把别人的抽象变成自己的地基，
就是复制"依赖 DSH"那一类错误——**而这一次，那个别人自己都在去 C 化、换类型系统。**
