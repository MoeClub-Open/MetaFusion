#!/usr/bin/env python3
"""CI 引用的脚本/文件必须存在于版本库（元检查）。

为什么需要它：CI 里写过 run: bun scripts/xxx.ts 而那个脚本根本没入库（或路径写错一层目录），
表现是 job 在 clone 出来的干净检出上红——本地却看不出来，因为本地工作区里文件是有的。
本脚本把「工作流文本 → 被引用的仓库内路径」解析出来，逐个核对 **git 索引**（不是工作区），
所以「本地有、没提交」也会被抓到。另外核对 bun run <script> 的目标在 package.json 里存在。

为什么不用 PyYAML：这个检查要在最小依赖下跑（CI 里 pyyaml 只在别的 job 装），
工作流结构简单，按缩进解析足够——它只认 run: 块与 working-directory。

用法：
    python3 scripts/check_ci_scripts.py
    python3 scripts/check_ci_scripts.py --selftest
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WORKFLOWS = ROOT / ".github" / "workflows"

# 运行时才存在的路径：构建产物、CI 机器上的目录、容器内路径。
SKIP_PREFIXES = ("/tmp/", "/etc/", "/usr/", "/bin/", "/var/", "/opt/", "/dev/", "/proc/")
# "$" 与 "{{" 分开写：GitHub 表达式在路径检查里一律跳过，连在一起容易看成本模板的插值。
SKIP_SUBSTRINGS = ("$" + "{{", "://", "node_modules/")
# 被 CI 直接引用的仓库内文件：脚本，加上工作流里挂载/读取的配置文件与版本锁。
SCRIPT_SUFFIXES = (".ts", ".mjs", ".cjs", ".js", ".py", ".sh", ".ps1", ".bash", ".conf", ".lock")

# 形如 scripts/check_x.py、./deploy/a.sh、frontend/scripts/gen.ts 的路径。
PATH_RE = re.compile(r"(?<![\w./-])((?:\./)?(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+\.(?:ts|mjs|cjs|js|py|sh|ps1|bash|conf|lock))")
BUN_RUN_RE = re.compile(r"\bbun run ([A-Za-z0-9_:.-]+)")
INLINE_CODE_RE = re.compile(r"\b(?:bunx?|node|deno|python3?)\s+-(?:e|c)\b")

ARROW = "\u2192"


def git_index(root: Path) -> set[str]:
    out = subprocess.run(
        ["git", "ls-files"], cwd=root, capture_output=True, text=True, check=False
    )
    if out.returncode != 0:
        raise SystemExit("git ls-files 失败：" + out.stderr.strip())
    return {line.strip().replace("\\", "/") for line in out.stdout.splitlines() if line.strip()}


def parse_workflow(text: str) -> list[dict]:
    """按缩进解析出每个 step 的 run 块与生效的 working-directory。"""
    steps: list[dict] = []
    job = None
    # job 级工作目录按 job 名存：只在最后统一回填（早退回填会用到"最后一个 job"的值）
    job_wd: dict[str, str] = {}
    in_defaults = False
    step: dict | None = None
    run_lines: list[str] = []
    in_run = False
    run_indent = 0

    def flush_run() -> None:
        nonlocal run_lines, in_run, step
        if step is not None and run_lines:
            step.setdefault("runs", []).append("\n".join(run_lines))
        run_lines = []
        in_run = False

    for raw in text.splitlines():
        line = raw.rstrip()
        stripped = line.strip()
        indent = len(line) - len(line.lstrip(" "))

        if in_run:
            if stripped and indent <= run_indent:
                flush_run()
            else:
                run_lines.append(stripped)
                continue

        if not stripped or stripped.startswith("#"):
            continue

        if indent == 2 and stripped.endswith(":") and not stripped.startswith("- "):
            job = stripped[:-1]
            in_defaults = False
            step = None
            continue
        if job is None:
            continue

        if stripped.startswith("- ") or stripped == "-":
            step = {"job": job, "workdir": None, "runs": []}
            steps.append(step)
            in_defaults = False
            continue

        if indent == 4 and stripped == "defaults:":
            in_defaults = True
            continue
        if indent <= 4 and stripped.endswith(":") and stripped != "defaults:":
            in_defaults = False

        # defaults 块里的 run: 是键不是命令：这里若当成命令块，会把紧跟的
        # working-directory 行当成它的命令吞掉，job 级工作目录就永远取不到。
        if in_defaults and indent > 4:
            if stripped.startswith("working-directory:"):
                job_wd[job] = stripped.split(":", 1)[1].strip().strip("'\"")
            continue

        if stripped.startswith("working-directory:"):
            if step is not None:
                step["workdir"] = stripped.split(":", 1)[1].strip().strip("'\"")
            continue

        if stripped.startswith("run:"):
            in_run = True
            run_indent = indent
            run_lines = []
            if step is None or step.get("runs"):
                step = {"job": job, "workdir": None, "runs": []}
                steps.append(step)
            tail = stripped[4:].strip()
            if tail and tail not in ("|", ">", "|-", ">-"):
                run_lines.append(tail)
                flush_run()

    flush_run()

    for s in steps:
        if s.get("workdir") is None:
            s["workdir"] = job_wd.get(s["job"]) or "."
    return steps


def normalize(p: str) -> str:
    parts: list[str] = []
    for seg in p.split("/"):
        if seg in ("", "."):
            continue
        if seg == "..":
            if parts:
                parts.pop()
            continue
        parts.append(seg)
    return "/".join(parts)


def resolve(path_token: str, workdir: str) -> str | None:
    """把工作流里的路径字面量归一成仓库相对路径；不属于仓库内文件就返回 None。"""
    tok = path_token.strip()
    if not tok or any(sub in tok for sub in SKIP_SUBSTRINGS):
        return None
    if tok.startswith("/") or tok.startswith(SKIP_PREFIXES):
        return None
    if tok.startswith("../") or tok.startswith("$PWD/../"):
        return None
    # $PWD/ 前缀：正则从 "$PWD/deploy/x.sh" 抓到的字面量是 "PWD/deploy/x.sh"
    # （$ 不在路径字符集里），所以两种形态都要剥。
    if tok.startswith("$PWD/"):
        tok = tok[len("$PWD/"):]
    if tok.startswith("PWD/"):
        tok = tok[len("PWD/"):]
    if tok.startswith("./"):
        tok = tok[2:]
    if not tok.endswith(SCRIPT_SUFFIXES):
        return None
    base = (workdir or ".").strip()
    if base.startswith("./"):
        base = base[2:]
    joined = tok if base in (".", "") else base + "/" + tok
    return normalize(joined)


def strip_mount_suffix(tok: str) -> str:
    """docker -v host:container:ro 里的 host 段。"""
    return tok.split(":", 1)[0]


def collect(steps: list[dict]) -> list[tuple[str, str, str]]:
    """返回 (引用来源说明, 仓库相对路径, 原始行)。"""
    found: list[tuple[str, str, str]] = []
    for s in steps:
        for run in s.get("runs", []):
            for line in run.splitlines():
                # `bun -e "..."` / `python3 -c "..."` 是内联代码：里面的字符串拼接
                # （'src/messages/'+f）会被当成路径，全是假阳性。
                if INLINE_CODE_RE.search(line):
                    continue
                for m in PATH_RE.finditer(line):
                    rel = resolve(strip_mount_suffix(m.group(1)), s["workdir"])
                    if rel:
                        found.append((s["job"] + " " + ARROW + " " + s["workdir"], rel, line.strip()))
    return found


def check_run_targets(steps: list[dict], root: Path) -> list[str]:
    problems: list[str] = []
    for s in steps:
        base = "" if s["workdir"] in (".", "") else s["workdir"]
        pkg = root / base / "package.json"
        if not pkg.exists():
            continue
        scripts = json.loads(pkg.read_text(encoding="utf-8")).get("scripts", {})
        for run in s.get("runs", []):
            for m in BUN_RUN_RE.finditer(run):
                if m.group(1) not in scripts:
                    problems.append(
                        s["job"] + ": bun run " + m.group(1) + " 在 " + str(pkg.relative_to(root)) + " 里没有对应脚本"
                    )
    return problems


def selftest() -> int:
    fake = """
