#!/usr/bin/env python3
"""文档端点引用 ↔ 主仓真实路由清单的一致性检查：文档写了、实现里没有的 /api 端点判 P1。

为什么需要它：端点引用的唯一真实来源是 backend 的注册代码，而教程在独立仓库
（../metafusion-docs）、技能契约在 ../metafusion-skills，两者与实现之间没有任何机器守着的绑定。
文档里出现过实现里并不存在的端点（写错的别名、已删除或改名的端点），读者照着调只会拿到 404。

路由来源的选择（先侦察再定，不用脆弱正则硬猜）：
1. backend 里没有 openapi.json / openapi.yaml 之类的生成物，也没有 go:embed 一份：
   GET /api/openapi.json 是 openapi.go 的 OpenAPI() 在进程内用反射拼出来的内存结构，
   静态文件根本不存在，因此不能当来源。
2. 注册形态是 gin 的 RouterGroup，前缀分两层：catalog/http.go 里 cat/extSrc/defs/ext/shelves
   的路径相对 Group("/catalog") 这类父前缀，父前缀又落在函数参数上
   （h.registerGroup(r.Group("/api"))），capabilities/handler.go 则在根引擎上写全路径。
   所以来源取"实现本身"：扫描 backend/**/*.go（排除 _test.go）的 X.Method("path") 调用，
   沿 Group 链与函数参数（*gin.RouterGroup / *gin.Engine）把前缀补全。
3. 再并入 catalog/openapi.go 里声明的 (path, method)：同包的
   TestOpenAPIRouteCoverage / TestOpenAPIReverseCoverage 已强制它与真实路由双向一致，
   取并集是为了让"解析漏项"退化成少报，而不是把活端点误报成残留（假 P1 会让检查失去信任）。

判定口径：
- P1：文档/技能里有、主仓实现里没有的 /api 端点（非零退出）。反向（实现里有、文档没写）
  只打印报告行，不失败——那是文档缺口，不是错误引用，不该拦住主仓的 CI。
- 只有主仓 /api 前缀判 P1。/api 下归属账号(auth)/互动(community)/存储(storage)的路径只报告：
  它们的实现不在本仓库，本脚本没有证据判它错（归属取自
  docs/architecture/service-split-migration.md §2 表）。
- 归一化：路径参数（冒号写法、花括号写法、具体数字或 UUID）折成同一段 *；查询串与 #锚点丢弃。
- 不参与比对的引用逐条给理由（并带行号证据）：纯前缀（以 / 结尾）、通配符片段、花括号列表简写
  （/api/admin/{users,groups} 是前缀枚举，而 {id} 这类路径参数照常归一化后比对）、
  讲分组前缀的句子（"前缀是 /api/external-sources"）、文档声明不存在的版本前缀（/api/v1）、
  以及否定标记紧邻引用的反例句（"没有 /api/search"、"不是 /api/catalog/importer"）。
  后两类是启发式：被它们挪进"只报告"桶的引用都会连同行原文打印出来，规则本身可以被人工复核；
  判 P1 仍然只看"归一化后的路径在实现里注册过没有"。

用法：python3 scripts/check_doc_routes.py [仓库根目录] [--siblings-root DIR]
      [--docs-repo DIR] [--skills-repo DIR] [--selftest]
      （仓库根目录可执行；--selftest 是离线自测，不读兄弟仓库）
"""
import argparse
import os
import re
import sys

