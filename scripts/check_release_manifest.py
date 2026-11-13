#!/usr/bin/env python3
"""发布清单完整性校验：部署前拒绝不完整清单（只用标准库，部署机无 pyyaml 也能跑）。

判据（结构问题永远失败；未知 digest 只在 --strict 下失败）：
- schema 必须是 metafusion-release-manifest/v2（v1 没有每服务独立引用，视为不完整）；
- sha/ref/versions_lock（跨仓版本组合）/backend_migrations 缺一不可，versions_lock 为空即失败；
- 每个服务（backend/migrator/frontend）必须有 repository、非空 tags、digest；
  digest 要么是合法 sha256:64位十六进制，要么是"待填"（未知如实标记）；
- migrator 必须带 same_source 同源绑定且 git_sha 与清单 sha 一致（迁移器与运行镜像同源，
  不许各拉各的版本）；
- --strict：任一 digest/任一 tags 为"待填"即失败（流水线与部署门禁用它）；
- --expect-tag TAG：TAG 必须落在每个服务的 tags 冒号后缀里；它只校验清单归属，
  标签可能被重指，部署必须使用下面生成的 digest 覆盖。
- --expect-lock PATH：清单的 versions_lock 必须与该锁文件逐条一致
  （部分旧版本/多出项即失败，生产 pull 用它核对兄弟版本组合）。
- --expect-sha SHA：清单源码提交必须与部署机主仓检出一致。
- --print-pull-refs：校验通过后按每服务 digest 构造 repository@sha256 引用并打印
  （digest 未知或非法时拒绝打印）。
- --write-compose-override PATH：校验通过后写出 backend、backend-migrate、frontend 的
  digest 镜像覆盖；部署的拉取、迁移与启动均使用该覆盖文件。

用法：
    python3 scripts/check_release_manifest.py [--strict] [--expect-tag TAG] [--expect-lock PATH] [清单路径]
    python3 scripts/check_release_manifest.py --print-pull-refs [--expect-tag TAG] [清单路径]
    python3 scripts/check_release_manifest.py --selftest
""";

import argparse
import json
import os
import re
import sys

SERVICES = ("backend", "migrator", "frontend")
UNKNOWN = "待填"
SCHEMA = "metafusion-release-manifest/v2"
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")


def parse_block(lines, start, indent):
    """极简 YAML 子集解析：只认本清单生成器写出的形态（2 空格缩进、key: value、
    嵌套 map、- 标量列表、# 整行注释）。返回值 (node, next_index)。"""
    node = {}
    i = start
    while i < len(lines):
        raw = lines[i]
        stripped = raw.strip()
        if not stripped or stripped.startswith("#") or stripped == "---":
            i += 1
            continue
        cur = len(raw) - len(raw.lstrip(" "))
        if cur < indent:
            break
        if cur > indent or stripped.startswith("- "):
            break
        if ":" not in stripped:
            raise ValueError("第 %d 行不是 key: value：%r" % (i + 1, raw))
        key, _, tail = stripped.partition(":")
        key = key.strip().strip("'\"")
        tail = tail.strip()
        if tail == "":
            child, nxt = parse_block(lines, i + 1, indent + 2)
            # 紧跟的 - 列表：先看是不是列表
            j = i + 1
            while j < len(lines) and (not lines[j].strip() or lines[j].strip().startswith("#")):
                j += 1
            if j < len(lines) and lines[j].strip().startswith("- "):
                items = []
                while j < len(lines) and lines[j].strip().startswith("- "):
                    items.append(lines[j].strip()[2:].strip().strip("'\"").strip('"'))
                    j += 1
                node[key] = items
                i = j
                continue
            node[key] = child
            i = nxt
            continue
        node[key] = tail.strip("'\"").strip('"')
        i += 1
    return node, i


def load_manifest(path):
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    if "\t" in text:
        raise ValueError("清单含制表符缩进，拒绝解析")
    doc, _ = parse_block(text.splitlines(), 0, 0)
    return doc


