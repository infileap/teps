#!/usr/bin/env python3
"""一份会说话的技能载荷，而模型是**常驻**的。

它与 `main.py` 的区别只有一处：`main.py` 每问一次就起一个 `llama-cli`，
于是每问一次都要把 3.8 GB 重读一遍（实测约 93 秒的加载，一次调用共约 160 秒）。
这里它连一个**已经在跑**的 `llama-server`，加载只发生一次。

实测（本机，Qwen2.5-7B Q3_K_M）：

    llama-server 就绪        35 秒（这一次加载）
    第一次请求                4 秒
    第二次请求                1 秒
    第三次请求                0 秒
    对照：每问一次起一个进程   160 秒

**这一份载荷在 `env.reaches` 出现之前发布不成技能。** 旧规则里 `env.network` 是
一个布尔值，而它把两件不同的事合成了一件：

  - 连**远端主机**：答案取决于它今天说什么 ⇒ 不可复现，该拒
  - 连**本机套接字**：它背后的东西是本机状态的一部分，**由自己的字节钉住** ⇒ 可复现，该放行

`env.reaches` 就是那条分界线，而这份载荷是它的第一个真实用户。
"""
import json
import sys
import urllib.request

# ★ 套接字与「它背后是什么」都钉在载荷的字节里，和 main.py 钉模型路径是同一个理由：
# 载荷的字节就是技能的身份。`artifact` 是那个 .gguf 的 sha256 —— 它是**声明**，
# 商店核不了它（那个文件在商店之外），但它必须给，而且它把「本机」从一个形容词
# 变成了一条可以复核的句子。
SOCKET = "http://127.0.0.1:18911"
ARTIFACT = "a96b16179dc6cc9afdf0cf7a96a80c199cbd00b9be207c3465be21cb721cca5e"


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

    req = urllib.request.Request(
        SOCKET + "/v1/chat/completions",
        data=json.dumps({
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": n,
            "temperature": 0,
        }).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            doc = json.loads(resp.read().decode("utf-8"))
    except Exception as exc:
        # 说清是哪一件事失败了：套接字连不上与模型答错了，是两条不同的处置。
        print("could not reach the resident model at %s: %s" % (SOCKET, exc), file=sys.stderr)
        print("start it with: .toolchain/llama/llama-server -m <model.gguf> "
              "--host 127.0.0.1 --port 18911", file=sys.stderr)
        return 4

    answer = doc["choices"][0]["message"]["content"].strip()
    # 只留最后一非空行：模型有时会先自言自语一句再给答案。
    lines = [l for l in answer.splitlines() if l.strip()]
    answer = lines[-1] if lines else ""

    print(json.dumps({"answer": answer}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