# ── 路由来源（backend 实现）────────────────────────────────────────────
# 组赋值：docs := api.Group("/docs")（父前缀可能来自函数参数，见下面的第二层数据流）
GO_GROUP_ASSIGN = re.compile(r"([A-Za-z_]\w*)\s*:?=\s*([A-Za-z_]\w*)\.Group\(\s*\"([^\"]*)\"")
# 内联组：h.registerGroup(r.Group("/api"))——前缀在这一层落到参数名上
GO_GROUP_CALL = re.compile(r"\A([A-Za-z_]\w*)\.Group\(\s*\"([^\"]*)\"")
GO_ROUTE_CALL = re.compile(r"([A-Za-z_]\w*)\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(\s*\"([^\"]*)\"")
GO_FUNC_DECL = re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(([^)]*)\)")
GO_ROUTER_PARAM = re.compile(r"([A-Za-z_]\w*)\s+\*?gin\.(?:RouterGroup|IRoutes|Engine)\b")
GO_CALL_HEAD = re.compile(r"([A-Za-z_]\w*)\s*\(")
# openapi.go 的声明行：表里的 {"<path>", "<method>", ...} 与零散的 add("<path>", "<method>", ...)
GO_OPENAPI_PAIR = re.compile(r"\"(/[^\"]*)\"\s*,\s*\"(get|post|put|patch|delete|head|options)\"")

# ── 归一化 ────────────────────────────────────────────────────────────
UUID_RE = re.compile(r"\A[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\Z")
HEX_RE = re.compile(r"\A[0-9a-fA-F]{32}\Z")
# 路径参数：冒号段与花括号段都折成 *，几种写法在文档里混用（见 --selftest 的用例）
PARAM_SEG_RE = re.compile(r"\A(:[A-Za-z_]\w*|\{[A-Za-z_]\w*\})\Z")

# 文档里的端点引用：/api/ 开头。反引号、代码块、表格、Markdown 链接都是同一份文本，
# 逐个语法分别解析只会漏，所以直接扫原文。
DOC_REF_RE = re.compile(r"/api/[A-Za-z0-9][A-Za-z0-9_./{}:*\-]*")
# 文档明确写着"不存在"的版本前缀（README/SKILL 用它们解释为什么调不通），不是端点
VERSION_PREFIX_RE = re.compile(r"\A/api/v\d+(/|\Z)")
# 反例语境的标记：文档用"没有 X / 不是 X"说明该路径调不通，这类引用不是文档在推荐端点
NEGATION_MARKERS = ("不是", "没有", "不存在", "不再", "no such", "does not exist")
# 反例标记必须紧邻引用才算数：窗口放大到整句会把"只渲染…返回的键值，没有写入控件"这类
# 无关的"没有"也算成豁免，于是真端点被踢出比对、报告反而变糊
NEGATION_WINDOW = 12
NEGATION_CONTEXT = 12
# 前缀说明的标记：紧跟引用的这几个字说明它在讲前缀，而不是在推荐一条端点
PREFIX_MARKERS = ("前缀", "prefix")
PREFIX_CONTEXT = 8
TRAILING_PUNCT = ".。，、；：,;:\"'"
BACKTICK = chr(96)

# /api 下不属于主仓的前缀：文档引用它们不算错，但本脚本没有兄弟服务的路由表，无法判对错，
# 因此只报告、不失败。逐条对应 docs/architecture/service-split-migration.md §2 表的归属列。
OTHER_SERVICE_ROUTES = [
    ("auth", "exact", "/api/users/*"),  # 公开账号资料（网关按 ^/api/users/[^/]+$ 分流）
    ("auth", "prefix", "/api/setup"),
    ("auth", "prefix", "/api/auth/"),
    ("auth", "prefix", "/api/admin/users"),
    ("auth", "prefix", "/api/admin/groups"),
    ("auth", "prefix", "/api/admin/permissions"),
    ("auth", "prefix", "/api/admin/settings"),
    ("auth", "prefix", "/api/admin/invites"),
    ("auth", "prefix", "/api/admin/oauth/"),
    ("auth", "prefix", "/api/oauth/"),
    ("auth", "prefix", "/api/oidc/"),
    ("auth", "prefix", "/api/.well-known"),
    ("auth", "prefix", "/api/developer/"),
    ("community", "prefix", "/api/community/"),
    ("community", "prefix", "/api/favorites/"),
    ("community", "prefix", "/api/messages/"),
    ("community", "exact", "/api/users/*/favorites"),
    ("community", "exact", "/api/users/*/stats"),
    ("storage", "prefix", "/api/storage/"),
]

