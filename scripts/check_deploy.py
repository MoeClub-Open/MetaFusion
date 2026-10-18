#!/usr/bin/env python3
"""deploy/ 编排自检：本机无 docker 时也能跑（CI 的 compose-lint 只做 config 解析，
解析不出 "构建目标不存在" 这类错误——deploy/docker-compose.metadata.yml 就曾把
catalog-server 当成 backend 的阶段名，直到 build 才炸）。

检查四件事，任一不过以非零码退出：
1. deploy/*.yml 能被 YAML 解析；
2. build 字段里的变量写法能展开（${VAR} / ${VAR:-默认值} / ${VAR:?报错} / $VAR；
   名字只吃 [A-Za-z_][A-Za-z0-9_]*，${VAR:?msg} 的 msg 不参与）；
   **设了变量却给不出值**（未设置且无默认值、:? 的必填缺失、环境里显式给了空串）算 FAIL，
   不再当成"这台机器没有这个服务"悄悄放过——静默跳过正是这类假检查的根源；
3. 构建上下文在本机时，build.target 必须在对应 Dockerfile 里存在；
   目录不在本机（只检出主仓库、兄弟仓库缺席）只记"未能校验"并照常退出 0，不算失败；
4. 编排里 "${VAR:?...}" 形式声明的必填变量，必须能在 .env.example 里找到。
自测还通过模拟 Docker 验证真实部署函数的停写顺序和迁移失败行为，不连接容器。

用法：python scripts/check_deploy.py [仓库根目录]
      python scripts/check_deploy.py --selftest    # 只跑变量展开 / 路径解析的自测
"""
import os
import re
import shutil
import subprocess
import sys

import yaml  # 解析编排要用；--selftest 也走这里（CI 的这一步本来就会装 pyyaml）

REQUIRED = re.compile(r"\${([A-Z][A-Z0-9_]*):\?")
STAGE = re.compile(r"^FROM\s+\S+\s+[aA][sS]\s+(\S+)", re.M)

# 花括号写法；名字只吃合法字符，避免把 ${DB_PASSWORD:?...} 读成 DB_PASSWORDB。
BRACED = re.compile(r"\${(?P<name>[A-Za-z_][A-Z0-9_]*)(?::(?P<op>[-?])(?P<arg>[^}]*))?}")
# 裸写法：$VAR / $VAR-x（- 不属于变量名，所以名字在分隔符处自然收住）。
BARE = re.compile(r"\$(?P<name>[A-Za-z_][A-Z0-9_]*)(?![A-Za-z0-9_]*\})")  # 不带 op/arg 组
# 拼路径前先把 Windows 反斜杠换成 /，否则 ../frontend 与 admin\Dockerfile 拼不上。
WINDOWS_SEP = re.compile(r"[\\/]+")


class Resolution:
    """一次变量展开的结果：要么拿到 value，要么拿到人能看懂的原因。"""

    def __init__(self, value=None, error=None):
        self.value = value
        self.error = error


class ComposeLoader(yaml.SafeLoader):
    """"docker compose 的 !reset / !override 标签对 YAML 解析无意义，这里当 null 处理。"""


ComposeLoader.add_multi_constructor("!", lambda loader, suffix, node: None)


def compose_files(root):
    d = os.path.join(root, "deploy")
    if not os.path.isdir(d):
        print("FAIL 读不到 %s（%s 不是编排目录？）" % (os.path.relpath(d, root), root))
        return None
    return sorted(os.path.join(d, f) for f in os.listdir(d) if f.endswith(".yml"))


def expand_value(text, env=None):
    """展开 build 字段里的变量，返回 Resolution。

    取值顺序对齐 docker compose：环境变量优先，其次 :- 默认值；环境里显式给了空串也走默认值
    （CI 里常见 VAR=""）——否则一个空串就会把上下文变成仓库根，检查反而指到别处去。
    """
    env = os.environ if env is None else env
    error = None

    def one(match):
        nonlocal error
        name = match.group("name")
        op = match.groupdict().get("op")   # 裸写法没有 op：只有未设置才算错
        arg = match.groupdict().get("arg")
        value = env.get(name)
        if op == "?":
            if value is None:
                error = error or "${%s} 必填但未设置（%s）" % (name, (arg or "").strip() or "无说明")
                return ""
            return value
        if op == "-":
            return arg if value in (None, "") else value
        if value is None:
            error = error or "${%s} 未设置且无默认值" % name
            return ""
        return value

    out = BRACED.sub(one, text)
    if error:
        return Resolution(error=error)
    out = BARE.sub(one, out)
    if error:
        return Resolution(error=error)
    if out.strip() == "":
        return Resolution(error="展开后为空（%r）" % text)
    return Resolution(value=out)


