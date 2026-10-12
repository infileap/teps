# 第二批 2026 系统名的核对结果（本地复现）

由 `tools/check_provenance.py` 生成。**它不走搜索 API**——
`web_search` 在本环境返回 `HTTP 402`，所以它直接问 GitHub 仓库搜索与 arXiv 的公开接口。

**怎么读这张表：**

| 记号 | 意思 |
|---|---|
| `SAME-NAME` | 有一个**名字完全相同**的东西存在 |
| `NEAR-NAME` | 有命中，但**没有一个名字相同** |
| `NONE` | 一个命中都没有 |
| `ERROR` | **没查成**——不是「没有」 |

## ⚠️ 这两列量的是「同名」，不是「核实」

**`SAME-NAME` 只说明有个东西叫这个名字。** 第一次跑这份检查时，
33 个名字里有 14 个拿到 `SAME-NAME`，而那些东西分别是：

`DS-LoRA` → 一块 LoRaWAN 开发板 · `SLoRA` → 一个纳米孔测序工具 ·
`HELM` → Kubernetes 的包管理器 · `Lotus` → Filecoin 协议 ·
`SuperBake` → 一个 CakePHP 仓库 · `EDGE` → 一个 .NET/Node.js 运行时 ·
`SOLA` → Solana 区块链 · `ESSA` → 一个博客系统 · `ASCENT` → 一个 HPC 可视化运行时 ·
`AGNN` → 一个视频目标分割网络 · `DualPath` → 2017 年的 Keras 网络 ·
`MMP` → mmpose · `RB-LoRA` → 一个 ComfyUI 的 LoRA 键名转换器 ·
`SPEE` → speech-to-speech

**只按名字核对，这 14 个全都会「通过」。** 所以这张表里唯一有信息量的列是
最后一列——**那个东西自己怎么描述自己**。判定词是索引，不是结论。

第一批的教训逐字是「真实的名字 + 真实的论文 + 不属于它的指标」。
这一批是它的反面：**真实的名字 + 真实的项目 + 完全不是那件事。**

