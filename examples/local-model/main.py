#!/usr/bin/env python3
"""一份真的会说话的技能载荷。

它不是桩：它起本机的 llama.cpp，把提示词送进去，把模型的回答打到标准输出。
它存在是因为本项目的设计规则把「一个模型」定义成了一个**本地程序**——
`store/manifest.go` 拒收 `env.network: true`，理由是**可复现性**：一个行为取决于
远端今天说了什么的技能不是可复现的，而它的内容哈希仍然会声称它是。

三条不显然的地方：

1. **模型与运行时都是绝对路径，而且那是刻意的。** 载荷的字节就是技能的身份，
   所以路径必须钉在字节里。它们指向本机（`/mnt/d/models/...` 与仓库里的
   `.toolchain/llama/`），于是这份载荷**换一台机器就不可复现**——那是实话，
   而且它由哈希链记录着，不是由注释承诺着。

2. **一个自由发挥的回答没法断言。** 所以提示词被约束成有唯一可查的答案
   （「Reply with exactly: PONG」），断言查的就是那串字符。这是唯一诚实的做法：
   你能断言的是一个**受限的**回答，不是一个**好的**回答。

3. **它必须有能力失败。** 输入里带 `"refute": true` 时它故意退出 3。
   没有这一条，一个永远返回成功的载荷和一份正确的声明长得一模一样
   ——那正是 `verify_refute` 存在的理由。
"""
import json
import os
import subprocess
import sys

LLAMA = "/home/sakiko/inimerse/.teps/.toolchain/llama/llama-cli"
MODEL = "/mnt/d/models/qwen2.5-7b-instruct-q3_k_m.gguf"


def main() -> int:
    try:
        body = json.load(sys.stdin)
    except Exception as exc:
        print("input is not JSON: %s" % exc, file=sys.stderr)
        return 2

    # 必败用例：一个有能力失败的载荷才谈得上被验证过。
    if body.get("refute"):
        print("REFUSED-INPUT", file=sys.stderr)
        return 3

    prompt = body.get("prompt", "")
    if not prompt:
        print("input has no prompt", file=sys.stderr)
        return 2
    n = int(body.get("n", 16))

    for path, what in ((LLAMA, "the llama.cpp runtime"), (MODEL, "the model file")):
        if not os.path.exists(path):
            # 说清是哪一个不在、以及怎么装 —— 一句「跑不起来」不是可执行的信息。
            print("%s is not at %s: run tools/install_llama.sh" % (what, path), file=sys.stderr)
            return 4

    proc = subprocess.run(
        [LLAMA, "-m", MODEL, "-p", prompt, "-n", str(n), "-st", "--simple-io"],
        capture_output=True,
        text=True,
        timeout=600,
    )
    if proc.returncode != 0:
        print("llama-cli exited %d" % proc.returncode, file=sys.stderr)
        return 5

    # llama-cli 把横幅、提示词、回答和统计行全打在标准输出上。
    # 回答是 `> <提示词>` 那一行之后、`[ Prompt:` 那一行之前的东西。
    text = proc.stdout
    marker = "> " + prompt
    i = text.find(marker)
    if i < 0:
        print("llama-cli did not echo the prompt: the output shape changed", file=sys.stderr)
        return 6
    rest = text[i + len(marker):]
    j = rest.find("[ Prompt:")
    if j < 0:
        print("llama-cli printed no stats line: the output shape changed", file=sys.stderr)
        return 6
    answer = rest[:j].strip()

    # 只留最后一非空行：模型有时会先自言自语一句再给答案。
    lines = [l for l in answer.splitlines() if l.strip()]
    answer = lines[-1] if lines else ""

    print(json.dumps({"answer": answer}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