SIBLING_DEFAULTS = {"docs": "../metafusion-docs", "skills": "../metafusion-skills"}

# 引用记录：(归一化路径, 原始写法, 文件, 行号, 行原文)；忽略记录同形但第二项是忽略原因
REF_NORM, REF_RAW, REF_FILE, REF_LINE, REF_TEXT = range(5)


def norm_path(raw):
    """把一条路径折成可比较的形态：去查询串/锚点，路径参数（冒号、花括号、数字、UUID）折成 *。"""
    path = raw.split("?", 1)[0].split("#", 1)[0]
    segments = []
    for seg in path.split("/"):
        if seg == "":
            continue
        if PARAM_SEG_RE.match(seg) or seg.isdigit() or UUID_RE.match(seg) or HEX_RE.match(seg):
            segments.append("*")
        else:
            segments.append(seg)
    return "/" + "/".join(segments)


def join_group(base, prefix):
    if prefix == "":
        return base
    if not prefix.startswith("/"):
        prefix = "/" + prefix
    return base + prefix


def go_source_files(backend_dir):
    """backend 下的 Go 源码；_test.go 里的路由是测试自建的引擎，不是真实清单。"""
    out = []
    for dirpath, dirnames, filenames in os.walk(backend_dir):
        dirnames[:] = [d for d in dirnames if d not in (".git", "node_modules", "vendor")]
        for name in filenames:
            if name.endswith(".go") and not name.endswith("_test.go"):
                out.append(os.path.join(dirpath, name))
    return sorted(out)


def split_args(text):
    """顶层逗号切分：实参里带嵌套调用与字符串，朴素 split(",") 会切错。"""
    args, depth, current = [], 0, []
    for ch in text:
        if ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        if ch == "," and depth == 0:
            args.append("".join(current).strip())
            current = []
            continue
        current.append(ch)
    tail = "".join(current).strip()
    if tail:
        args.append(tail)
    return args


def iter_calls(line):
    """逐行找调用点并按括号深度取实参：r.Group("/api") 这类嵌套实参是常态。"""
    for match in GO_CALL_HEAD.finditer(line):
        start, depth, index = match.end(), 1, match.end()
        while index < len(line) and depth > 0:
            depth += {"(": 1, ")": -1}.get(line[index], 0)
            index += 1
        if depth == 0:
            yield match.group(1), split_args(line[start:index - 1])