name: t
jobs:
  frontend:
    defaults:
      run:
        working-directory: frontend
    steps:
      - name: a
        run: bun scripts/present.ts
      - name: b
        working-directory: .
        run: bun scripts/root-present.ts
      - name: c
        run: |
          docker run --rm -v $PWD/deploy/nginx.conf:/etc/nginx/nginx.conf:ro nginx:1
          python3 scripts/missing.py
          node /tmp/built.ts
          ./deploy/no_such_script.sh
"""
    steps = parse_workflow(fake)
    got = {(w, p) for w, p, _ in collect(steps)}
    expect = {
        ("frontend " + ARROW + " frontend", "frontend/scripts/present.ts"),
        ("frontend " + ARROW + " .", "scripts/root-present.ts"),
        ("frontend " + ARROW + " frontend", "frontend/deploy/nginx.conf"),
        ("frontend " + ARROW + " frontend", "frontend/scripts/missing.py"),
        ("frontend " + ARROW + " frontend", "frontend/deploy/no_such_script.sh"),
    }
    cases = 0
    failed = 0

    def want(cond: bool, label: str) -> None:
        nonlocal cases, failed
        cases += 1
        if not cond:
            failed += 1
            print("  FAIL " + label)

    want(got == expect, "工作流解析：期望 " + str(sorted(expect)) + " 实际 " + str(sorted(got)))
    want("tmp/built.ts" not in {p for _, p in got}, "绝对路径不参与比对")
    want(normalize("frontend/./scripts/a.ts") == "frontend/scripts/a.ts", "normalize 去 ./")
    want(normalize("frontend/../scripts/a.ts") == "scripts/a.ts", "normalize 处理 ..")
    want(resolve("/abs/a.py", ".") is None, "绝对路径返回 None")
    want(resolve("../sib/a.py", ".") is None, "仓库外路径返回 None")
    want(resolve("$PWD/deploy/a.sh", ".") == "deploy/a.sh", "$PWD 前缀可归一")
    want(resolve("scripts/a.sh", "frontend") == "frontend/scripts/a.sh", "按工作目录拼接")
    want(resolve("a.txt", ".") is None, "非脚本后缀忽略")
    want(
        strip_mount_suffix("deploy/nginx.conf:/etc/nginx/nginx.conf:ro") == "deploy/nginx.conf",
        "docker 挂载后缀剥离",
    )
    print("check_ci_scripts --selftest：" + str(cases) + " 项用例，" + str(failed) + " 个问题")
    return 1 if failed else 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--selftest", action="store_true")
    ap.add_argument("--root", default=str(ROOT))
    args = ap.parse_args()
    if args.selftest:
        return selftest()

    root = Path(args.root).resolve()
    index = git_index(root)
    workflows = sorted(WORKFLOWS.glob("*.yml")) + sorted(WORKFLOWS.glob("*.yaml"))
    if not workflows:
        print("SKIP：.github/workflows 下没有工作流")
        return 0

    problems: list[str] = []
    referenced = 0
    for wf in workflows:
        steps = parse_workflow(wf.read_text(encoding="utf-8"))
        for source, rel, line in collect(steps):
            referenced += 1
            if rel in index:
                continue
            on_disk = (root / rel).exists()
            tail = "文件在本地存在但未入库（先 git add）" if on_disk else "版本库里不存在"
            problems.append(wf.name + ": " + source + " 引用 " + rel + "：" + tail + "｜" + line)
        for p in check_run_targets(steps, root):
            problems.append(wf.name + ": " + p)

    for p in problems:
        print("FAIL " + p)
    print(
        "check_ci_scripts: "
        + str(len(workflows))
        + " 个工作流、"
        + str(referenced)
        + " 处脚本引用，"
        + str(len(problems))
        + " 个问题"
    )
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