def is_absolute(path):
    """本机绝对路径与 POSIX 绝对路径都算绝对：Windows 上 /srv/auth 会被 os.path.isabs 判成相对，
    拼出来就成了 <仓库>/srv/auth，检查会指到一个根本不存在的目录上去。"""
    return os.path.isabs(path) or path.startswith("/")


def join_rel(base_dir, rel):
    """跨平台拼相对路径：先按 / 切段再交给 os.path 规范化（Windows 两种分隔符都认）。"""
    if is_absolute(rel):
        return os.path.normpath(rel)
    return os.path.normpath(os.path.join(base_dir, *WINDOWS_SEP.split(rel.strip("/\\"))))


def display_path(path, root):
    """尽量给人类看相对路径；跨盘（或跨挂载根）时 os.path.relpath 会抛 ValueError，
    那就退回绝对路径——诊断信息不该把检查脚本自己搞崩。"""
    try:
        return os.path.relpath(path, root)
    except ValueError:
        return path


def stages_of(dockerfile):
    with open(dockerfile, encoding="utf-8") as fh:
        return {m.group(1).lower() for m in STAGE.finditer(fh.read())}


def resolve_build(build, env=None):
    """把一条 build 解析成 (context, dockerfile, None) 或 (None, None, 原因)。"""
    context = expand_value(str(build.get("context", ".")), env)
    if context.error:
        return None, None, "build.context: %s" % context.error
    dockerfile = expand_value(str(build.get("dockerfile") or "Dockerfile"), env)
    if dockerfile.error:
        return None, None, "build.dockerfile: %s" % dockerfile.error
    return context.value, dockerfile.value, None


def migrator_same_source(backend, migrator):
    backend_none = backend is None
    migrator_none = migrator is None
    if backend_none or migrator_none:
        return None
    left = (backend[0], backend[1].lower())
    right = (migrator[0], migrator[1].lower())
    if left != right:
        return ("backend-migrate 与 backend 构建来源不一致（backend=%s %s，migrator=%s %s）：" % (backend[0], backend[1], migrator[0], migrator[1]) + "迁移前必须同批构建两者，否则旧迁移器被复用")
    return None


SELFTEST_MIGRATOR_CASES = [
    ("同源构建通过", ("../backend", "Dockerfile"), ("../backend", "Dockerfile"), False),
    ("Dockerfile 大小写差异放行", ("../backend", "Dockerfile"), ("../backend", "dockerfile"), False),
    ("已有旧迁移器：上下文不一致被拦", ("../backend", "Dockerfile"), ("../backend-old", "Dockerfile"), True),
    ("已有旧迁移器：Dockerfile 不一致被拦", ("../backend", "Dockerfile"), ("../backend", "Dockerfile.old"), True),
    ("pull 路径迁移器无 build 放行", ("../backend", "Dockerfile"), None, False),
    ("单服务无 build 放行", None, ("../backend", "Dockerfile"), False),
]


SELFTEST_CASES = [
    # (说明, 输入, 环境, 期望值, 期望原因里的片段)
    ("默认值展开（含子目录）", "${MF_AUTH_DIR:-../../metafusion-auth}/admin/Dockerfile",
     {}, "../../metafusion-auth/admin/Dockerfile", None),
    ("环境变量优先于默认值", "${MF_AUTH_DIR:-../fallback}", {"MF_AUTH_DIR": "/srv/auth"}, "/srv/auth", None),
    ("空串环境变量走默认值", "${MF_AUTH_DIR:-../fallback}", {"MF_AUTH_DIR": ""}, "../fallback", None),
    ("未设置且无默认值", "${X}/Dockerfile", {}, None, "未设置且无默认值"),
    ("必填变量缺失（保留 :? 的说明）", "${DB_PASSWORD:?DB_PASSWORD must be set}",
     {}, None, "DB_PASSWORD must be set"),
    ("必填变量已设置", "${DB_PASSWORD:?must be set}", {"DB_PASSWORD": "pw"}, "pw", None),
    ("裸变量", "$MF_DOCS_DIR/Dockerfile", {"MF_DOCS_DIR": "../metafusion-docs"},
     "../metafusion-docs/Dockerfile", None),
    ("裸变量名在分隔符处收住", "$DIR-x", {"DIR": "/srv"}, "/srv-x", None),
    # 已展开的值里若出现 "$X}" / "${Z" 这类字面量（不是 docker 的变量替换），不能再当成变量，
    # 否则会误报"未设置"。这两条把该行为钉住。
    ("展开结果里的裸 $X} 不是变量", "${A} x$Q}", {"A": "v"}, "v x$Q}", None),
    ("展开结果里的花括号残段不算变量", "${A} y${Z", {"A": "v"}, "v y${Z", None),
]
SELFTEST_JOIN_CASES = [
    # (说明, compose 所在目录, 上下文, dockerfile, 期望结果)
    ("绝对路径原样保留（POSIX 写法在 Windows 上也不例外）", "/repo/deploy", "/srv/auth", "admin/Dockerfile",
     os.path.normpath(os.path.join("/srv/auth", "admin", "Dockerfile"))),
    ("相对上下文按 compose 所在目录解析", os.path.join("C:", "repo", "deploy"), "../../metafusion-auth",
     "admin/Dockerfile", os.path.normpath(os.path.join("C:", "metafusion-auth", "admin", "Dockerfile"))),
    ("反斜杠 dockerfile 也能拼", os.path.join("C:", "repo", "deploy"), "../frontend", "admin\\Dockerfile",
     os.path.normpath(os.path.join("C:", "repo", "frontend", "admin", "Dockerfile"))),
]