def scan_backend_routes(backend_dir, root):
    """扫描实现里的 gin 注册，返回 (routes, 未解析提示, 前缀变量数, /api 组前缀)。

    只做两层数据流：组赋值（父是本函数内的变量）与函数参数传递（父在调用点实参里）。
    用不动点迭代而不按行顺序推：文件之间没有读取顺序保证，exchange.go 可能先于 http.go 被读。
    前缀按函数作用域存放：不同函数的同名参数（registerGroup 与 registerExchange 都叫 api）
    指向的可能不是同一层组。
    """
    group_assigns = []  # (函数, 变量, 父变量, 组路径, 位置)
    funcs, calls = {}, []  # 函数名 -> [gin 路由参数名]；(调用方函数, 被调函数, 实参, 位置)
    route_calls = []  # (函数, 文件, 行号, 接收者, 方法, 路径)
    current = ""

    for path in go_source_files(backend_dir):
        shown = os.path.relpath(path, root).replace(os.sep, "/")
        with open(path, encoding="utf-8") as fh:
            for index, line in enumerate(fh.read().splitlines()):
                lineno = index + 1
                where = "%s:%d" % (shown, lineno)
                decl = GO_FUNC_DECL.match(line)
                if decl:
                    current = decl.group(1)
                    router_params = []
                    for param in [p.strip() for p in decl.group(2).split(",") if p.strip()]:
                        router = GO_ROUTER_PARAM.match(param)
                        if router:
                            router_params.append(router.group(1))
                    if router_params:
                        funcs[current] = router_params
                assign = GO_GROUP_ASSIGN.search(line)
                if assign:
                    group_assigns.append((current, assign.group(1), assign.group(2), assign.group(3), where))
                for callee, args in iter_calls(line):
                    calls.append((current, callee, args, where))
                route = GO_ROUTE_CALL.search(line)
                if route:
                    route_calls.append((current, shown, lineno, route.group(1), route.group(2), route.group(3)))

    local, params = {}, {}  # (函数, 变量) -> 前缀；(函数, 参数) -> 前缀

    def lookup(func, name):
        # 认不出的接收者按根引擎处理：根引擎上的注册本来就写全路径（capabilities 就是这样）
        if (func, name) in local:
            return local[(func, name)]
        return params.get((func, name), "")

    for _round in range(8):  # 轮数上限只是防呆：真实链路最多三层（引擎 -> /api -> /catalog）
        changed = False
        for func, var, parent, group, _where in group_assigns:
            if (func, parent) not in local and (func, parent) not in params:
                continue
            value = join_group(lookup(func, parent), group)
            if local.get((func, var)) != value:
                local[(func, var)] = value
                changed = True
        for caller, callee, args, _where in calls:
            router_params = funcs.get(callee)
            if not router_params:
                continue
            for position, param in enumerate(router_params):
                if position >= len(args):
                    continue
                arg = args[position]
                # 实参可能是 r.Group("/api") 或 ex := api.Group("/exchange", attachUser(s))：
                # 只认开头，不要求整串匹配（组构造常带中间件实参）
                inline = GO_GROUP_CALL.match(arg)
                if inline:
                    value = join_group(lookup(caller, inline.group(1)), inline.group(2))
                elif re.fullmatch(r"[A-Za-z_]\w*", arg):
                    value = lookup(caller, arg)
                else:
                    continue
                if params.get((callee, param)) != value:
                    params[(callee, param)] = value
                    changed = True
        if not changed:
            break

    routes = {}
    for func, shown, lineno, receiver, method, raw in route_calls:
        routes.setdefault((method.upper(), norm_path(join_group(lookup(func, receiver), raw))),
                          []).append("%s:%d" % (shown, lineno))
    unresolved = sorted({"%s: %s.Group(\"%s\")" % (where, parent, group)
                         for func, _var, parent, group, where in group_assigns
                         if (func, parent) not in local and (func, parent) not in params})
    # 诊断用：把解析出来的组前缀原样交给报告，免得"抽取失效"只能靠猜
    resolved_prefixes = ",".join(sorted({value for value in params.values() if value})) or "(空)"
    return routes, unresolved, len(local) + len(params), resolved_prefixes


def scan_openapi_routes(openapi_path, root):
    """openapi.go 的声明行；路径相对该文档 servers 里声明的 /api 基址。"""
    routes = {}
    if not os.path.isfile(openapi_path):
        return routes
    shown = os.path.relpath(openapi_path, root).replace(os.sep, "/")
    with open(openapi_path, encoding="utf-8") as fh:
        for number, line in enumerate(fh, 1):
            for raw, method in GO_OPENAPI_PAIR.findall(line):
                routes.setdefault((method.upper(), norm_path("/api" + raw)), []).append("%s:%d" % (shown, number))
    return routes


def iter_markdown(dirpath):
    for dirpath_now, dirnames, filenames in os.walk(dirpath):
        dirnames[:] = [d for d in dirnames if d not in (".git", "node_modules", "dist", "build")]
        for name in sorted(filenames):
            if name.endswith(".md"):
                yield os.path.join(dirpath_now, name)


