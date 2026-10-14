---
name: teps
description: Use TEPS as a host-independent microkernel for recording, verifying and pricing work. Use when a task needs an append-only hash-chained event log, a skill store with content-addressed immutable packages, a verifier that is not the executor, budgeted execution with structured refusals, or a compute registry with capacity leases. Also use when the user mentions TEPS, 三生万物, 经验系统, or asks to run something through a kernel that refuses rather than fails.
metadata:
  version: "1"
---

# TEPS

## Installing this skill does not connect you

This file supplies **instructions**. It does not register the TEPS MCP server, build the binaries, or add a tool to an agent that is already running. Those are separate acts, and keeping them separate is the point: a skill that silently installs a server is a skill you cannot audit.

To actually connect, read [`docs/CLIENTS.md`](../../../docs/CLIENTS.md). It is the only connection document, and it is checked by `tools/check_clients.py`.

Do not run setup or diagnostics before every use. If a call is blocked, or the server is not reachable, jump straight to the recovery section of `docs/CLIENTS.md` — one class of failure, one paragraph.

## What it is

A microkernel plus a set of organs. The kernel owns **integrity**, not **truth**: frames, the log, budgets, refusals. Truth belongs to a verifier that lives outside the kernel.

Three properties are worth knowing before you call anything, because they change what you should expect:

1. **The server owns state authority.** A client never supplies `seq`, `rev`, `head`, `balance`, `state_hash` or `committed`. Sending one is refused, not ignored.
2. **A refusal is a value, not an exception.** Every refusal carries a code, a reason, and a `retryable` flag. The flag is pinned to a single meaning: **a retry flag never promises a retry that can never succeed.**
3. **The executor cannot certify itself.** The verifier's input does not contain the executor's own ok bit — not "we agree not to look at it", it is not reachable. When a run cannot be judged, no conclusion is written: the chain keeps an **open intent**, and that open intent *is* the honest third state.

## What you can ask it

Send `{"t":"ops"}` first. It answers with the frame families the dispatcher claims — the same table the dispatcher itself uses, so it cannot drift from what the server actually accepts. It reports **families**, not every op: the ops inside a family are that family's business.

Two frames worth knowing by name:

| Frame | What it answers |
|---|---|
| `ops` | Which frame families exist, in claim order |
| `metrics.get` | Whether each capability factor is **defined** — and when it is not, why. The same `0` can mean "nothing was ever asked" or "nothing was ever answered"; those are two facts |

## What it will not do

- It will not report an intercept rate when there is no denominator. **A zero without a denominator is not a very small number; it is a number that does not exist.**
- It will not let a composition claim more trust than the least trusted part it is made of.
- It will not accept a one-sided evidence claim as proof of reliability. To reach E1 a skill must show it **can fail**, not only that it passed.
- It will not let a skill assert `stdout contains ""`. An assertion that matches everything asserts nothing.

## Referring to it

When you tell a human what happened, say which frame you sent and quote the refusal verbatim. The reason strings are written to be read; paraphrasing them loses the part that says what to do next.