def migration_quiesce_selftest():
    """执行真实 Bash 函数，模拟 Docker 失败，保证停写失败不会继续迁移。"""
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    with open(os.path.join(root, "deploy", "deploy.sh"), encoding="utf-8") as fh:
        source = fh.read()
    functions = []
    for name in ("run_migrate", "stop_catalog_writes", "migrate_up_checked"):
        match = re.search(r"^function " + name + r"\(\) \{\n.*?^\}", source, re.M | re.S)
        if not match:
            return ["停写自测找不到部署函数 " + name], 0
        functions.append(match.group(0))

    bash = shutil.which("bash")
    # Windows 的系统 bash 是 WSL 启动器；优先用 Git 附带的原生 Bash。
    if os.name == "nt":
        git_bash = os.path.join(os.environ.get("ProgramFiles", "C:\\Program Files"), "Git", "bin", "bash.exe")
        if os.path.isfile(git_bash):
            bash = git_bash
    if not bash:
        return ["停写自测需要 Bash（不需要 Docker）"], 0

    scenarios = [
        ("ok", True), ("stop-failed", False), ("up-failed", False),
        ("status-failed", False), ("PENDING", False), ("DIRTY", False),
        ("MODIFIED", False), ("UNVERIFIED", False), ("UNKNOWN", False),
        ("empty-status", False),
    ]
    fixture = r'''
set -e
COMPOSE_ENV="--env-file fixture.env"
docker() {
    printf 'CALL:%s\n' "$*"
    case "$*" in
        *" stop backend mcp")
            [ "$SCENARIO" != stop-failed ] || return 9 ;;
        *" backend-migrate up")
            [ "$SCENARIO" != up-failed ] || return 9 ;;
        *" backend-migrate status")
            case "$SCENARIO" in
                status-failed) return 9 ;;
                empty-status) ;;
                ok) printf '000035_collaboration APPLIED\n' ;;
                *) printf '000035_collaboration %s\n' "$SCENARIO" ;;
            esac ;;
        *) return 99 ;;
    esac
}
'''
    expected_stop = "CALL:compose --env-file fixture.env -f docker-compose.yml -f docker-compose.prod.yml -f digest.yml stop backend mcp"
    expected_up = "CALL:compose --env-file fixture.env -f docker-compose.yml -f docker-compose.prod.yml -f digest.yml run --rm --no-deps backend-migrate up"
    failures = []
    for scenario, success in scenarios:
        script = fixture + "\nSCENARIO=" + scenario + "\n" + "\n".join(functions)
        script += '\nmigrate_up_checked -f docker-compose.prod.yml -f digest.yml\n'
        result = subprocess.run([bash, "--noprofile", "--norc", "-s"], input=script,
                                capture_output=True, text=True, encoding="utf-8", timeout=15)
        output = result.stdout + result.stderr
        if (result.returncode == 0) != success:
            failures.append("停写场景 %s 返回码 %s，期望成功=%s" % (scenario, result.returncode, success))
        calls = [line for line in result.stdout.splitlines() if line.startswith("CALL:")]
        if not calls or calls[0] != expected_stop:
            failures.append("停写场景 %s 未首先停止 backend 与 MCP 或丢失 Compose 覆盖" % scenario)
        if scenario == "stop-failed" and calls != [expected_stop]:
            failures.append("停止失败后仍执行了迁移")
        if scenario != "stop-failed" and (len(calls) < 2 or calls[1] != expected_up):
            failures.append("停写场景 %s 未在停写成功后执行同源迁移器" % scenario)
        if scenario == "up-failed" and len(calls) != 2:
            failures.append("迁移失败后仍执行了后续操作")
        if " up -d" in output or " start " in output or " restart " in output:
            failures.append("停写场景 %s 自动恢复了写入端" % scenario)
    return failures, len(scenarios)


