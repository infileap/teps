# 选型证据的可信度（为什么模型层不能作为选型依据）

**本文档记录一次证据审查的结论，以及由它导出的硬规则。**

---

## 一、发现

来源文档（一份 2946 行的设计对话）在"模型层"引用了三个 1B 级方案。
逐条核对引用后发现：**关键指标是嫁接的。**

| 声称 | 归属的引用 | 那个引用的实际内容 |
|---|---|---|
| Spartacus-1B / Causal Monoid State Compression | 某天文台 ezproxy 链接 | 标题是 *VDCores: Resource Decoupled Programming and Execution for Asynchronous GPUs* |
| Zebra-Llama，KV Cache 压到 2.73% | IEEE 11240722 | 那篇是 *LEAP: LLM Inference on Scalable PIM-NoC Architecture* |
| 若干 arXiv `2605.*` / `2607.*` / `2609.*` | — | 编号无法核实 |

**Zebra-Llama 本身是真实工作**（NeurIPS 2025）——**问题在于 2.73% 这个数字是接上去的。**
"真实的名字 + 真实的论文 + 不属于它的指标"是最难发现的一类错误：
名字对得上，于是没人再查数字。

---

## 二、导出的硬规则

> **凡未在本地复现的模型能力，一律按假设处理，不进 Core Memory。**

**为什么这条规则是必须的，而不是谨慎：**

1. **选型依据被污染。** 如果拿"KV Cache 压到 2.73%"当依据去设计会话路由，
   而那个数字不存在，**整个路由设计就建在空气上**。
2. **这类错误不会被使用发现。** 它不报错，只是让系统比预期差——
   而差的原因会被归到"参数没调好"上，从而永远查不到根因。
3. **它是可传染的。** 一旦进了 Core Memory，后续所有推理都会引用它，
   而 Core Memory 是**不可变**的（只能 supersede）——**污染会变成永久事实。**

---

## 三、它对项目就绪度的影响

「三生万物」的 AI 因子因此被分成两半：

| | 状态 | 依据 |
|---|---|---|
| **本地那半**（自研 1B / 边缘推理） | 🔴 **悬空** | 上述引用不可信，无本地复现 |
| **外部那半**（L3 API） | 🟢 真实可用 | 可以直接调用并实测 |

⇒ **启动顺序据此确定：AI 先租（API），不必等一个 1B 模型。**

**这不是退让，是把一个不可验证的前提换成可验证的前提。**
外部那半今天就能跑出真实数字；本地那半需要先复现，才配进入设计依据。

---

## 四、当前状态

| 项 | 状态 |
|---|---|
| 引用核对 | ✅ 已完成（结论如上） |
| 硬规则 | ✅ 已定 |
| **本地复现** | ⬜ **未做——这是把 AI 因子从 🔴 变 🟢 的唯一途径** |
| 第二批名字的核对 | ✅ 已做（`tools/check_provenance.py` → `PROVENANCE-CHECK.md`） |

⚠️ 本文档的核对结论**引用了外部链接的标题**，而不是逐篇精读全文。
若要把某个模型正式纳入选型，**必须先本地复现，而不是先补引用。**

---

## 第二批：一份新设计文档里的 2026 系统名（**未核实**）

同一份对话的后续推演（用户提供，见本项目会话记录）在「2026 最新突破」与
「权重更新难」两节里列了 **30 多个系统名**：

AION Hard Bypass · ASCENT · MOPD · AGNN · MMP · SlimInfer · DualPath · KnapSpec ·
SPEE · EDGE · Lotus · OxyGent · JoyAgent · Infinite-Parameter LLM · SOLA ·
Parameter Updater Experts · ESSA · In-Place TTT · 决策论 TTT · TTT-Discover ·
PaRSP · RectLoRA · SLoRA · Meta-UCF · DS-LoRA · RB-LoRA ·
Titans Fast-Weight Neural Memory Adapter · δ-mem · SuperBake
（另提 Kimi K3 · Nemotron 3 · Titans++ · HELM）

**核对状态：已核对（2026-10-12）。** `web_search` 仍然返回 `HTTP 402 Insufficient Balance`，
但**`curl` 出得去**，而 GitHub 与 arXiv 都有公开、稳定、可复现的接口。
所以这一批不再走搜索 API：`tools/check_provenance.py` 直接问这两个索引，
结果在 [`PROVENANCE-CHECK.md`](PROVENANCE-CHECK.md)。**33 个名字，0 个没查成。**

### ★ 结果比「未核实」更糟：同名不是证据

**两个数，必须分开说：**

| 读数 | 值 | 谁算的 |
|---|---|---|
| 至少一侧 `SAME-NAME`（有一个名字完全相同的东西） | **23 / 33** | **工具** |
| 其中最高命中是**一个完全不同的东西** | **14** | **我**（判断，不是计数） |

★ 第一版我把这两个数混成了「14 个有名字完全相同的仓库」——
**那正是这份文档在记录的错：一个判定声称的比它量到的多。**
23 是索引给的，14 是我读描述读出来的，**它们必须长得不一样。**

那 14 个是：

| 名字 | 匹配到的东西 |
|---|---|
| `DS-LoRA` | 一块 **LoRaWAN 开发板**（Arduino） |
| `SLoRA` | `slorado` —— 一个**纳米孔测序**工具 |
| `HELM` | **Kubernetes** 的包管理器 |
| `Lotus` | **Filecoin** 协议 |
| `SuperBake` | 一个 **CakePHP** 仓库 |
| `EDGE` | 一个 **.NET/Node.js** 进程内运行时 |
| `SOLA` | **Solana** 区块链 |
| `ESSA` | 一个博客系统 |
| `ASCENT` | 一个 **HPC 可视化**运行时 |
| `AGNN` | 一个视频目标分割网络 |
| `DualPath` | 2017 年的 Keras 网络 |
| `MMP` · `RB-LoRA` · `SPEE` | mmpose · 一个 ComfyUI 的 LoRA 键名转换器 · speech-to-speech |

**只按名字核对，这 14 个全都会「通过」。**

这是第一批那个坑的**反面**：

- 第一批：**真实的名字 + 真实的论文 + 不属于它的指标**
- 第二批：**真实的名字 + 真实的项目 + 完全不是那件事**

两次都指向同一件事：**「存在」和「是它」是两个判据，而按名字核对只能回答前者。**

### 另一头：确实对得上的那些

`SlimInfer`（AAAI 2026，长上下文 LLM 推理的动态 token 剪枝）·
`MOPD`（多教师 on-policy 蒸馏）· `OxyGent`（ACL 2026，多智能体）·
`JoyAgent`（京东开源通用智能体）· `In-Place TTT`（ByteDance-Seed）·
`KnapSpec`（kaist-flexml-lab）· `Infinite-Parameter LLMs`（arXiv）·
`Kimi K3`（MoonshotAI）· `Nemotron 3` · `Titans++` · `δ-mem` ·
`Parameter Updater Experts`。

**这批名字里至少有一半是真东西。** 但「真东西」和「那份文档说的那件事」仍然是两个判据——
`SlimInfer` 的标题是「通过动态 token 剪枝加速长上下文 LLM 推理」，
而那份文档把它放在哪一节、声称它做了什么，这张表没有问。

**形态与第一批相同**：具体到可以引用的名字 + 一个出处都没有。这不是说它们一定是假的，
而是说**「未核实」和「已核实」必须长得不一样**——否则下一次有人引用这份清单时，
没人知道它从来没被核对过。

**另有一条独立的判断**：即使这些系统全部为真，这两节也**不改变本项目的任何一个决定**。
它们的价值是背景，不是依据。
