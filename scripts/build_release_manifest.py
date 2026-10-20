#!/usr/bin/env python3
"""发布清单生成：把 build-push-action 的实际 digest 产物收进 release-manifest.yaml。

为什么需要它：release.yml 曾在清单里给三个镜像 digest 手写"待填"，部署侧只能再去人肉
解析 digest——写错/过期都不会有人拦。本脚本只做一件事：把流水线上一步下载下来的
digest 产物（每个镜像一个 .digest 文件与一个 .tags 文件）原样收进清单，不推测、不编造。

输入（--digests-dir，来自 actions/download-artifact 的 digest-* 合并产物）：
    <服务>.digest   # build-push-action 的 outputs.digest，一行，如 sha256:abc…
    <服务>.tags     # metadata-action 的 outputs.tags，原样多行/逗号分隔都认

未知 digest 一律标"待填"，绝不伪造：缺文件、空文件、格式不对都记待填并打印 warning，
是否放行由 scripts/check_release_manifest.py（--strict）与 deploy.sh pull 门禁决定，
本脚本永远不因为"缺 digest"自己失败——缺失是事实，要如实记录。

跨仓版本组合直接读 deploy/versions.lock（与 check_versions.py 同一份锁）；
目录库迁移文件列 backend/migrations（部署与回滚对账用）。

用法：
    python3 scripts/build_release_manifest.py [--digests-dir digests-in] [--out release-manifest.yaml]
    python3 scripts/build_release_manifest.py --selftest
""";

import argparse
import os
import re
import sys
import tempfile

import yaml  # release.yml 在调用前 pip 安装 pyyaml； checker 侧刻意只用标准库

SERVICES = ("backend", "migrator", "frontend")
UNKNOWN = "待填"
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")