def clean_ref_token(token):
    """剥掉紧跟路径的标点；花括号/圆括号只在比开括号多时才剥：{id} 是路径参数不是标点。"""
    token = token.rstrip(TRAILING_PUNCT + BACKTICK)
    while token.endswith("}") and token.count("}") > token.count("{"):
        token = token[:-1]
    while token.endswith(")") and token.count(")") > token.count("("):
        token = token[:-1]
    return token


def extract_refs(text, shown):
    """返回 (引用, 忽略项, 反例引用)。后两者也带证据：忽略/豁免规则本身要能被复核。"""
    refs, skipped, negated = [], [], []
    lines = text.splitlines()
    for index, line in enumerate(lines):
        number = index + 1
        # 反例可能跨行（"也没有独立的" 换行后才是路径），所以语境带上一行的尾巴；
        # 但表格行的尾巴常是别的列的结论（"实现里没有 → P1"），拿它当语境会误伤下一行的正常引用
        previous = lines[index - 1].rstrip() if index else ""
        context_head = "" if previous.lstrip().startswith("|") else previous[-NEGATION_CONTEXT:]
        for match in DOC_REF_RE.finditer(line):
            raw = clean_ref_token(match.group(0))
            if raw in ("/api", "/api/") or raw.endswith("/"):
                skipped.append((raw, "纯前缀", shown, number, line))
                continue
            if "*" in raw:
                skipped.append((raw, "通配符片段", shown, number, line))
                continue
            if VERSION_PREFIX_RE.match(raw):
                skipped.append((raw, "文档声明不存在的版本前缀", shown, number, line))
                continue
            if has_brace_list(raw):
                skipped.append((raw, "花括号列表简写（前缀枚举，不是单条端点）", shown, number, line))
                continue
            normalized = norm_path(raw)
            if normalized == "/api" or normalized.count("/") < 2:
                skipped.append((raw, "纯前缀", shown, number, line))
                continue
            before = line[:match.start()]
            if any(marker in before[-PREFIX_CONTEXT:] for marker in PREFIX_MARKERS):
                # 文档在讲分组前缀（"前缀是 /api/importer"），不是在推荐一条端点
                skipped.append((raw, "前缀说明（讲的是分组前缀，不是端点）", shown, number, line))
                continue
            if any(marker in (context_head + before)[-NEGATION_WINDOW:] for marker in NEGATION_MARKERS):
                negated.append((normalized, raw, shown, number, line))
                continue
            refs.append((normalized, raw, shown, number, line))
    return refs, skipped, negated


def has_brace_list(raw):
    """花括号列表简写（/api/admin/{users,groups}）是前缀枚举，不是单条端点；
    而 {id} 这类路径参数是正常端点写法，必须照常归一化后参与比对。"""
    for seg in raw.split("/"):
        if "{" in seg and not PARAM_SEG_RE.match(seg):
            return True
    return False


def other_service_owner(normalized):
    for owner, kind, pattern in OTHER_SERVICE_ROUTES:
        if kind == "exact":
            if normalized == pattern:
                return owner
        elif normalized == pattern.rstrip("/") or normalized.startswith(pattern):
            return owner
    return None


def compare(implemented_paths, refs):
    """核心判定：返回 (P1, 其他服务报告)。真实检查与 --selftest 共用它。"""
    p1, other = {}, {}
    for ref in refs:
        normalized = ref[REF_NORM]
        if normalized in implemented_paths:
            continue
        owner = other_service_owner(normalized)
        if owner:
            other.setdefault((owner, normalized), []).append(ref)
        else:
            p1.setdefault(normalized, []).append(ref)
    return p1, other


def collect_refs(doc_roots, root):
    refs, skipped, negated, scanned = [], [], [], []
    for label, base in doc_roots:
        count = 0
        for path in iter_markdown(base):
            shown = os.path.relpath(path, root).replace(os.sep, "/")
            with open(path, encoding="utf-8", errors="replace") as fh:
                found, ignored, denied = extract_refs(fh.read(), shown)
            refs.extend(found)
            skipped.extend(ignored)
            negated.extend(denied)
            count += 1
        scanned.append((label, count))
    return refs, skipped, negated, scanned