def check(doc, strict=False, expect_tag=None, expect_sha=None):
    problems, warnings = [], []
    if doc.get("schema") != SCHEMA:
        problems.append("schema 应为 %s，实际 %r（旧清单没有每服务独立引用，视为不完整）"
                        % (SCHEMA, doc.get("schema")))
    for key in ("sha", "ref", "versions_lock", "backend_migrations"):
        if not doc.get(key):
            problems.append("缺少必填项 %s" % key)
    if expect_sha and doc.get("sha") != expect_sha:
        problems.append("清单 sha 与当前检出不一致：清单 %s，检出 %s" %
                        (str(doc.get("sha", ""))[:12], expect_sha[:12]))
    lock = doc.get("versions_lock")
    if isinstance(lock, dict) and not lock:
        problems.append("versions_lock 为空：跨仓版本组合是部署对账依据，不许缺")
    images = doc.get("images")
    if not isinstance(images, dict):
        problems.append("缺少 images 映射")
        images = {}
    for name in SERVICES:
        entry = images.get(name)
        if not isinstance(entry, dict):
            problems.append("images.%s 缺失" % name)
            continue
        if not entry.get("repository"):
            problems.append("images.%s 缺少 repository" % name)
        tags = entry.get("tags")
        if not isinstance(tags, list) or not tags:
            problems.append("images.%s 缺少 tags 列表" % name)
            tags = []
        digest = entry.get("digest", "")
        if digest == UNKNOWN or UNKNOWN in tags:
            warnings.append("images.%s 含待填（digest 或 tags 未知，未伪造）" % name)
        elif not DIGEST_RE.match(digest or ""):
            problems.append("images.%s 的 digest 非法：%r（合法形如 sha256:<64位十六进制>）"
                            % (name, digest))
        if expect_tag:
            if not any(t.rsplit(":", 1)[-1] == expect_tag for t in tags if isinstance(t, str)):
                problems.append("images.%s 的 tags 里没有 :%s（IMAGE_TAG 不是本次清单的 tag）"
                                % (name, expect_tag))
    mig = images.get("migrator")
    if isinstance(mig, dict):
        same = mig.get("same_source")
        if not isinstance(same, dict) or same.get("service") != "backend":
            problems.append("migrator 缺少指向 backend 的 same_source 同源绑定")
        elif doc.get("sha") and same.get("git_sha") != doc.get("sha"):
            problems.append("migrator 同源 sha %r 与清单 sha %r 不一致"
                            % (same.get("git_sha"), doc.get("sha")))
    if strict and warnings:
        problems.extend(warnings)
    return problems, ([] if strict else warnings)


def read_lock_file(path):
    lock = {}
    with open(path, encoding='utf-8') as fh:
        for line in fh:
            text = line.split('#', 1)[0].strip()
            if text and '=' in text:
                repo, sha = (p.strip() for p in text.split('=', 1))
                if repo and sha:
                    lock[repo] = sha
    return lock


def check_lock_match(manifest_lock, file_lock):
    problems = []
    if not isinstance(manifest_lock, dict):
        return ['versions_lock 不是映射，无法与锁文件比对']
    for repo, sha in sorted(file_lock.items()):
        got = manifest_lock.get(repo)
        if got is None:
            problems.append('versions_lock 缺少 %s（锁文件要求 %s）' % (repo, sha[:12]))
        elif got != sha:
            problems.append('versions_lock %s 过旧：清单 %s，锁文件 %s（部分旧版本拒绝）' % (repo, str(got)[:12], sha[:12]))
    for repo in sorted(manifest_lock.keys()):
        if repo not in file_lock:
            problems.append('versions_lock 多出 %s（锁文件里没有，组合不一致）' % repo)
    return problems


def pull_refs(doc):
    refs, bad = [], []
    images = doc.get('images') or {}
    for name in SERVICES:
        entry = images.get(name) or {}
        repo = entry.get('repository') or ''
        digest = entry.get('digest') or ''
        if not repo:
            bad.append('images.%s 缺少 repository（无法构造 digest 引用）' % name)
        elif not DIGEST_RE.match(digest or ''):
            bad.append('images.%s 的 digest 未知或非法，拒绝按 tag 拉取' % name)
        else:
            refs.append('%s@%s' % (repo, digest))
    return refs, bad


def compose_override(doc):
    refs, bad = pull_refs(doc)
    if bad:
        return "", bad
    services = (("backend", refs[0]), ("agent-worker", refs[0]), ("backend-migrate", refs[1]), ("frontend", refs[2]))
    lines = ["services:"]
    for name, ref in services:
        lines.extend(("  %s:" % name, "    image: %s" % json.dumps(ref)))
    return "\n".join(lines) + "\n", []


