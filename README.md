# 三生 —— 微内核 + 外设

> **名称是暂定代号**，取自本项目宗旨「三生万物：AI × 算力 × Skill」。
> 按项目的命名纪律，正式名字由项目所有者定。

一个**宿主无关**的微内核 + 外设群：分布式、神经符号混合、可持续学习。

它**不依赖** DSH、Cordis、Inimerse 或任何宿主。**这不只是设计目标，是可执行的判据**：

> **删掉 DSH、删掉 Inimerse 之后，一段 curl NDJSON 脚本仍能驱动全流程。**

---

## 这个目录为什么在这里（以及为什么该尽早搬走）

本会话的文件沙箱**只允许写 `/home/sakiko/inimerse`**，而工作区外是只读的。
所以它被放在工作区内的一个**被本地排除**的目录里：

- 已写入 `.git/info/exclude` —— **只对本机生效，不进版本库**；
- 外层仓库的 `git status` 在 `main` 上保持干净（实测：外层只剩 headagent 自己的改动）；
- Inimerse 的门禁（`tools/check_*.py`）枚举 `git ls-files`，**看不见未跟踪路径**，
  所以这个目录不可能把它们的 CI 弄红。

**它是一个自带 `.git` 的独立仓库**，提取只需一步：

```sh
mv .sansheng ~/sansheng
```

**这件事应该尽早做。** 把它的历史挂在 Inimerse 的仓库分支下，
正是"自由判据"要防的那种耦合——不是代码依赖，而是**身份与历史的依赖**。

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
| 算力 | 🟡 有点子无实现 | 注册表还没有 | 
| Skill | 🟢 设计完整 | 本仓库的 ABI 与技能店规格 |
| **AI** | 🔴 本地那半悬空 | 引用的 1B 模型论文里，关键指标是嫁接的（见 `docs/PROVENANCE.md`）|

但 **AI 的外部那半（L3 API）真实可用**。

⇒ **启动顺序：AI 先租（API）· 算力用几台开发机 · Skill 自己造（唯一真正的活）。**
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
            check_format.sh                      gofmt 干净，别让 diff 里混进重排
            drive.sh                             ★ 自由判据：只有 curl 驱动全流程
            drive_mcp.sh                         ★ 自由判据：只有 printf 的 JSON-RPC
            go · install_go.sh                   自包含工具链：包装器 + 重建
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
| 四条内核守卫 + 契约守卫 + 格式守卫 + 适配器守卫 | ✅ **可跑，已验**（领域词泄漏、文档漂移、格式漂移、适配器边界各有注入测试） |
| **Go 工具链** | ✅ **go1.27.1 已装**（项目内自包含、sha256 校验、可重建） |
| **P0 内核** | ✅ **四概念已写成类型**（459 行 / 6 公开类型 / 领域词 0） |
| **P0 shell 适配器** | ✅ **stdio + HTTP，curl 可驱动** |
| **P1 会话状态机 + 停滞守卫** | ✅ **`organs/session/`，纯函数，20 个测试** |
| **P1 会话落在链上** | ✅ **重启后从日志重放；适配器内存里没有真相** |
| **P2 技能店：内容寻址** | ✅ **`store/cas.go`——读时重算哈希，不符即拒** |
| **P2 技能店：许可证闭包** | ✅ **`store/license.go`——闭包等级 = 成员最大值** |
| **P2 技能店：注册流水线** | ✅ **`store/register.go`——八步，`ref` 最后才动** |
| **P2 技能店：可变的一半** | ✅ **`store/refs.go`——先写历史再移指针** |
| **P2 组合代数** | ✅ **`store/compose.go`——可靠性是乘积；剪枝要报告丢掉了什么** |
| **自由判据** | ✅ **`tools/drive.sh` 152 项断言全过，只用 curl** |
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
| **执行器 + 独立核验器** | ✅ **`organs/executor/` 纯函数 + `adapters/shell/execute.go` 脏半边；21 个测试，4 个变异全被抓住** |
| **执行者不许自证** | ✅ **`Judge` 的入参里没有「成功了吗」这个位置；判不了就不写 resolution** |
| **证据只由真实执行提升** | ✅ **E0→E1，且不碰 `Promote` 的 E0 闸门** |
| **组合（执行图）** | ✅ **`organs/executor/compose.go`——serial/parallel 算成执行图；另外四个算子各自说明缺什么；9 个变异全被抓住** |
| **组合接进适配器** | ✅ **`skill.compose` 按图 spawn、串行步的 stdout 接下一步的 stdin；16 项断言，4 个变异全被抓住** |
| **组合跑通了** ★ | ✅ 第一次由 curl 跑出**未经人手编写**的执行：两个孩子都没有的行为（`CHAINED`）出现了。⏳ 组合**本身仍不可注册**（没有自己的 ref），所以还不能递归组合——可靠性只能按**形状**分组 |

### 证据（七条命令，可复跑）

```sh
tools/go test ./...                # 八个包，179 个测试
bash tools/drive.sh                # 152/152，全程只有 curl
bash tools/drive_mcp.sh            # 21/21，全程只有 printf 的 JSON-RPC
bash tools/check_format.sh         # gofmt 干净
python3 tools/check_kernel.py      # 四条内核守卫
python3 tools/check_contract.py    # 文档与代码的拒绝码不许漂移
python3 tools/check_adapters.py    # 翻译器不许 import 内核
```

守卫读数：**459 / 3000 行 · 0 / 0 依赖 · 6 / 12 公开类型 · 0 / 0 领域词**。

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

### 四条守卫各自被注入测试证过

| 守卫 | 它防什么 | 注入测试 |
|---|---|---|
| `check_kernel.py` | 领域词 / 宿主词泄漏进内核 | `type skillRef struct{ TotalGain int }` → 2 处报警 |
| `check_contract.py` | 文档与代码的拒绝码漂移 | 改一个 ✅、删一行、给常量改名 → 三种全被抓住 |
| `check_format.sh` | 混进 diff 的几百行重排 | 第一次运行就判出 `core/refusal.go` 与 `adapters/shell/session.go` 是脏的 |
| `organs/session` 的穷尽测试 | 状态机漏边、阈值漂移 | 10 个变异（删边、改阈值、改比较符）全部被抓住 |
| `store` 的双门一致性测试 | 同一事实被两道门判成两个码 | 把 `Validate` 改回 `bad_request` → `one fact, two codes` |
| `organs/arbiter` 的证伪点 | 「系统自己发现」变成一句声称 | 8 个变异（去掉遗忘、去掉探索、去掉 margin、毒化概率）全部被抓住 |
| `organs/executor` 的四条纪律 | 执行者自证、判不了默认成功、无时限也放行 | 4 个变异全部被抓住（各 1–6 条 FAIL） |

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