def snippet(line):
    text = " ".join(line.split())
    return text if len(text) <= 120 else text[:117] + "..."


def check(root, doc_roots):
    backend_dir = os.path.join(root, "backend")
    if not os.path.isdir(backend_dir):
        print("FAIL 读不到 %s：本脚本只在主仓根目录下有意义" % os.path.relpath(backend_dir, root))
        return 1

    api_routes, unresolved, group_count, api_prefix = scan_backend_routes(backend_dir, root)
    openapi_rel = os.path.join("backend", "internal", "catalog", "openapi.go")
    openapi_routes = scan_openapi_routes(os.path.join(root, openapi_rel), root)

    implemented = {}
    for (method, path) in list(api_routes) + list(openapi_routes):
        implemented.setdefault(path, set()).add(method)
    implemented_paths = set(implemented)

    if len(implemented_paths) < 10:
        # 抽取失效时不能把它当成"文档全错"：那会一次喷出几百条假 P1，把真问题淹掉
        print("FAIL 从 backend 只抽到 %d 条路由，路由来源明显失效（不是文档的问题）" % len(implemented_paths))
        print("     解析出的组前缀：%s" % api_prefix)
        return 1
    for item in unresolved:
        print("note 未能确定组前缀，该组路径按原样登记（可能是新出现的组嵌套形态）：%s" % item)

    refs, skipped, negated, scanned = collect_refs(doc_roots, root)
    if not refs and not skipped:
        print("SKIP 文档与技能目录里没有取到任何 /api 端点引用")
        return 0
    p1, other = compare(implemented_paths, refs)

    print("路由来源：backend 实现 %d 条（前缀变量 %d 个，解析出的组前缀 %s）+ %s 声明 %d 条，去重后 %d 条路径"
          % (len(api_routes), group_count, api_prefix or "(空)", openapi_rel.replace(os.sep, "/"),
             len(openapi_routes), len(implemented_paths)))
    reasons = {}
    for item in skipped:
        reasons[item[1]] = reasons.get(item[1], 0) + 1
    print("文档来源：%s；共 %d 个 Markdown 文件，%d 条 /api 引用，忽略 %d 条，反例引用 %d 条"
          % ("、".join("%s 仓库 %d 个文件" % (label, count) for label, count in scanned),
             sum(count for _label, count in scanned), len(refs), len(skipped), len(negated)))
    if reasons:
        print("      忽略构成：%s" % "、".join("%s %d 条" % (reason, count) for reason, count in sorted(reasons.items())))

    if p1:
        print("")
        print("P1：文档写了、主仓实现里没有的 /api 端点（%d 条，按引用位置逐条核对）" % len(p1))
        for path in sorted(p1):
            places = p1[path]
            print("  FAIL %s —— 文档 %d 处引用，backend 未注册任何方法" % (path, len(places)))
            for ref in places[:4]:
                print("       证据 %s:%d  %s" % (ref[REF_FILE], ref[REF_LINE], snippet(ref[REF_TEXT])))

    if other:
        print("")
        print("其他服务前缀的 /api 引用（只报告，不判 P1；归属见 docs/architecture/service-split-migration.md §2 表）")
        for key in sorted(other):
            owner, path = key
            places = other[key]
            print("  note [%s] %s —— 文档 %d 处，示例 %s:%d"
                  % (owner, path, len(places), places[0][REF_FILE], places[0][REF_LINE]))

    if negated:
        print("")
        print("反例引用（否定标记紧邻该引用，例如「没有 /api/search」「不是 /api/catalog/importer」；"
              "只报告不判 P1，行原文在下面，需要人工确认它是否真在否定这个路径）")
        for key in sorted({ref[REF_NORM]: ref for ref in negated}.values(), key=lambda ref: ref[REF_NORM]):
            print("  note %s —— 示例 %s:%d  %s" % (key[REF_NORM], key[REF_FILE], key[REF_LINE], snippet(key[REF_TEXT])))

    # 反查只针对 /api：/healthz、/ready 这类探针本来就不该进教程，列出来只会稀释报告；
    # 反例引用也算"文档提过"（它确实出现在文档里，只是被讲成调不通）
    mentioned = {ref[REF_NORM] for ref in refs + negated}
    unused = sorted(path for path in implemented_paths if path.startswith("/api") and path not in mentioned)
    print("")
    print("反向报告：/api 下实现里有、文档与技能都没写的路径 %d 条（只报告，不影响退出码）" % len(unused))
    for path in unused:
        methods = ",".join(sorted(implemented[path]))
        print("  note 未文档化 %s %s" % (methods, path))

    print("")
    print("check_doc_routes: 实现路径 %d 条、文档引用 %d 条、P1 %d 条、其他服务引用 %d 条、"
          "反例引用 %d 条、未文档化 %d 条"
          % (len(implemented_paths), len(refs), len(p1), len(other), len(negated), len(unused)))
    return 1 if p1 else 0