| 名字 | GitHub | arXiv | 找到的东西自己怎么说 |
|---|---|---|---|
| `AION Hard Bypass` | NONE | NONE |  |
| `ASCENT` | SAME-NAME | SAME-NAME | Alpine-DAV/ascent — A flyweight in situ visualization and analysis runtime for multi-physics HPC simulations |
| `MOPD` | SAME-NAME | SAME-NAME | BytedTsinghua-SIA/Open-MOPD — Open-MOPD: Diagnosing and Fixing Capability Imbalance in Multi-Teacher On-Policy Distillation |
| `AGNN` | SAME-NAME | SAME-NAME | carrierlxk/AGNN — Zero-shot Video Object Segmentation via Attentive Graph Neural Networks (ICCV2019 Oral) |
| `MMP` | NEAR-NAME | SAME-NAME | open-mmlab/mmpose — OpenMMLab Pose Estimation Toolbox and Benchmark. |
| `SlimInfer` | SAME-NAME | SAME-NAME | Longxmas/SlimInfer — [AAAI 2026] SlimInfer: Accelerating Long-Context LLM Inference via Dynamic Token Pruning |
| `DualPath` | SAME-NAME | SAME-NAME | titu1994/Keras-DualPathNetworks — Dual Path Networks for Keras 2.0+ |
| `KnapSpec` | SAME-NAME | SAME-NAME | kaist-flexml-lab/knapspec — （无描述） |
| `SPEE` | NEAR-NAME | NEAR-NAME | huggingface/speech-to-speech — Build voice agents with open-source models |
| `EDGE` | SAME-NAME | SAME-NAME | tjanczuk/edge — Run .NET and Node.js code in-process on Windows, MacOS, and Linux |
| `Lotus` | SAME-NAME | SAME-NAME | filecoin-project/lotus — Reference implementation of the Filecoin protocol, written in Go |
| `OxyGent` | SAME-NAME | SAME-NAME | jd-opensource/OxyGent — [ACL 2026] OxyGent: Making Multi-Agent Systems Modular, Observable, and Evolvable via Oxy Abstraction https://arxiv.org/abs/2604.25602 |
| `JoyAgent` | SAME-NAME | SAME-NAME | jd-opensource/joyagent-jdgenie — 开源的端到端产品级通用智能体 |
| `Infinite-Parameter LLM` | NONE | SAME-NAME | Infinite-Parameter LLMs: Generating and Adapting Weights from Live Data |
| `SOLA` | NEAR-NAME | SAME-NAME | solana-labs/solana — Web-Scale Blockchain for fast, secure, scalable, decentralized apps and marketplaces. |
| `Parameter Updater Experts` | NEAR-NAME | NONE | madmax0404/parameter-updater-experts-paper — （无描述） |
| `ESSA` | NEAR-NAME | SAME-NAME | essay-org/essay — personalise blog system |
| `In-Place TTT` | SAME-NAME | NEAR-NAME | ByteDance-Seed/In-Place-TTT — （无描述） |
| `决策论 TTT` | NONE | NONE |  |
| `TTT-Discover` | NEAR-NAME | NEAR-NAME | cheongalc/nanodiscover — Reimplementation of TTT-Discover without the Tinker dependency |
| `PaRSP` | NEAR-NAME | NONE | omid3098/Parspeak — （无描述） |
| `RectLoRA` | NONE | NONE |  |
| `SLoRA` | NEAR-NAME | SAME-NAME | BonsonW/slorado — A simplified version of Dorado built on top of S/BLOW5 format. |
| `Meta-UCF` | NEAR-NAME | NONE | HUBId/UCF-meta — （无描述） |
| `DS-LoRA` | NEAR-NAME | NONE | schroepfer/CubeCell-LoraWan-DS18B20 — Heltec CubeCell HTCC-AB01 Dev-Board for LoraWan TTN V3 with DS18B20. Arduino. |
| `RB-LoRA` | SAME-NAME | NONE | RamonGuthrie/ComfyUI-RBG-LoraConverter — A Powerful LoRA key converter for ComfyUI |
| `Titans Fast-Weight Neural Memory Adapter` | NONE | NONE |  |
| `δ-mem` | NEAR-NAME | SAME-NAME | elimaine/delta-mem-mlx-sidecar — OpenAI-compatible MLX sidecar for δ-mem adapter experiments, runnable directly or behind OpenClaw. |
| `SuperBake` | SAME-NAME | NONE | el-cms/superBake — superBake repository for CakePHP 2.3.x |
| `Kimi K3` | SAME-NAME | SAME-NAME | MoonshotAI/Kimi-K3 — Open Frontier Intelligence |
| `Nemotron 3` | NEAR-NAME | SAME-NAME | NVIDIA/workbench-example-nemotron-finetune — An NVIDIA AI Workbench example project for fine-tuning a Nemotron-3 8B model |
| `Titans++` | NEAR-NAME | SAME-NAME | lucidrains/titans-pytorch — Unofficial implementation of Titans, SOTA memory for transformers, in Pytorch |
| `HELM` | SAME-NAME | SAME-NAME | helm/helm — The Kubernetes Package Manager |

## 计数

- 查了 **33** 个名字
- 至少一侧 `SAME-NAME`（**同名，不是核实**）：**23**
- 两侧都只有 `NEAR-NAME` 或更弱：**10**
- 两侧都 `NONE`：**4**

**这批计数不改变本项目的任何一个决定**——它们只是把「未核实」换成
「在某一天、用这两个索引、得到过这样的结果」。一个名字在这张表里是
`SAME-NAME`，说明**有个东西叫这个名字**；它做了什么，这张表没问，
**而且这个名字相同的概率与「它就是那件事」毫无关系**。
