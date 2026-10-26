#!/usr/bin/env python3
"""清理本项目闲置镜像；从标准输入读取包含全部 profiles 的 Compose JSON。"""
import argparse
import json
import re
import subprocess
import sys


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def inspect_images(refs):
    return json.loads(docker("image", "inspect", *refs)) if refs else []


def repository(ref):
    name = ref.split("@", 1)[0]
    return name.rsplit(":", 1)[0] if ":" in name.rsplit("/", 1)[-1] else name


def cleanup_candidates(config, images, protected):
    project = config["name"]
    repositories = {
        repository(service.get("image") or project + "-" + name)
        for name, service in config["services"].items() if service.get("build")
    }

    def owned_tag(tag):
        repo = repository(tag)
        return repo in repositories or repo.startswith((
            "metafusion-", "ghcr.io/moeclub-open/metafusion/", "ghcr.io/moeclubm/metafusion/",
            "ghcr.io/moeclub-open/metafusion-", "ghcr.io/moeclubm/metafusion-",
        ))

    candidates = []
    for image in images:
        if image["Id"] in protected:
            continue
        labels = (image.get("Config") or {}).get("Labels") or {}
        source = labels.get("org.opencontainers.image.source", "")
        tags = image.get("RepoTags") or []
        owned_tags = [tag for tag in tags if owned_tag(tag)]
        owned = labels.get("com.docker.compose.project") == project or re.fullmatch(
            r"https://github\.com/(?:moeclubm|moeclub-open)/metafusion(?:-[a-z0-9-]+)?(?:\.git)?/?",
            source, re.I,
        ) or owned_tags
        if owned and (not tags or owned_tags):
            if not tags and any(not owned_tag(ref) for ref in image.get("RepoDigests") or []):
                continue
            candidates.append({**image, "CleanupRefs": owned_tags or [image["Id"]]})
    return candidates


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    config = json.load(sys.stdin)

    # 未启动的一次性迁移器也属于当前配置。任何引用无法解析时停止清理。
    refs = {service["image"] for service in config["services"].values() if service.get("image")}
    protected = {image["Id"] for image in inspect_images(sorted(refs))}
    containers = docker("ps", "-aq").splitlines()
    if containers:
        refs = docker("inspect", "--format", "{{.Image}}", *containers).splitlines()
        protected.update(image["Id"] for image in inspect_images(sorted(set(refs))))

    ids = sorted(set(docker("image", "ls", "-aq", "--no-trunc").splitlines()))
    candidates = cleanup_candidates(config, inspect_images(ids), protected)
    for image in candidates:
        refs = image["CleanupRefs"]
        if args.dry_run:
            print("would remove " + ", ".join(refs))
        else:
            subprocess.run(["docker", "image", "rm", "--no-prune", *refs], check=True)
    print("project images: %d %s, %d protected" % (
        len(candidates), "eligible" if args.dry_run else "removed", len(protected),
    ))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (subprocess.CalledProcessError, ValueError, KeyError) as error:
        print("镜像清理失败：" + str(error), file=sys.stderr)
        sys.exit(1)