SELFTEST_ROUTES = {"/api/catalog/entities", "/api/catalog/entities/*", "/api/version"}
SELFTEST_DOC = """
# 假文档

正文里写 GET /api/catalog/entities 与 `PUT /api/catalog/entities/{id}`。

| 端点 | 说明 |
| --- | --- |
| `POST /api/catalog/entities/:id` | 冒号写法 |
| [详情](https://example.com/api/catalog/entities/42) | 具体数字 |
| `/api/catalog/entities/9f1c2f5e-1a2b-4c3d-8e4f-5a6b7c8d9e0f` | UUID |
| `/api/catalog/ghost-endpoint` | 实现里没有 → P1 |
| `/api/auth/password` | 账号服务 → 只报告 |
| `/api/storage/upload` | 存储服务 → 只报告 |
| `/api/catalog/*`、`/api/catalog/`、`/api/v1` | 忽略 |
| `/api/admin/{users,groups}` | 花括号列表简写 → 忽略 |
| `/api/catalog/entities?limit=10&offset=0` | 查询串要丢掉 |

没有独立的 `/api/search` 端点（也没有 `/api/browse/*` 兼容层）。
导入端点的前缀是 `/api/importer`，实体查询走 `/api/catalog/entities`。

```bash
curl -X DELETE /api/catalog/entities/7
```
"""


