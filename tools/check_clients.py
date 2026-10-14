#!/usr/bin/env python3
"""check_clients.py —— 接入面的一致性守卫。

它核的是**三份文件对同一件事的说法**，而不是任何一份文件好不好看：

  1. `server.json` 的形状（按 MCP 2025-12-11 的 ServerDetail / Package 必填字段）
  2. `server.json` 的 `_meta.launch` 里提到的每个路径，在这个仓库里真的存在
  3. `docs/CLIENTS.md` 里每个反引号里的**仓库相对路径**真的存在
  4. `.agents/skills/teps/SKILL.md` 存在，front-matter 的 `name` 与目录名一致

★ 为什么这是守卫而不是文档检查：
一份说「怎么接」但命令跑不起来的文档，与一个「接受了参数却从不读」的字段
是同一种失败 —— **不是拒绝，也不是做到，是看起来做到了**。
读这份文档的人不会知道该怀疑哪一句。

自检：TEPS_CLIENTS_SELF_TEST=1 时，先证明它还看得见（每类各喂一个样本），
再证明看见了会红（注入一个违规，它必须退出 1）。**两半都要有。**
"""
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SERVER = os.path.join(ROOT, "server.json")
CLIENTS = os.path.join(ROOT, "docs", "CLIENTS.md")
SKILL = os.path.join(ROOT, ".agents", "skills", "teps", "SKILL.md")

# MCP 2025-12-11 的必填字段。少一个，注册表读不懂这份声明。
SERVER_REQUIRED = ["name", "description", "version"]
PACKAGE_REQUIRED = ["registryType", "identifier", "transport"]
TRANSPORT_TYPES = ["stdio", "streamable-http", "sse"]


def problems_for_server(doc):
    out = []
    for k in SERVER_REQUIRED:
        if not doc.get(k):
            out.append("server.json 缺必填字段 " + k)
    pkgs = doc.get("packages")
    if pkgs is None:
        out.append("server.json 没有 packages：一个也没有的话，这份声明对注册表没有意义")
    elif not isinstance(pkgs, list) or not pkgs:
        out.append("server.json 的 packages 必须是至少一项的数组")
    else:
        for i, p in enumerate(pkgs):
            for k in PACKAGE_REQUIRED:
                if not p.get(k):
                    out.append("server.json packages[%d] 缺必填字段 %s" % (i, k))
            t = (p.get("transport") or {}).get("type")
            if t and t not in TRANSPORT_TYPES:
                out.append("server.json packages[%d].transport.type 不是已知传输：%s" % (i, t))
    return out


# launch 里出现的、看起来像仓库相对路径的 token。
# 只认这些前缀，否则 `-child`、`-dir` 这类旗标名会被当成路径。
PATH_PREFIXES = ("./", ".out/", "tools/", "adapters/", "core/", "organs/",
                 "store/", "docs/", "evidence/", ".agents/")


def paths_in(text):
    found = set()
    for tok in re.findall(r"[A-Za-z0-9_./-]+", text):
        if tok.startswith(PATH_PREFIXES):
            found.add(tok.rstrip(".,;:"))
    return found


def problems_for_paths(label, text, lenient=True):
    """★ 只报「看起来是文件、却不在仓库里」的 token。

    这里的难点是反引号里的东西**不区分路径与代码**：`tools/install_go.sh` 是路径，
    而 `tools/call` 是 MCP 的方法名，两者的前缀一模一样。
    判据：一个 token 是候选路径，当且仅当它**存在**，或者它的最后一段带扩展名
    （有 `.`）。于是 `tools/call` 被跳过，`adapters/nope/missing.go` 被抓到。

    ★ 散文用**宽松**判据（存在，或最后一段带扩展名），因为反引号里既有路径
    也有方法名（`tools/call` 与 `tools/install_go.sh` 前缀一模一样）。
    它的已知漏网是一个拼错的**目录**，写在这里以免下一个人以为它守得住那一种。

    ★ 代码用**严格**判据（lenient=False）：`server.json` 的 `_meta.launch` 里
    每个路径形状的 token 都真的会被执行，所以那里没有豁免 ——
    这正是「把 `./adapters/mcp` 打错成 `./adapters/nope`」唯一能被抓到的地方，
    而那个错**就是命令跑不起来的原因**。
    """
    out = []
    for p in sorted(paths_in(text)):
        # `.out/...` 是构建产物目录，仓库里不该有；它由 CLIENTS.md 的构建步骤产生。
        if p.startswith(".out/"):
            continue
        full = os.path.join(ROOT, p)
        exists = os.path.exists(full)
        if exists:
            continue
        last = p.rstrip("/").rsplit("/", 1)[-1]
        if lenient and "." not in last:
            continue  # 不是文件形状：多半是方法名或目录引用，不判
        out.append("%s 提到 %s，而它不在仓库里" % (label, p))
    return out


