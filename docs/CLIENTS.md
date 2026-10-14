# 接入 TEPS —— 从一台什么都没装的机器开始

这份文档是**唯一的接入说明**。`server.json` 说的是「这个服务器是什么」，这里说的是「怎么把它跑起来并接上」——**两件事，两份文件**，因为一份清单同时干两件事时，两件事会各自漂移。

## 0. 它是什么，不是什么

TEPS 是一个**宿主无关的微内核**加一组外设（器官）。内核只保证**完整性**：帧、日志、预算、拒绝。它**不**保证真值——真值归一个内核之外的核验器。

它对客户端的形状是：

| 面 | 传输 | 说什么 |
|---|---|---|
| `adapters/mcp` | **stdio**（JSON-RPC 2.0） | 一个工具 `kernel`，参数 `{t, key, body}`——**内核的帧原样透传** |
| `adapters/shell` | stdio **或** `POST /k`（NDJSON） | 内核自己的协议，不是 MCP |

★ **MCP 那一面只翻译信封，不翻译概念。** 一个 MCP 客户端拿到的 `t` 就是内核的帧类型；`ops` 那个帧会告诉你能收哪些族。

## 1. 装

仓库**不含** Go 工具链（282MB，`.toolchain/` 在 `.gitignore` 里）。先把它装进仓库内：

```sh
tools/install_go.sh          # 自包含 go1.27.1 linux/amd64，落在 .toolchain/
```

★ `$HOME` 与 `/usr/local` 只读的机器上，`tools/go` 包装器负责把 `GOROOT`/`GOPATH`/`GOCACHE` 全部指进 `.toolchain/`——**不这么做 `go build` 直接失败**，而失败信息不会告诉你原因。

## 2. 建

```sh
tools/go build -o .out/teps-shell ./adapters/shell
tools/go build -o .out/teps-mcp   ./adapters/mcp
```

两个二进制，不是一个。`teps-mcp` 是翻译器；它**自己不带内核**，而是 spawn 一个 `teps-shell`：

```sh
.out/teps-mcp -child .out/teps-shell -dir .out/chain
```

| 旗标 | 意思 |
|---|---|
| `-child` | 内核参考传输二进制的路径。**必填**：没有它翻译器没有内核可翻 |
| `-dir` | 链目录。第一次跑会自动建 |

`teps-shell` 自己另有旗标（接 `POST /k` 时用）：

| 旗标 | 默认 | 意思 |
|---|---|---|
| `-dir` | 必填 | 链目录 |
| `-store` | `<dir>/skills` | 技能店目录 |
| `-http` | 空 | 监听地址，如 `127.0.0.1:0`；**非空时打印 `LISTEN <addr>` 到 stderr** |
| `-ms` / `-tokens` / `-money` | 60000 / 1000000 / 1000 | 初始预算 |

## 3. 接

### 裸 JSON-RPC（任何客户端都能这么试）

stdio 上一条一行。`initialize` → `tools/list` → `tools/call`：

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kernel","arguments":{"t":"ops"}}}' \
| .out/teps-mcp -child .out/teps-shell -dir .out/chain
```

`ops` 会回**分派用的那张表**（族 + 说明），所以客户端不用读源码就知道能发什么。

### MCP 客户端配置（`mcpServers` 形状）

```json
{
  "mcpServers": {
    "teps": {
      "command": "/absolute/path/to/.out/teps-mcp",
      "args": ["-child", "/absolute/path/to/.out/teps-shell", "-dir", "/absolute/path/to/.out/chain"]
    }
  }
}
```

★ **用绝对路径。** `teps-mcp` 把 `-child` 交给 `exec.Command`，而一个不含斜杠的名字会拿**服务端自己**的 PATH 去找——那不是你 shell 的 PATH。

### 只想要内核自己的协议

```sh
.out/teps-shell -dir .out/chain -http 127.0.0.1:8787
# 另一处：
curl -sS --data-binary '{"t":"ops","v":1}' -H 'Content-Type: application/x-ndjson' http://127.0.0.1:8787/k
```

这条路径**不需要任何 SDK、任何语言运行时**——`tools/drive.sh` 的 478 条断言全程只用 `curl`。

## 4. 接不上时：一类失败一段

按症状找，不要从第 0 节重读。

### `-child` 没给或指错

翻译器启动后**不报错**，只是每条 `tools/call` 都失败。先手工跑一遍 `-child` 指的那个二进制：

```sh
.out/teps-shell -dir .out/chain </dev/null
```

它应该立刻回一行 welcome。回不来的话，问题在二进制，不在翻译器。

### `tools/install_go.sh` 之后 `go build` 仍然说找不到

用 `tools/go`，不要用系统的 `go`：

```sh
tools/go env GOROOT GOPATH GOCACHE     # 三个都应在仓库内
```

### `chain is not readable` / `recovery_required`

链是**追加式哈希链**，中间一条被改动过就会拒绝打开——这是设计，不是损坏。**不要删目录重来**：先把它留证据地挪开（`mv .out/chain .out/chain.broken`），因为那条链上可能有你唯一的一份运行记录。

### `unknown op <t> in the family <prefix>`

族**在**，族里没有这个 op。这与

```
unknown frame type <t>: no family claims this prefix
```

是**两句不同的话**：前者说前缀有人认领、op 不存在，后者说没有任何族认领这个前缀。先发 `{"t":"ops"}` 看族清单。

### `budget_exhausted`

内核有一本预算，`-ms` / `-tokens` / `-money` 是它的初始额度。**读**操作不花钱（`ops`/`status`/`audit.tail`/`metrics.get`）；**跑技能**花。加大初始额度重开即可，链本身不受影响。

### `circuit_open`

一个宿主（解释器）连续起不来，熔断器把它关了一会。理由里写着还剩多少毫秒。**这不是技能失败**——技能自己判 `failed` 不计入熔断，因为那是技能在如实报告它做不到。

## 5. 这份文档自己也要能被检查

`tools/check_clients.py` 会核对：

- `server.json` 的必填字段与 `packages[]` 的必填字段（按 MCP 2025-12-11 schema）
- `server.json` 里 `_meta.launch` 提到的每个**路径**在这个仓库里真的存在
- 这份文档里每个反引号里的**仓库相对路径**真的存在

★ 一份说「怎么接」但命令跑不起来的文档，与一个「接受了参数却从不读」的字段是同一种失败：**不是拒绝，也不是做到，是看起来做到了。**