def selftest():
    """离线自测：变量、路径和真实迁移函数；不调用真实 Docker、不改环境。"""
    failures = []
    for label, text, env, want_value, want_error in SELFTEST_CASES:
        got = expand_value(text, env)
        if want_error is None:
            if got.value != want_value or got.error is not None:
                failures.append("%s: 期望 %r，实际 value=%r error=%r" % (label, want_value, got.value, got.error))
        elif not got.error or want_error not in got.error:
            failures.append("%s: 期望原因含 %r，实际 error=%r value=%r" % (label, want_error, got.error, got.value))
    for label, compose_dir, context, dockerfile, want in SELFTEST_JOIN_CASES:
        ctx = join_rel(compose_dir, context)
        full = join_rel(ctx, dockerfile)
        if full != want:
            failures.append("%s: 期望 %r，实际 %r" % (label, want, full))
        if "}" in full or "$" in full:
            failures.append("%s: 结果里残留变量语法：%r" % (label, full))
    for label, backend, migrator, want_fail in SELFTEST_MIGRATOR_CASES:
        got = migrator_same_source(backend, migrator)
        if want_fail and got is None:
            failures.append("%s: 期望被拦，实际放行" % label)
        if not want_fail and got is not None:
            failures.append("%s: 期望放行，实际被拦：%s" % (label, got))

    quiesce_failures, quiesce_total = migration_quiesce_selftest()
    failures.extend(quiesce_failures)
    for bad in failures:
        print("FAIL " + bad)
    total = len(SELFTEST_CASES) + len(SELFTEST_JOIN_CASES) + len(SELFTEST_MIGRATOR_CASES) + quiesce_total
    print("check_deploy --selftest: %d 项用例，%d 个问题" % (total, len(failures)))
    return 1 if failures else 0


def main():
    root = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else ".")
    problems, skipped = [], []

    files = compose_files(root)
    if files is None:
        print("check_deploy: 1 个问题，0 项未能校验")
        return 1

    for path in files:
        rel = os.path.relpath(path, root)
        try:
            with open(path, encoding="utf-8") as fh:
                doc = yaml.load(fh, Loader=ComposeLoader) or {}
        except Exception as exc:  # noqa: BLE001 —— 解析失败就是要报出来的问题
            problems.append("%s: YAML 解析失败: %s" % (rel, exc))
            continue

        for name, svc in (doc.get("services") or {}).items():
            build = svc.get("build")
            if not isinstance(build, dict):
                continue
            context, dockerfile, why = resolve_build(build, os.environ)
            if why:
                # 变量给不出值是**配置问题**（不是"这台机器没检出"），必须失败；
                # 旧版正是在这里静默跳过，才留下"检查看着有、其实没跑"的空档。
                problems.append("%s:%s: %s" % (rel, name, why))
                continue
            ctx = join_rel(os.path.dirname(path), context)
            dfile = join_rel(ctx, dockerfile)
            target = build.get("target")
            if not os.path.isfile(dfile):
                skipped.append("%s:%s: 未能校验（本机没有 %s）" % (rel, name, display_path(dfile, root)))
                continue
            stages = stages_of(dfile)
            if target and target.lower() not in stages:
                problems.append(
                    "%s:%s: build.target=%s 在 %s 里不存在（可用阶段: %s）"
                    % (rel, name, target, display_path(dfile, root), ", ".join(sorted(stages)))
                )

        services = doc.get("services") or {}
        sources = {}
        for key in ("backend", "backend-migrate"):
            b = (services.get(key) or {}).get("build")
            if not isinstance(b, dict):
                continue
            c, d, w = resolve_build(b, os.environ)
            if w:
                continue
            sources[key] = (c, d)
        why = migrator_same_source(sources.get("backend"), sources.get("backend-migrate"))
        if why:
            problems.append("%s: %s" % (rel, why))

    # 3. 必填变量必须写进模板，否则照着 .env.example 建的 .env 起不来
    template = os.path.join(root, ".env.example")
    documented = set()
    if os.path.exists(template):
        with open(template, encoding="utf-8") as fh:
            documented = set(re.findall(r"^#?\s*([A-Z][A-Z0-9_]*)[=:]", fh.read(), re.M))
    for path in files:
        rel = os.path.relpath(path, root)
        with open(path, encoding="utf-8") as fh:
            for var in sorted(set(REQUIRED.findall(fh.read()))):
                if var not in documented:
                    problems.append("%s: 必填变量 %s 未在 .env.example 中记录" % (rel, var))

    # "问题"是配置错误（退出 1）；"未能校验"是本机没检出兄弟仓库，分开报，不混在一起。
    for note in skipped:
        print("skip " + note)
    for bad in problems:
        print("FAIL " + bad)
    print("check_deploy: %d 个问题，%d 项未能校验" % (len(problems), len(skipped)))
    return 1 if problems else 0


if __name__ == "__main__":
    if "--selftest" in sys.argv[1:]:
        sys.exit(selftest())
    sys.exit(main())
