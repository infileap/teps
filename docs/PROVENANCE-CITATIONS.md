# 第一批：那份文档引用的 arXiv 编号，实际上是什么

由 `tools/check_provenance.py --citations` 生成，**不走搜索 API**。

| 编号 | 被引用为 | 它实际上是什么 |
|---|---|---|
| `2605.03190` | Spartacus-1B / Causal Monoid State Compression | VDCores: Resource Decoupled Programming and Execution for Asynchronous GPU |
| `2605.05699` | （同一节的 KV Cache 压缩依据） | When Quantization Is Free: An int4 KV Cache That Outruns fp16 on Apple Silicon |
| `2601.10402` | （第一批其余引用） | Toward Ultra-Long-Horizon Agentic Science: Cognitive Accumulation for Machine Learning Engineering |
| `2603.07770` | — | ArcLight: A Lightweight LLM Inference Architecture for Many-Core CPUs |
| `2603.10342` | — | AgentServe: Algorithm-System Co-Design for Efficient Agentic AI Serving on a Consumer-Grade GPU |
| `2603.18030` | — | Quine: Realizing LLM Agents as Native POSIX Processes |
| `2607.14144` | — | The Capability Convergence Hypothesis: Capability from Access Structure, Not Scale |
| `2607.17181` | — | Talaria: Session-Aware Serverless Serving of Hundred-Billion-Parameter LLMs |
| `2608.15383` | — | Every Expert Counts: ExactMoE for Memory-Efficient W4A16 Inference |
| `2608.20622` | — | Applying Anthropic Primitives at Large Enterprises: Harness Paradigm for Knowledge Work |
| `2608.23953` | — | The Empire, Long Divided, Must Unite: Architectural Convergence in Three LLM Agent Harnesses |
| `2609.00006` | — | Harness Engineering: Anatomy, Architecture, and Evolution of Coding Agents -- A Source-Code Study of Eleven Systems |
| `2609.02292` | — | SCX Router: Streaming Zero-Shot Model Selection with a Decoder-KV Classifier and a Real-World Task Ontology |
| `2609.13486` | — | Mixture-of-Experts Language Models Can Be Strong and Efficient Retrievers |

## 探针本身

- control ok: 1706.03762 = Attention Is All You Need; 2303.08774 = GPT-4 Technical Report
- ★ the batch said totalResults=14 and returned 10 entries; 4 ids were resolved one by one rather than recorded as missing

## 怎么读这张表

**编号能解析 ≠ 引用是对的。** 这张表回答的是「那个编号是哪篇论文」，
而它足以回答那个真正的问题：**被引用为 A 的编号，是不是 A？**

反过来也一样：**编号解析不到 ≠ 那篇论文不存在。** arXiv 的接口会静默截断
（`totalResults` 说 14、返回 10 条），所以没解析到的编号是**逐个补查**过的；
补查仍拿不到，才记成没解析到。而且有一组**对照编号**先跑——
**一个对已知存在的编号也回「没有」的探针是坏的，它报出来的东西和「不存在」一模一样。**