def problems_for_skill(path):
    out = []
    if not os.path.exists(path):
        return ["skills 目录里没有 teps/SKILL.md：技能是接入的说明面，缺了它主机得读源码"]
    text = open(path, encoding="utf-8").read()
    m = re.match(r"^---\n(.*?)\n---\n", text, re.S)
    if not m:
        return ["SKILL.md 没有 YAML front-matter"]
    fm = m.group(1)
    name = re.search(r"^name:\s*(\S+)\s*$", fm, re.M)
    if not name:
        out.append("SKILL.md 的 front-matter 没有 name")
    elif name.group(1) != "teps":
        out.append("SKILL.md 的 name 是 %s，而目录叫 teps" % name.group(1))
    if not re.search(r"^description:\s*\S", fm, re.M):
        out.append("SKILL.md 的 front-matter 没有 description")
    # ★ 教义：装了技能不等于接上了服务器。这句话必须在文件里。
    if "does not register" not in text:
        out.append("SKILL.md 没说清「装了这个技能不等于接上了服务器」——"
                   "技能供说明、服务器供工具，两件事必须分开写")
    return out


def run():
    out = []
    if not os.path.exists(SERVER):
        out.append("没有 server.json")
    else:
        doc = json.load(open(SERVER, encoding="utf-8"))
        out += problems_for_server(doc)
        launch = ((doc.get("_meta") or {}).get("io.github.infileap/teps") or {}).get("launch") or {}
        blob = " ".join(launch.get("build", []) + [launch.get("run", "")])
        # ★ launch 那一块**不是散文，是代码**：那里的每一个路径形状的 token
        # 都真的会被执行。所以对它用严格判据（不做扩展名豁免）——
        # 这正是「把 ./adapters/mcp 打错成 ./adapters/nope」唯一能被抓到的地方，
        # 而那个错**就是命令跑不起来的原因**。
        out += problems_for_paths("server.json 的 _meta.launch", blob, lenient=False)
    if os.path.exists(CLIENTS):
        out += problems_for_paths("docs/CLIENTS.md", open(CLIENTS, encoding="utf-8").read())
    else:
        out.append("没有 docs/CLIENTS.md：接入说明缺了，主机只能读源码")
    out += problems_for_skill(SKILL)
    return out


def self_test():
    """两半：证明它还看得见，证明看见了会红。"""
    out = []
    good = {"name": "x", "description": "y", "version": "1",
            "packages": [{"registryType": "github", "identifier": "a/b",
                          "transport": {"type": "stdio"}}]}
    if problems_for_server(good):
        out.append("SELF-TEST  一个合法声明被判成有问题（误报）")
    bad = {"name": "x", "description": "y", "version": "1",
           "packages": [{"registryType": "github", "identifier": "a/b",
                         "transport": {"type": "carrier-pigeon"}}]}
    if not problems_for_server(bad):
        out.append("SELF-TEST  未知传输没被抓到（它还看得见吗）")
    if not problems_for_paths("SELF-TEST", "see adapters/nope/missing.go"):
        out.append("SELF-TEST  不存在的路径没被抓到（它还看得见吗）")
    if problems_for_paths("SELF-TEST", "see .out/teps-mcp"):
        out.append("SELF-TEST  .out/ 是构建产物目录，不该被当路径核")
    if not problems_for_paths("SELF-TEST", "go build ./adapters/nope", lenient=False):
        out.append("SELF-TEST  严格判据下，一个不存在的目录没被抓到（它还看得见吗）")
    # ★ 这一半最要紧：证明「看见了会红」。
    out.append("SELF-TEST  注入的违规（这一条必须让它退出 1）")
    return out


def main():
    if os.environ.get("TEPS_CLIENTS_SELF_TEST") == "1":
        bad = self_test()
        if len(bad) != 1:
            for b in bad:
                print("  " + b)
            print("接入面守卫自检失败：%d 处" % len(bad))
            return 1
        print("接入面守卫自检：还看得见（3 类各一个样本），且看见了会红")
        return 1
    out = run()
    if not out:
        print("接入面一致：server.json 的必填字段、launch 里的路径、CLIENTS.md 里的路径、SKILL.md 的形状。")
        return 0
    for p in out:
        print("  " + p)
    print("接入面守卫：%d 处不一致。" % len(out))
    return 1


if __name__ == "__main__":
    sys.exit(main())
