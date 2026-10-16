#!/usr/bin/env python3
"""自由判据的 AI 那一半：**把模型删掉，整个流程还能跑吗？**

内核有一条判据叫「删掉 DSH，curl 能不能驱动全流程」——它把「不依赖宿主」
从一句主张变成一条可执行的检查。这个脚本是同一件事用在另一个因子上的版本：

    **删掉模型，整个流程还能跑吗？**

它扫项目自己的 Go 文件，断言里面**没有任何一处会调用模型**：
没有厂商名、没有 API key、没有 Authorization 头、没有出站 HTTP、
没有从环境里读配置。`net/http` 只许用作**内核自己的传输**（POST /k），
`exec.Command` 只许用在执行器里（那是技能，不是模型）。

## 为什么这是一条守卫而不是一条记录

「内核不依赖模型」如果只写在文档里，它就会在某次「顺手加一个 embedding 调用」
里悄悄变成假的，而没有任何东西会红。这正是本项目已经付过学费的形状：
一个事实写在文档里，代码里没有它。

## 它必须能失败

一台永远绿的守卫不是守卫。用 `--self-test` 注入一处违规，它必须报出来。
"""
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# 一、出站：任何一处都算违规。
OUTBOUND = [
    (r'\bopenai\b', "OpenAI"),
    (r'\banthropic\b', "Anthropic"),
    (r'\bdeepseek\b', "DeepSeek"),
    (r'\bollama\b', "Ollama"),
    (r'\bchat/completions\b', "chat completions 端点"),
    (r'\bembeddings?\b\s*\(', "embedding 调用"),
    (r'api[_-]?key', "API key"),
    (r'\bAuthorization\b', "Authorization 头"),
    (r'\bBearer\b', "Bearer token"),
    (r'\bos\.Getenv\b', "从环境里读配置"),
    (r'\bhttp\.Get\b', "出站 GET"),
    (r'\bhttp\.Post\b', "出站 POST"),
    (r'\bhttp\.Do\b', "出站请求"),
    (r'\bhttp\.NewRequest\b', "构造出站请求"),
    (r'\bnet\.Dial\b', "拨号"),
]

# 二、`net/http` 只许以这些形式出现：内核自己的服务端。
ALLOWED_HTTP = [
    r'\bnet/http\b',
    r'http\.NewServeMux\b',
    r'http\.Serve\b',
    r'http\.Error\b',
    r'http\.Status[A-Za-z]+\b',
    r'http\.MethodPost\b',
    r'http\.ResponseWriter\b',
    r'http\.Request\b',
    r'http\.Handler\b',
    r'http\.Server\b',
]


# ★ 边界是一个决定，不是一件事后观察到的巧合。
#
# 这条规则守的是「**引擎不依赖模型**」，不是「这个仓库里任何 Go 文件都不许
# 提到模型」。两件事今天恰好落在同一批文件上，所以第一版写成 `git ls-files
# '*.go'` 也能过——而它过掉的是**另一条规则**，只是那条规则今天还没有反例。
#
# 反例迟早会来，而且会是**正当**的：这个项目的论点就是「模型是一个技能，
# 不是依赖」。于是一个真的模型载荷**应该**可以调模型，而它多半是一个脚本。
# 按扩展名划界，会一边放它过去（`.sh` 不被扫），一边把同样正当的 Go 载荷
# 拒掉（`.go` 被扫）——**两个方向都错，而错误的样子都是「通过」或「红」。**
#
# 所以边界按**角色**划：引擎是 `core/ organs/ adapters/ store/ evidence/`，
# 内容不是。
ENGINE_DIRS = ("core/", "organs/", "adapters/", "store/", "evidence/")
CONTENT_DIRS = ("examples/", ".agents/", "tools/", "docs/")


def engine_files():
    """引擎自己的 Go 文件（不含 .toolchain，不含测试）。

    用 `git ls-files` 而不是遍历目录：未跟踪的文件不在这个仓库的承诺里，
    而守卫守的是承诺。这和本仓库其它守卫是同一条纪律。
    """
    out = subprocess.run(
        ["git", "ls-files", "*.go"], cwd=ROOT, capture_output=True, text=True
    ).stdout.split()
    return [
        f for f in out
        if f.startswith(ENGINE_DIRS) and not f.endswith("_test.go")
    ]


def scanned_summary(files):
    """★ 一个数要带分母。

    「0 处模型调用」如果不说清扫了什么，读的人分不出这两种情况：
    整个仓库干净，还是**这台守卫只看了它恰好选中的那几个文件**。
    本项目的度量那一条早就写过：一个没有分母的数不是一个很小的数，
    它是一个不存在的数。
    """
    dirs = sorted({f.split("/")[0] + "/" for f in files})
    skipped = [d for d in CONTENT_DIRS if not any(f.startswith(d) for f in files)]
    return ("扫了 %d 个引擎文件（%s）；没扫 %s —— 那些是内容，不是引擎"
            % (len(files), " ".join(dirs), " ".join(skipped)))