def read_text(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read().strip()
    except OSError:
        return ""


def parse_tags(text):
    """meta.outputs.tags 可能是换行/逗号/空格分隔，原样拆成引用列表。"""
    return [t for t in re.split(r"[\s,]+", text.strip()) if t]


def read_versions_lock(path):
    lock = {}
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            text = line.split("#", 1)[0].strip()
            if text and "=" in text:
                repo, sha = (p.strip() for p in text.split("=", 1))
                if repo and sha:
                    lock[repo] = sha
    return lock


def build_manifest(digests_dir, lock_path, migrations_dir, env):
    prefix = env.get("IMAGE_PREFIX", "ghcr.io/moeclub-open/metafusion")
    sha = env.get("GITHUB_SHA", "")
    ref = env.get("GITHUB_REF", "")
    lock = read_versions_lock(lock_path)
    if not lock:
        raise SystemExit("FAIL 锁文件 %s 为空或读不到：版本组合是清单必填项" % lock_path)
    migrations = sorted(os.listdir(migrations_dir)) if os.path.isdir(migrations_dir) else []
    images = {}
    for name in SERVICES:
        raw_digest = read_text(os.path.join(digests_dir, name + ".digest"))
        tags = parse_tags(read_text(os.path.join(digests_dir, name + ".tags")))
        digest = raw_digest if DIGEST_RE.match(raw_digest) else UNKNOWN
        entry = {
            "repository": "%s/%s" % (prefix, name),
            "tags": tags if tags else [UNKNOWN],
            "digest": digest,
        }
        if name == "migrator":
            # 迁移器同源绑定：与 backend 同一次提交、同一份 backend 上下文构建，
            # 只换 Dockerfile target；部署时两者必须同源，不许各拉各的版本。
            entry["same_source"] = {"service": "backend", "git_sha": sha}
        images[name] = entry
    return {
        "schema": "metafusion-release-manifest/v2",
        "repo": "MoeclubM/MetaFusion",
        "ref": ref,
        "sha": sha,
        "versions_lock": lock,
        "backend_migrations": migrations,
        "images": images,
        "digest_note": "digest 只认 build-push-action 实际产物：未知一律标待填，绝不伪造；"
                       "部署前由 check_release_manifest.py --strict 拒绝不完整清单；"
                       "回滚按本清单的 sha 重打 tag，不复用 latest",
    }


def main():
    ap = argparse.ArgumentParser(description="生成 release-manifest.yaml")
    ap.add_argument("--digests-dir", default="digests-in")
    ap.add_argument("--lock", default=None)
    ap.add_argument("--migrations-dir", default=None)
    ap.add_argument("--out", default="release-manifest.yaml")
    ap.add_argument("--root", default=None)
    args = ap.parse_args()
    root = os.path.abspath(args.root or os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
    manifest = build_manifest(
        os.path.join(root, args.digests_dir) if not os.path.isabs(args.digests_dir) else args.digests_dir,
        args.lock or os.path.join(root, "deploy", "versions.lock"),
        args.migrations_dir or os.path.join(root, "backend", "migrations"),
        os.environ,
    )
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write("# 由 scripts/build_release_manifest.py 生成：一次发布“是什么”只认这一份文件。\n")
        yaml.safe_dump(manifest, fh, allow_unicode=True, sort_keys=False)
    unknowns = sorted(n for n, e in manifest["images"].items() if e["digest"] == UNKNOWN)
    for n in unknowns:
        print("warning %s 的 digest 未知，已标待填（不伪造，放行与否由 --strict 门禁决定）" % n)
    for n, e in manifest["images"].items():
        print("%s: %s digest=%s tags=%d" % (n, e["repository"], e["digest"][:19], len(e["tags"])))
    print("build_release_manifest: %d 个服务，%d 个未知" % (len(SERVICES), len(unknowns)))
    return 0


def selftest():
    failures = []

    def want(cond, label):
        if not cond:
            failures.append(label)
            print("  FAIL " + label)

    want(parse_tags("a:1\nb:2\n") == ["a:1", "b:2"], "tags 换行分隔")
    want(parse_tags("a:1,b:2 c:3") == ["a:1", "b:2", "c:3"], "tags 混合分隔")
    want(parse_tags("") == [], "tags 空串")
    with tempfile.TemporaryDirectory() as tmp:
        lock = os.path.join(tmp, "versions.lock")
        with open(lock, "w", encoding="utf-8") as fh:
            fh.write("# 注\n.=abc123\n../metafusion-auth=def456\n")
        mig = os.path.join(tmp, "migrations")
        os.mkdir(mig)
        open(os.path.join(mig, "000001_x.up.sql"), "w").close()
        dig = os.path.join(tmp, "digests-in")
        os.mkdir(dig)
        good = "sha256:" + "a" * 64
        with open(os.path.join(dig, "backend.digest"), "w") as fh:
            fh.write(good + "\n")
        with open(os.path.join(dig, "backend.tags"), "w") as fh:
            fh.write("ghcr.io/m/metafusion/backend:sha-abc\n")
        # migrator 缺文件、frontend 写坏值：都要落成待填，不能抛异常
        with open(os.path.join(dig, "frontend.digest"), "w") as fh:
            fh.write("not-a-digest\n")
        env = {"IMAGE_PREFIX": "ghcr.io/m/metafusion", "GITHUB_SHA": "abc123", "GITHUB_REF": "refs/heads/main"}
        m = build_manifest(dig, lock, mig, env)
        want(m["schema"] == "metafusion-release-manifest/v2", "schema 版本")
        want(m["images"]["backend"]["digest"] == good, "合法 digest 原样收录")
        want(m["images"]["migrator"]["digest"] == UNKNOWN, "缺文件标待填")
        want(m["images"]["frontend"]["digest"] == UNKNOWN, "坏值标待填不伪造")
        want(m["images"]["migrator"]["same_source"] == {"service": "backend", "git_sha": "abc123"},
             "迁移器同源绑定")
        want(m["versions_lock"].get("../metafusion-auth") == "def456", "版本锁组合收录")
        want(m["backend_migrations"] == ["000001_x.up.sql"], "迁移文件列表")
    print("build_release_manifest --selftest: %d 个问题" % len(failures))
    return 1 if failures else 0


if __name__ == "__main__":
    if "--selftest" in sys.argv[1:]:
        sys.exit(selftest())
    sys.exit(main())