def selftest():
    failures = []

    def want(cond, label):
        if not cond:
            failures.append(label)
            print("  FAIL " + label)

    good_digest = "sha256:" + "b" * 64
    base = {
        "schema": SCHEMA, "sha": "abc", "ref": "refs/tags/v1",
        "versions_lock": {".": "abc"},
        "backend_migrations": ["000001_x.up.sql"],
        "images": {
            "backend": {"repository": "ghcr.io/m/backend", "tags": ["ghcr.io/m/backend:v1"], "digest": good_digest},
            "migrator": {"repository": "ghcr.io/m/migrator", "tags": ["ghcr.io/m/migrator:v1"],
                         "digest": good_digest, "same_source": {"service": "backend", "git_sha": "abc"}},
            "frontend": {"repository": "ghcr.io/m/frontend", "tags": ["ghcr.io/m/frontend:v1"], "digest": good_digest},
        },
    }
    import copy
    import tempfile
    p, w = check(copy.deepcopy(base))
    want(p == [] and w == [], "完整清单非 strict 通过")
    p, _ = check(copy.deepcopy(base), strict=True)
    want(p == [], "完整清单 strict 通过")
    bad = copy.deepcopy(base)
    bad["images"]["frontend"]["digest"] = UNKNOWN
    p, w = check(bad)
    want(p == [] and len(w) == 1, "待填非 strict 只警告")
    p, _ = check(bad, strict=True)
    want(len(p) == 1, "待填 strict 拒绝")
    bad2 = copy.deepcopy(base)
    bad2["images"]["migrator"]["same_source"]["git_sha"] = "other"
    p, _ = check(bad2, strict=True)
    want(any("同源" in x for x in p), "同源绑定不一致被拦")
    bad3 = copy.deepcopy(base)
    bad3["images"]["backend"]["digest"] = "sha256:xyz"
    p, _ = check(bad3)
    want(any("非法" in x for x in p), "伪造形 digest 被拦")
    p, _ = check(copy.deepcopy(base), expect_tag="v1")
    want(p == [], "expect-tag 命中通过")
    p, _ = check(copy.deepcopy(base), expect_tag="latest")
    want(any("latest" in x for x in p), "expect-tag 落空被拦")
    p, _ = check(copy.deepcopy(base), expect_sha="other")
    want(any("sha 与当前检出不一致" in x for x in p), "expect-sha 与当前检出不符被拦")
    # 负向 1/3：缺清单——文件不存在即解析失败，部署必须中止而不是警告放行
    try:
        load_manifest(os.path.join(tempfile.gettempdir(), "metafusion-no-such-manifest.yaml"))
        want(False, "缺清单应抛异常")
    except OSError:
        want(True, "缺清单抛异常")
    except Exception as exc:
        want(False, "缺清单应为 OSError，实际 %r" % exc)
    empty = copy.deepcopy(base)
    del empty["images"]["backend"]
    p, _ = check(empty, strict=True)
    want(any("backend" in x for x in p), "缺服务镜像被拦")
    # 负向 2/3：tag 被重指——清单里的 tag 与本次 IMAGE_TAG 不是同一个
    retagged = copy.deepcopy(base)
    retagged["images"]["backend"]["tags"] = ["ghcr.io/m/backend:v999"]
    retagged["images"]["migrator"]["tags"] = ["ghcr.io/m/migrator:v999"]
    retagged["images"]["frontend"]["tags"] = ["ghcr.io/m/frontend:v999"]
    p, _ = check(retagged, expect_tag="v1")
    want(len(p) == 3, "tag 被重指三服务同拦")
    # 负向 3/3：部分旧版本——清单 versions_lock 与部署锁逐条比对
    p = check_lock_match({".": "abc"}, {".": "abc"})
    want(p == [], "锁一致通过")
    p = check_lock_match({".": "abc"}, {".": "abc", "../metafusion-auth": "def456"})
    want(any("metafusion-auth" in x for x in p), "清单缺仓库项被拦")
    p = check_lock_match({".": "abc", "../metafusion-auth": "old111"}, {".": "abc", "../metafusion-auth": "def456"})
    want(any("过旧" in x for x in p), "部分旧版本被拦")
    p = check_lock_match("not-a-dict", {".": "abc"})
    want(len(p) == 1, "锁非映射被拦")
    # digest 引用构造：合法逐服务输出 repository@digest，未知拒绝
    refs, bad = pull_refs(copy.deepcopy(base))
    want(bad == [] and len(refs) == 3 and all("@" + good_digest in r for r in refs), "digest 引用逐服务构造")
    unk = copy.deepcopy(base)
    unk["images"]["frontend"]["digest"] = UNKNOWN
    refs, bad = pull_refs(unk)
    want(refs == [] or len(bad) == 1, "未知 digest 拒绝构造引用")
    override, bad = compose_override(copy.deepcopy(base))
    want(bad == [] and 'image: "ghcr.io/m/backend@' + good_digest + '"' in override,
         "Compose 覆盖使用后端 digest")
    want('agent-worker:\n    image: "ghcr.io/m/backend@' + good_digest + '"' in override,
         "Compose 覆盖同步钉死 agent-worker digest")
    want('backend-migrate:\n    image: "ghcr.io/m/migrator@' + good_digest + '"' in override,
         "Compose 覆盖使用迁移器 digest")
    override, bad = compose_override(unk)
    want(override == "" and len(bad) == 1, "未知 digest 不生成 Compose 覆盖")
    # 生成器→校验器往返：解析手写 YAML 子集
    text = ("schema: " + SCHEMA + "\nsha: abc\nref: r\n"
            "versions_lock:\n  .: abc\nbackend_migrations:\n  - 000001_x.up.sql\n"
            "images:\n  backend:\n    repository: ghcr.io/m/backend\n"
            "    tags:\n      - ghcr.io/m/backend:v1\n    digest: " + good_digest + "\n")
    try:
        import tempfile
        with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False, encoding="utf-8") as fh:
            fh.write(text)
            tmp = fh.name
        doc = load_manifest(tmp)
        os.unlink(tmp)
        want(doc["images"]["backend"]["digest"] == good_digest, "往返解析 digest")
        want(doc["backend_migrations"] == ["000001_x.up.sql"], "往返解析列表")
        want(doc["versions_lock"] == {".": "abc"}, "往返解析嵌套 map")
    except Exception as exc:  # noqa: BLE001 —— 自测失败要报出来
        want(False, "往返解析抛异常：%s" % exc)
    print("check_release_manifest --selftest: %d 个问题" % len(failures))
    return 1 if failures else 0