def scan(text):
    hits = []
    for line_no, line in enumerate(text.splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith("//"):
            continue
        for pat, label in OUTBOUND:
            if re.search(pat, line):
                hits.append((line_no, label, line.strip()[:110]))
    return hits


def http_uses(text):
    """`net/http` 的用法，去掉白名单之后剩下的。"""
    left = []
    for line_no, line in enumerate(text.splitlines(), 1):
        if "http." not in line:
            continue
        stripped = line.strip()
        if stripped.startswith("//"):
            continue
        residual = line
        for pat in ALLOWED_HTTP:
            residual = re.sub(pat, "", residual)
        if re.search(r'\bhttp\.[A-Za-z]', residual):
            left.append((line_no, line.strip()[:110]))
    return left


def boundary_selftest():
    """证明边界是它说的那条，而不是「今天恰好没反例」。

    两侧都要有：引擎里的模型调用**必须**被选中；同一段文本放在内容目录里
    **必须**不被选中。只测一侧的边界与没有边界在观察上一样。

    ★ 它**报出自己跑了几条探针**，而不是只在失败时说话。

    为什么：一个只在失败时说话的检查，与一个**被删掉的**检查，输出完全一样
    ——两者都什么都不打印。本项目在 `check_all.sh` 里已经为同一件事栽过一次
    （「一个只验第一台的自检循环会让第二台的自检永远不被跑，而它坏掉的样子与
    「通过」一模一样：它什么都不打印」）。所以这里的计数是承重的：
    删掉任一侧的探针，这个数会变小。
    """
    out = []
    probe = 'import "github.com/sashabaranov/go-openai"'
    in_engine = "core/probe.go" in _select(["core/probe.go"])
    in_content = "examples/probe.go" in _select(["examples/probe.go"])
    probes = 0
    probes += 1
    if not in_engine:
        out.append("SELF-TEST  引擎里的文件没被选中：那这条守卫什么都没守")
    probes += 1
    if in_content:
        out.append("SELF-TEST  内容目录里的文件被选中了："
                   "那一个正当的模型载荷会被当成依赖拒掉")
    probes += 1
    if not scan(probe):
        out.append("SELF-TEST  引擎里的一处模型调用没被抓到")
    print(f"自检：边界 {probes} 条探针（引擎侧必须选中、内容侧必须不选中、违规必须被抓到）。")
    return out


def _select(paths):
    return [f for f in paths
            if f.startswith(ENGINE_DIRS) and not f.endswith("_test.go")]


def main():
    files = engine_files()
    if not files:
        print("没有扫到任何引擎文件：通过是平凡的真，不构成证据。")
        return 1

    problems = []
    for f in files:
        text = open(os.path.join(ROOT, f), encoding="utf-8").read()
        for line_no, label, line in scan(text):
            problems.append(f"{f}:{line_no}  {label}  → {line}")
        for line_no, line in http_uses(text):
            problems.append(f"{f}:{line_no}  出站 HTTP（net/http 只许做内核自己的传输）  → {line}")

    if os.environ.get("TEPS_NOMODEL_SELF_TEST"):
        # ★ 一台永远绿的守卫不是守卫。
        #
        # 第一版的自检只是 `problems.append("注入的违规")` —— 它证明的是
        # 「这个列表非空时会红」，而那件事不需要证明。真正要证明的是
        # **每一条正则都还认得出它要认的东西**：一条写坏的正则会让守卫
        # 悄悄变成一台永远绿的机器，而它的输出看起来和「真的很干净」一样。
        #
        # 所以自检把**每一条模式各喂一个样本**，逐条核对它被抓到。
        samples = [
            ("import \"github.com/sashabaranov/go-openai\"", "OpenAI"),
            ("client := anthropic.NewClient()", "Anthropic"),
            ("url := \"https://api.deepseek.com/v1/chat/completions\"", "DeepSeek"),
            ("m := ollama.New()", "Ollama"),
            ("resp, _ := http.Post(endpoint, \"application/json\", body)", "出站 POST"),
            ("req, _ := http.NewRequest(\"POST\", u, nil)", "构造出站请求"),
            ("key := os.Getenv(\"TEPS_API_KEY\")", "从环境里读配置"),
            ("h.Set(\"Authorization\", \"Bearer \"+tok)", "Authorization 头"),
            ("vec := embeddings(text)", "embedding 调用"),
            ("c, _ := net.Dial(\"tcp\", host)", "拨号"),
        ]
        missed = []
        for text, label in samples:
            got = {lab for _, lab, _ in scan(text)}
            if label not in got:
                missed.append((label, text))
        if missed:
            for label, text in missed:
                problems.append(f"SELF-TEST  这条模式认不出「{label}」了  → {text}")
        # 白名单也要能失败：一个不在名单里的 http.* 必须被抓到。
        if not http_uses("resp, _ := http.Head(u)"):
            problems.append("SELF-TEST  http_uses 放过了白名单之外的 http.Head")
        # 而名单内的用法不许被抓到，否则守卫会靠喊狼来了变得没用。
        for ok_line in ("mux := http.NewServeMux()", "http.Error(w, \"x\", http.StatusBadRequest)",
                        "if r.Method != http.MethodPost {", "func (s *server) httpHandler(w http.ResponseWriter, r *http.Request) {"):
            if http_uses(ok_line):
                problems.append(f"SELF-TEST  白名单内的用法被误报了  → {ok_line}")
        print(f"SELF-TEST OK  自检：{len(samples)} 条模式样本 + 白名单两侧。")
        # ★ 而自检自己第一版只做了上面这一半：它证明了**每一条模式还认得出**
        # 它要认的东西，却没有走一遍**失败路径**——于是它的退出码永远是 0，
        # 而 `check_all.sh` 的自检项读到「注入违规之后它仍然说通过」。
        #
        # 两半都要有：模式那一半证明「它还看得见」，这一半证明「看见了会红」。
        problems += boundary_selftest()
        problems.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")

    print(scanned_summary(files))
    if problems:
        print("\n这些地方会调用模型、或者会出站：")
        for p in problems:
            print("  " + p)
        print("\n「删掉模型整个流程还能跑」现在不是真的了。")
        return 1

    print("模型调用 0 处 · API key 0 处 · 环境读取 0 处 · 出站请求 0 处。")
    print("net/http 只用于内核自己的 POST /k 传输。")
    print("\n★ 「AI 可换」不是一句主张：**这个仓库里没有模型可换。**")
    return 0


if __name__ == "__main__":
    sys.exit(main())
