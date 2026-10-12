# 第一批点名的两个模型：它们有没有指称对象

由 `tools/check_provenance.py --models` 生成。**这不是核对论文说了什么，
更不是本地复现那个能力。** 它只回答两个问题：这个名字在索引里有没有对应物，
以及它引的那个数**是不是它自己的**。

| 文档怎么叫它 | GitHub 仓库命中 | 出处可达 | 出处标题 |
|---|---|---|---|
| `Spartacus-1B-Instruct`<br>首选：因果幺半群状态压缩，1.3B | `total_count: 0` | ✅ | `[2605.03190] VDCores: Resource Decoupled Programming and Execution for Asynchronous GPU` |
| `Causal Monoid State Compression`<br>（同一个首选模型的机制名） | `total_count: 0` | ❌ | — |
| `Zebra-Llama`<br>备选：SSM + MLA，**1B 的 KV Cache 压到 2.73%** | `total_count: 4` | ✅ | `Zebra-Llama: Towards Extremely Efficient Hybrid Models` |

## ★ 那句被引的话，重新抓一次

**Zebra-Llama** 的出处里，

> down to 3.9%, 2%, and 2.73% of the original for the 1B, 3B, and 8B variants

**重新抓取后这句话仍在。**