def main():
    ap = argparse.ArgumentParser(description="校验 release-manifest.yaml 完整性")
    ap.add_argument("manifest", nargs="?", default="release-manifest.yaml")
    ap.add_argument("--strict", action="store_true")
    ap.add_argument("--expect-tag", default=None)
    ap.add_argument("--expect-lock", default=None)
    ap.add_argument("--expect-sha", default=None)
    ap.add_argument("--print-pull-refs", action="store_true")
    ap.add_argument("--write-compose-override", default=None)
    ap.add_argument("--root", default=None)
    args = ap.parse_args()
    path = args.manifest
    if args.root and not os.path.isabs(path):
        path = os.path.join(os.path.abspath(args.root), path)
    if not os.path.isfile(path):
        print("FAIL 读不到清单 %s" % path)
        print("check_release_manifest: 1 个问题")
        return 1
    try:
        doc = load_manifest(path)
    except Exception as exc:  # noqa: BLE001 —— 解析失败本身就是问题
        print("FAIL 清单解析失败：%s" % exc)
        print("check_release_manifest: 1 个问题")
        return 1
    problems, warnings = check(doc, strict=args.strict, expect_tag=args.expect_tag,
                               expect_sha=args.expect_sha)
    if args.expect_lock:
        try:
            file_lock = read_lock_file(args.expect_lock if os.path.isabs(args.expect_lock) else os.path.join(os.path.abspath(args.root or "."), args.expect_lock))
        except OSError as exc:
            print("FAIL 读不到锁文件 %s：%s" % (args.expect_lock, exc))
            print("check_release_manifest: 1 个问题，0 项待填警告")
            return 1
        problems.extend(check_lock_match(doc.get("versions_lock"), file_lock))
    if args.print_pull_refs or args.write_compose_override:
        refs, bad = pull_refs(doc)
        for x in bad:
            print("FAIL " + x)
        if bad:
            print("check_release_manifest: %d 个问题，%d 项待填警告" % (len(problems) + len(bad), len(warnings)))
            return 1
        for x in warnings:
            print("warning " + x)
        for x in problems:
            print("FAIL " + x)
        if problems:
            print("check_release_manifest: %d 个问题，%d 项待填警告" % (len(problems), len(warnings)))
            return 1
        if args.write_compose_override:
            override, bad = compose_override(doc)
            if bad:
                for x in bad:
                    print("FAIL " + x)
                return 1
            with open(args.write_compose_override, "w", encoding="utf-8") as fh:
                fh.write(override)
            print("已生成按摘要运行的 Compose 覆盖：backend / agent-worker / backend-migrate / frontend")
        if args.print_pull_refs:
            for ref in refs:
                print(ref)
        return 0
    for x in warnings:
        print("warning " + x)
    for x in problems:
        print("FAIL " + x)
    print("check_release_manifest: %d 个问题，%d 项待填警告" % (len(problems), len(warnings)))
    return 1 if problems else 0


if __name__ == "__main__":
    if "--selftest" in sys.argv[1:]:
        sys.exit(selftest())
    sys.exit(main())