def selftest():
    """离线自测：假路由表 + 假文档文本，验证归一化与两个方向的判定都不依赖兄弟仓库。"""
    failures, checks = [], 0

    def expect(label, got, want):
        nonlocal checks
        checks += 1
        if got != want:
            failures.append("%s: 期望 %r，实际 %r" % (label, want, got))

    expect("实现侧 :id 归一化", {norm_path(p) for p in SELFTEST_ROUTES},
           {"/api/catalog/entities", "/api/catalog/entities/*", "/api/version"})
    for raw in ("/api/catalog/entities/:id", "/api/catalog/entities/{id}", "/api/catalog/entities/42",
                "/api/catalog/entities/9f1c2f5e-1a2b-4c3d-8e4f-5a6b7c8d9e0f"):
        expect("归一化 " + raw, norm_path(raw), "/api/catalog/entities/*")
    expect("查询串丢弃", norm_path("/api/catalog/entities?limit=10&offset=0"), "/api/catalog/entities")
    expect("锚点丢弃", norm_path("/api/catalog/entities#top"), "/api/catalog/entities")

    refs, skipped, negated = extract_refs(SELFTEST_DOC, "selftest.md")
    expect("忽略原因集合", sorted({item[1] for item in skipped}),
           sorted(["前缀说明（讲的是分组前缀，不是端点）", "花括号列表简写（前缀枚举，不是单条端点）",
                   "纯前缀", "文档声明不存在的版本前缀", "通配符片段"]))
    expect("归一化后的引用集合", sorted({ref[REF_NORM] for ref in refs}),
           ["/api/auth/password", "/api/catalog/entities", "/api/catalog/entities/*",
            "/api/catalog/ghost-endpoint", "/api/storage/upload"])
    expect("前缀说明被忽略、同行的正常引用保留",
           [item[0] for item in skipped if item[1].startswith("前缀说明")], ["/api/importer"])
    expect("反例引用不进比对", sorted({ref[REF_NORM] for ref in negated}), ["/api/search"])
    expect("代码块里的引用也算", any(ref[REF_TEXT].startswith("curl -X DELETE") for ref in refs), True)
    expect("证据带行号", all(ref[REF_LINE] > 0 and ref[REF_FILE] == "selftest.md" for ref in refs), True)

    p1, other = compare(SELFTEST_ROUTES, refs)
    expect("方向一：只有实现里没有的目录端点进 P1", sorted(p1), ["/api/catalog/ghost-endpoint"])
    expect("方向一：归一化命中的四种写法都不进 P1",
           [path for path in p1 if path.endswith("entities") or path.endswith("entities/*")], [])
    expect("其他服务前缀不判 P1", sorted(other), [("auth", "/api/auth/password"), ("storage", "/api/storage/upload")])
    expect("方向二：实现里有、文档没写的只报告",
           sorted(SELFTEST_ROUTES - {ref[REF_NORM] for ref in refs} - set(p1) - {path for _o, path in other}),
           ["/api/version"])
    expect("方向二不产生 P1 以外的影响", len(p1) + len(other), 3)

    for bad in failures:
        print("FAIL " + bad)
    print("check_doc_routes --selftest: %d 项用例，%d 个问题" % (checks, len(failures)))
    return 1 if failures else 0


def resolve_doc_roots(root, siblings_root, docs_repo, skills_repo):
    """兄弟仓库缺席不失败：主仓单独检出（只拿主仓的机器、未接线文档的 CI job）本来就看不到它们。"""
    roots, missing = [], []
    for label, override, subdir in (("docs", docs_repo, "docs"), ("skills", skills_repo, "")):
        if override:
            base = os.path.abspath(os.path.join(root, override))
        elif siblings_root:
            base = os.path.join(os.path.abspath(siblings_root), os.path.basename(SIBLING_DEFAULTS[label]))
        else:
            base = os.path.normpath(os.path.join(root, SIBLING_DEFAULTS[label]))
        if subdir:
            base = os.path.join(base, subdir)
        if os.path.isdir(base):
            roots.append((label, base))
        else:
            missing.append((label, base))
    return roots, missing


def main():
    if "--selftest" in sys.argv[1:]:
        return selftest()
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(description="文档端点引用 ↔ 主仓路由清单一致性检查")
    ap.add_argument("root", nargs="?", default=os.path.dirname(here), help="仓库根目录")
    ap.add_argument("--siblings-root", default=os.environ.get("MF_SIBLINGS_ROOT"),
                    help="兄弟仓库所在根目录（默认按 ../metafusion-* 解析）")
    ap.add_argument("--docs-repo", default=None, help="文档仓库目录（默认 <root>/../metafusion-docs）")
    ap.add_argument("--skills-repo", default=None, help="技能仓库目录（默认 <root>/../metafusion-skills）")
    args = ap.parse_args()

    root = os.path.abspath(args.root)
    doc_roots, missing = resolve_doc_roots(root, args.siblings_root, args.docs_repo, args.skills_repo)
    for label, base in missing:
        print("SKIP %s 仓库不在本机（%s）：跳过该来源，不按失败处理" % (label, base))
    if not doc_roots:
        print("check_doc_routes: 0 条文档引用，全部来源缺席")
        return 0
    return check(root, doc_roots)


if __name__ == "__main__":
    sys.exit(main())
