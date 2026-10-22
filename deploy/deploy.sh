#!/usr/bin/env bash
# ==============================================================================
# MetaFusion 极速部署与智能运维工具 (Smart Fast Deployment Tool)
# ==============================================================================

set -e
# 绝对脚本目录（审计 §6）：nginx.conf 经 -v 挂进容器必须用绝对路径；从仓库根调
# 用时 dirname($0) 拼出来的是 deploy/deploy/nginx.conf（不存在），而从 deploy/ 内
# 调用时相对路径会让 Docker 报相对 bind 源。cd 后全文仍用 $SCRIPT_DIR 绝对引用。
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# --skip-version-check 是运维紧急开关（见 check_version_lock）：它可能出现在任意位置，
# 因此先把位置参数里的它摘掉，剩下的仍是 [action] [target]。
SKIP_VERSION_CHECK=0
POSITIONAL=()
for arg in "$@"; do
    case "$arg" in
        --skip-version-check) SKIP_VERSION_CHECK=1 ;;
        *) POSITIONAL+=("$arg") ;;
    esac
done
set -- "${POSITIONAL[@]}"

ACTION=${1:-"fast"}
TARGET=${2:-""}

# 版本 tag 的保留个数（每个镜像留几个可回退锚点）。tag 只是镜像的第二个名字，不额外占磁盘；
# 但名字会无限增长，所以在 tag_release_images 里撤掉更老的（同一份层数据仍在，容器用的
# :local 滚动标签从不参与清理）。要留更多历史就设 MF_IMAGE_TAG_KEEP。
IMAGE_TAG_KEEP=${MF_IMAGE_TAG_KEEP:-3}

# 自动定位 .env（项目根目录 ../.env 或当前 deploy/.env）
ENV_FILE=""
if [ -f "../.env" ]; then
  ENV_FILE="../.env"
elif [ -f ".env" ]; then
  ENV_FILE=".env"
fi

if [ -n "$ENV_FILE" ]; then
  COMPOSE_ENV="--env-file $ENV_FILE"
  echo "📦 使用环境文件: $ENV_FILE"
else
  COMPOSE_ENV=""
  echo "⚠️  未找到 .env，将依赖 docker-compose 内置默认值（仅适用于开发）"
  echo "   请执行: cp ../.env.example ../.env 并填入真实密钥"
fi

# 网关的 nginx.conf 是以文件挂载进容器的：compose 只看服务定义，不看被挂载文件的
# 内容，所以改完路由矩阵后 `up -d` 不会重建网关，必须显式 reload 才生效。
# 用 nginx -t 先校验再 reload，配置写错时保留旧配置继续服务，不会把入口打挂。
# 网关路由切换（审计 O03）：候选配置先验后切，失败非零、可回退。
# 顺序：① 候选文件经同版本 nginx 镜像离线 nginx -t（不碰运行中容器）；
# ② 通过后重建容器（单文件 bind mount 绑的是 inode，不重建则 reload 仍读旧内容）；
# ③ 容器内再做一次 nginx -t，成功才 reload。任何一步失败即非零退出——
# “保留旧配置继续服务”只在第 ① 步成立（旧容器原封不动）；第 ② 步之后若失败，
# 回退办法：git checkout -- deploy/nginx.conf 后重调本函数，或按上一版镜像 tag 重建。
# 调用约束（审计 §6）：候选校验必须跑在所有可能更新网关的 up 之前
# （各动作的整套 up -d 之前），reload_gateway 只负责切；候选缺失/非法一律非零，
# 不再“跳过并返回成功”（跳过等于带着错配置继续部署）。
function check_gateway_candidate() {
    local nginx_conf="$SCRIPT_DIR/nginx.conf"
    local candidate_image="nginx:1.30.6-alpine"
    if [ ! -f "$nginx_conf" ]; then
        echo "❌ 找不到网关候选配置 $nginx_conf：部署中止（候选缺失必须失败，不跳过）" >&2
        return 1
    fi
    # ① 先验候选：失败直接非零退出，运行中的网关与旧容器一律不动。
    if ! docker run --rm -v "$nginx_conf:/etc/nginx/nginx.conf:ro" "$candidate_image" nginx -t; then
        echo "❌ 网关候选配置校验失败：未切换、旧容器继续服务（审计 O03），部署中止" >&2
        return 1
    fi
    echo "✅ 网关候选配置离线校验通过"
}
function reload_gateway() {
    if ! docker exec metafusion-gateway true >/dev/null 2>&1; then
        return 0
    fi
    # 前置校验已在各动作的 up 之前由 check_gateway_candidate 做过；这里仍先验后切，
    # 候选在检查与切换之间被改坏时依然拦得住（多一次 docker run，换一次确定性）。
    if ! check_gateway_candidate; then
        return 1
    fi
    # ② 原子切换：重建以挂上新 inode，再验再载（失败即非零，不吞错）。
    docker compose $COMPOSE_ENV -f docker-compose.yml up -d --force-recreate --no-deps gateway
    if docker exec metafusion-gateway nginx -t >/dev/null 2>&1; then
        docker exec metafusion-gateway nginx -s reload >/dev/null 2>&1
        echo "🔄 网关候选已验证并切换：路由矩阵已重载"
    else
        echo "❌ 网关容器内校验失败：重建已发生，请 git checkout -- deploy/nginx.conf 后重调，或用上一版镜像回退，部署中止" >&2
        return 1
    fi
}

# 兄弟仓库是构建输入：编排按 ../metafusion-* 的**当前检出**构建，而 deploy/versions.lock 记的是
# 上一次验证过的那次提交。不比对就会把没验证过的代码推上线，回滚时也说不清当时部署的是哪一份。
# 比对由 scripts/check_versions.py 提供（逐条 git rev-parse HEAD，兄弟目录缺席会 SKIP）。
# 紧急情况用 ./deploy.sh <action> --skip-version-check 显式跳过：跳过原因会打进日志，
# 不是静默放行。
function check_version_lock() {
    if [ "$SKIP_VERSION_CHECK" = "1" ]; then
        echo "⚠️  已跳过版本锁校验（--skip-version-check）：本次部署不保证兄弟仓库是 versions.lock 里的提交"
        return 0
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo "❌ 本机没有 python3，无法校验版本锁；确认环境后可用 --skip-version-check 显式跳过" >&2
        exit 1
    fi
    echo "🔒 校验部署版本锁 (deploy/versions.lock)..."
    if ! python3 "$SCRIPT_DIR/../scripts/check_versions.py"; then
        echo "❌ 兄弟仓库与 deploy/versions.lock 不一致：先把锁刷到本次要部署的提交再继续" >&2
        echo "   紧急放行（会写进日志）：./deploy.sh $ACTION --skip-version-check" >&2
        exit 1
    fi
}

# /api/version 的构建期身份来源：把当前提交与版本交给 compose（backend 的 build args）。
# 只有构建期注入才靠得住——容器里没有 .git（.dockerignore 排除），运行期再读环境变量会随重启漂移。
# 刻意不标 dirty：部署树里有未提交文件是常态，标了几乎永远是 dirty，反而没人会看这一行。
function export_version_identity() {
    local root_dir sha tag
    root_dir="$SCRIPT_DIR/.."
    if [ -z "${METAFUSION_GIT_SHA:-}" ] && command -v git >/dev/null 2>&1; then
        sha=$(git -C "$root_dir" rev-parse --short=12 HEAD 2>/dev/null || true)
        [ -n "$sha" ] && export METAFUSION_GIT_SHA="$sha"
    fi
    if [ -z "${METAFUSION_VERSION:-}" ] && command -v git >/dev/null 2>&1; then
        tag=$(git -C "$root_dir" describe --tags --exact-match 2>/dev/null || true)
        [ -n "$tag" ] && export METAFUSION_VERSION="$tag"
    fi
    echo "🏷️  版本身份（写进 /api/version）：git=${METAFUSION_GIT_SHA:-unknown} version=${METAFUSION_VERSION:-unknown}"
}


# 每批部署给镜像补版本 tag。**没有版本 tag 就没有快速回退**：容器用的是滚动标签
# （metafusion-*:local 与编排生成的 deploy-<svc>:latest），下一次部署直接覆盖它们，
# 上一版镜像随即变成无主镜像被 prune 掉 —— v0.3.0 之前就是这样，回退只剩「按 tag 重建」（10-20 分钟）。
# 口径与 /api/version 的版本身份一致：METAFUSION_VERSION（git describe 的精确 tag），
# 没有 tag 时退回短 sha，保证每批都有锚点。回退命令见 docs/architecture/deployment-runbook.md。
function tag_release_images() {
    local ver="${METAFUSION_VERSION:-}"
    if [ -z "$ver" ] || [ "$ver" = "unknown" ]; then
        ver="${METAFUSION_GIT_SHA:-}"
    fi
    if [ -z "$ver" ]; then
        echo "⚠️  无法确定版本标识（既无 git tag 也无 sha）：跳过镜像版本 tag（本批没有可回退锚点）"
        return 0
    fi

    # 只给**本仓库/兄弟仓库构建的镜像**打 tag：仓库名要么是 metafusion-*（compose 里显式 image:
    # 的那几个服务），要么是 <compose 项目名>-*（frontend / docs / 三个管理台由 compose 生成
    # deploy-<svc>）。postgres / redis / nginx / rustfs / opensearch 是别人的发布物，给它们贴
    # 本项目的版本号等于把"这个 tag 代表一次发布"说成谎话，也会让保留策略去管别人的镜像；
    # 网关虽然用 nginx:1.30.6-alpine，但它的配置是 bind mount，本来就没有镜像可回退。
    local project="${COMPOSE_PROJECT_NAME:-$(basename "$PWD")}"
    local in_use_ids imgs img config_image image_id repo tagged=0
    # Config.Image 是创建容器时传入的可变标签（例如 deploy-frontend），后续 latest
    # 移动后它不再指向容器实际运行的镜像。用 immutable image ID 打锚，也按 ID 保护运行中镜像。
    in_use_ids="$(docker ps -aq | xargs -r docker inspect -f "{{.Image}}" | sort -u)"
    imgs="$(docker compose $COMPOSE_ENV -f docker-compose.yml ps -q 2>/dev/null | xargs -r docker inspect -f "{{.Config.Image}}|{{.Image}}" 2>/dev/null | sort -u)"
    if [ -z "$imgs" ]; then
        echo "⚠️  没读到本项目容器镜像：跳过镜像版本 tag"
        return 0
    fi
    for img in $imgs; do
        config_image="${img%%|*}"
        image_id="${img#*|}"
        repo="${config_image%:*}"
        [ -z "$repo" ] && continue
        case "$repo" in
            metafusion-*|"$project"-*) ;;
            *) continue ;;
        esac
        if ! docker image inspect "$image_id" >/dev/null 2>&1; then continue; fi
        if [ "$repo:$ver" != "$config_image" ]; then
            if docker tag "$image_id" "$repo:$ver" >/dev/null 2>&1; then
                echo "🏷️  镜像 tag：$config_image ($image_id) -> $repo:$ver"
                tagged=$((tagged + 1))
            else
                echo "❌ 无法为运行中的镜像 $config_image ($image_id) 创建版本 tag $repo:$ver" >&2
                return 1
            fi
        fi
        prune_release_tags "$repo" "$in_use_ids" "$repo:$ver"
    done
    echo "✅ 版本 tag 完成：$tagged 个镜像带 $ver（每个镜像保留最近 $IMAGE_TAG_KEEP 个版本）"
    echo "   回退：docker tag <repo>:<旧版本> <repo>:local && docker compose up -d --no-deps --force-recreate <svc>"
}

# 保留策略：每个镜像只留最近 IMAGE_TAG_KEEP 个版本 tag，更老的撤 tag（镜像层数据不动、
# 容器用的 :local / :latest 滚动标签永不参与）。候选只认「版本形态」的名字：vX.Y.Z 或 12 位短 sha。
function prune_release_tags() {
    local repo="$1" in_use_ids="$2" current="$3" i=0 t sorted image_id
    # 判定用 bash 通配，不用 awk/grep 正则：目标机的 awk 是 mawk 1.3.4，**不支持 {n} 区间**，
    # 写 /:(v[0-9]|[0-9a-f]{12})$/ 这种正则会静默不匹配（首版就是这么漏掉清理的，实测才发现）。
    # 历史 tag 按镜像构建时间倒序；当前 tag 必须单独保留，因为未变更服务的新版本 tag
    # 与旧 tag 指向同一镜像，CreatedAt 相同，排序无法判断哪个才是本次版本。
    sorted="$(docker images --format "{{.Repository}}:{{.Tag}} {{.CreatedAt}}" "$repo" 2>/dev/null \
        | while IFS= read -r line; do
              t="${line%% *}"; created="${line#* }"
              case "$t" in
                  *:v[0-9]*|*:[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) printf '%s %s\n' "$created" "$t" ;;
              esac
          done | sort -r | while IFS= read -r cline; do printf '%s\n' "${cline##* }"; done)"
    for t in $sorted; do
        [ "$t" = "$current" ] && continue
        i=$((i + 1))
        # 当前 tag 已占一个保留位，历史 tag 最多留 IMAGE_TAG_KEEP - 1 个。
        [ "$i" -lt "$IMAGE_TAG_KEEP" ] && continue
        if ! image_id="$(docker image inspect -f "{{.Id}}" "$t" 2>/dev/null)"; then continue; fi
        if printf '%s\n' "$in_use_ids" | grep -Fxq "$image_id"; then
            echo "   ↳ 保留 $t（镜像仍被容器使用）"
            continue
        fi
        if docker rmi "$t" >/dev/null 2>&1; then
            echo "   ↳ 撤掉更老的版本 tag $t（只留最近 $IMAGE_TAG_KEEP 个）"
        else
            echo "❌ 无法撤掉旧版本 tag $t" >&2
            return 1
        fi
    done
}

# 迁移必须用**当前镜像里**的迁移器：迁移是编译进二进制的（embed），用运行中的旧容器
# 执行会报"已是最新"而漏掉新迁移。迁移走 backend-migrate 一次性作业（入口已是 /app/migrate，
# 库 owner 身份，profiles: tools）：backend 常驻服务已不再持有 DB_*（审计 O01），
# 用它跑迁移会拿到空身份。--no-deps 不连带拉起依赖，只跑这一个一次性容器。
#
# 第二个及以后的参数是**额外的 compose 文件**（如 -f docker-compose.prod.yml）：pull 路径上
# 要跑的是拉下来那份镜像里的迁移器，而不是本地那张旧标签。
function run_migrate() {
    local cmd=${1:-up}
    shift || true
    docker compose $COMPOSE_ENV -f docker-compose.yml "$@" run --rm --no-deps backend-migrate "$cmd"
}

# 账号迁移在新镜像启动 HTTP 前完成；失败保持旧写入口停止。
# auth 镜像使用 CMD 而非 ENTRYPOINT，完整 CLI 已固定于 compose auth-migrate。
function migrate_auth_checked() {
    echo "🗄️  停止账号写入口并显式应用账号 schema..."
    docker compose $COMPOSE_ENV -f docker-compose.yml "$@" stop auth || return 1
    if ! docker compose $COMPOSE_ENV -f docker-compose.yml "$@" run --rm --no-deps auth-migrate; then
        echo "❌ 账号迁移失败：保持 auth 停止；用户名冲突按迁移错误说明修复后重跑" >&2
        return 1
    fi
}

# 只从 $ENV_FILE 取键值，不 source 整份文件（不执行 .env 内容）。
function _env_val() {
    [ -n "${ENV_FILE:-}" ] && [ -f "$ENV_FILE" ] || return 0
    sed -n "s/^[[:space:]]*$1=//p" "$ENV_FILE" | head -n1 | tr -d '"' | sed -e "s/^'//" -e "s/'\$//"
}

# 启动前 DSN 门禁：所有模式均要求四个本域 DSN。
# 缺失即非零退出，不静默回退共用身份。只判空、不打印值（验收口径：只查有无）。
function require_business_dsns() {
    local missing=0 v val
    for v in CATALOG_DATABASE_URL AUTH_DATABASE_URL AUTH_MIGRATION_DATABASE_URL COMMUNITY_DATABASE_URL STORAGE_DATABASE_URL MCP_DATABASE_URL MCP_MIGRATION_DATABASE_URL MCP_ENCRYPTION_KEY; do
        val="${!v:-}"
        [ -n "$val" ] || val="$(_env_val "$v")"
        if [ -z "$val" ]; then
            echo "❌ 缺少 $v：业务 DSN 与 MCP 配置必须逐个填入 .env（见 .env.example）" >&2
            missing=1
        fi
    done
    if [ "$missing" = 1 ]; then
        echo "   请配置各服务独立的数据库身份后重试。" >&2
        exit 1
    fi
    local sha short repo
    for repo in metafusion-skills metafusion-docs; do
        short=$(awk -F= -v repo="../$repo" '$1==repo {print $2}' "$SCRIPT_DIR/versions.lock")
        [ -n "$short" ] || { echo "❌ 版本锁缺少 $repo" >&2; exit 1; }
        sha=$(git -C "$SCRIPT_DIR/../../$repo" rev-parse "$short^{commit}") || exit 1
        case "$repo" in
            metafusion-skills) export MCP_SKILLS_REVISION="$sha" ;;
            metafusion-docs) export MCP_DOCS_REVISION="$sha" ;;
        esac
    done
    echo "✅ 业务 DSN 与 MCP 配置齐全；技能和文档已锁定（值不打印）"
}

# 发布清单门禁（审计 P1/A08c）：pull 面向线上，三件事缺一不可——
# ① IMAGE_TAG 只用于确认清单归属（latest/空直接拒绝）；实际拉取和运行以 digest 为准；
# ② release-manifest.yaml 必须存在且完整
#    （scripts/check_release_manifest.py --strict：无待填、同源绑定一致、
#    且 IMAGE_TAG 落在各服务 tags 内）。清单缺席不再警告放行，直接中止；
# ③ 清单的 versions_lock 必须与 deploy/versions.lock 逐条一致（部分旧版本即中止）。
# 校验通过后生成一次性 Compose 覆盖，让拉取、迁移和常驻服务使用同一组 digest。
# 同时要求清单 SHA 与当前主仓检出一致，避免旧清单配新迁移脚本。
function require_pinned_manifest() {
    local override="$1" expected_sha
    if [ -z "${IMAGE_TAG:-}" ] || [ "${IMAGE_TAG:-latest}" = "latest" ]; then
        echo "❌ IMAGE_TAG 未指定有效发布标签（当前 ${IMAGE_TAG:-空}）：请使用 release-manifest.yaml 内三个镜像共有的标签" >&2
        exit 1
    fi
    local manifest=""
    if [ -f "../release-manifest.yaml" ]; then manifest="../release-manifest.yaml"
    elif [ -f "release-manifest.yaml" ]; then manifest="release-manifest.yaml"
    fi
    if [ -z "$manifest" ]; then
        echo "❌ 找不到 release-manifest.yaml：必须按完整清单拉取，中止（从发布附件取回后放到仓库根再继续）" >&2
        exit 1
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo "❌ 有清单但本机没有 python3，无法校验完整性；确认环境后重跑" >&2
        exit 1
    fi
    if ! expected_sha=$(git -C "$SCRIPT_DIR/.." rev-parse HEAD 2>/dev/null); then
        echo "❌ 无法读取主仓提交：不能确认清单与当前检出同源，部署中止" >&2
        exit 1
    fi
    echo "🔒 校验发布清单 $manifest（只查结论，不打印凭据）..."
    if ! python3 "$SCRIPT_DIR/../scripts/check_release_manifest.py" --strict --expect-tag "$IMAGE_TAG" --expect-lock "$SCRIPT_DIR/versions.lock" --expect-sha "$expected_sha" --write-compose-override "$override" "$manifest"; then
        echo "❌ 发布清单、镜像摘要或版本组合与当前检出不一致：部署中止" >&2
        exit 1
    fi
}

# 编排凭据隔离断言（审计 O01，验收口径：只查键名与布尔结果，不打印值）。
# 用法：assert_compose_credential_isolation [-f 额外 compose 文件...]
# 判据：四个常驻业务服务的合并后 environment 里必须有 DATABASE_URL 键，
# 不得出现 DB_*（管理身份）；除 storage 外不得出现 STORAGE_S3_*/RUSTFS_*。
function assert_compose_credential_isolation() {
    local cfg
    if ! cfg=$(docker compose $COMPOSE_ENV -f docker-compose.yml "$@" config --format json 2>/dev/null); then
        echo "❌ 读不到合并后编排（docker compose config 失败）：无法确认凭据隔离，部署中止" >&2
        return 1
    fi
    printf '%s' "$cfg" | python3 -c '
import json, sys
doc = json.load(sys.stdin)
svcs = doc.get("services", {})
banned_db = {"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME",
             "AUTH_MIGRATION_DATABASE_URL", "MCP_MIGRATION_DATABASE_URL"}
banned_s3 = {"STORAGE_S3_ACCESS_KEY", "STORAGE_S3_SECRET_KEY",
              "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY"}
bad = []
for name in ("backend", "auth", "community", "storage", "mcp"):
    env = (svcs.get(name, {}) or {}).get("environment", {}) or {}
    keys = set(env.keys())
    leak = sorted(keys & banned_db)
    if leak:
        bad.append("%s 持有管理凭据键：%s" % (name, ",".join(leak)))
    dsn_key = "MCP_DATABASE_URL" if name == "mcp" else "DATABASE_URL"
    if dsn_key not in keys:
        bad.append("%s 缺少本域 DSN 键" % name)
    if name == "mcp" and "MCP_MIGRATION_DATABASE_URL" in keys:
        bad.append("mcp 常驻服务持有迁移 DSN")
    if name != "storage":
        sleak = sorted(keys & banned_s3)
        if sleak:
            bad.append("%s 持有对象凭据键：%s" % (name, ",".join(sleak)))
if bad:
    print("业务容器凭据隔离未通过（只列键名，不打印值）：")
    for b in bad:
        print("  - " + b)
    sys.exit(1)
print("业务容器凭据隔离通过：5 个服务仅本域 DSN（键名已核对，值未打印）")
' || {
        echo "❌ 凭据隔离断言失败：业务容器不得持有管理/对象凭据（审计 O01），部署中止" >&2
        return 1
    }
}

# 部署流程里唯一的迁移入口：跑完 up 必须回读账本确认，读不出来、还有 PENDING 或出现 DIRTY
# 就非零退出。"迁移没跑却报部署成功"是必须避免的失败模式：compose run 只替换 CMD、不换
# ENTRYPOINT，少了 --entrypoint 参数会被交给 /app/server，一条迁移都不会执行
# （backend/Dockerfile 的 server 阶段是 ENTRYPOINT ["/app/server"]）。
function stop_catalog_writes() {
    echo "🛑 迁移前停止 backend 与 MCP，避免旧写入端在协议切换后继续写入..."
    if ! docker compose $COMPOSE_ENV -f docker-compose.yml "$@" stop backend mcp; then
        echo "❌ 无法确认 backend 与 MCP 均已停写：迁移中止，请先停止所有目录写入端" >&2
        return 1
    fi
    echo "   backend 与 MCP 已停止；失败不会自动重启，请保持两者停写直至迁移与检查通过并就绪匹配的应用版本"
}

function migrate_up_checked() {
    # The consolidated baseline rejects incomplete pre-020 ledgers. Old
    # contract upgrades belong to the pre-baseline release and its runbook.
    stop_catalog_writes "$@" || return 1
    echo "🗄️  执行目录库版本化迁移..."
    if ! run_migrate up "$@"; then
        echo "❌ 迁移失败：保持 backend 与 MCP 停写，核对账本与备份并处理后再部署" >&2
        return 1
    fi

    local status
    if ! status=$(run_migrate status "$@"); then
        echo "❌ 迁移后读不到迁移账本：无法确认结构已落地，部署中止" >&2
        exit 1
    fi
    echo "$status"
    if printf '%s\n' "$status" | grep -q 'PENDING'; then
        echo "❌ 仍有未应用的迁移（上面标为 PENDING）：部署中止" >&2
        exit 1
    fi
    if printf '%s\n' "$status" | grep -Eq 'DIRTY|MODIFIED|UNVERIFIED|UNKNOWN'; then
        echo "❌ 存在脏迁移（上面标为 DIRTY）：先人工确认再部署" >&2
        exit 1
    fi
    if ! printf '%s\n' "$status" | grep -q 'APPLIED'; then
        echo "❌ 迁移账本里没有任何已应用版本：迁移未生效，部署中止" >&2
        exit 1
    fi
    echo "✅ 迁移已确认：账本无待办或未验证记录"
}

# 迁移落地后显式合并种子定义（EnsureSeedDefinitions，保留人工配置）。
# HTTP 启动只读检查，不执行种子；这里失败即非零退出，不启动新服务。
function seed_checked() {
    echo "🌱 显式合并种子定义（只增不改）..."
    run_migrate seed "$@"
}

# 悬挂引用体检是部署硬门禁：非零即阻断启动，backend 保持停写，处理后必须重验通过。
function check_refs_report() {
    echo "🔍 悬挂引用体检（调用 mf-migrate check-refs，部署门禁）..."
    if ! run_migrate check-refs "$@"; then
        echo "❌ 悬挂引用体检未通过：保持 backend 与 MCP 停写，处理上面的问题并重验通过后再部署" >&2
        return 1
    fi
}

function print_usage() {
    echo "================================================================="
    echo "  MetaFusion 极速部署与运维脚本"
    echo "================================================================="
    echo "用法: ./deploy.sh [action] [service_name]"
    echo ""
    echo "操作模式 (Actions):"
    echo "  fast [service]  - 增量极速更新指定服务 (默认)，自动复用构建缓存 (几秒内完成)"
    echo "  dev             - 启动热重载开发模式 (源码挂载，修改代码免构建秒级生效)"
    echo "  prod            - 完整生产模式冷启动"
    echo "  pull            - 按清单 digest 拉取 backend/frontend/migrator；兄弟服务就地构建后启动"
    echo "  migrate [cmd]   - 执行版本化数据库迁移 (up/down/status/force)"
    echo "  seed            - 显式合并种子定义（只增不改；prod/pull 在迁移后自动跑）"
    echo "  check-refs      - 悬挂引用体检（部署前置检查；非零=先修数据或显式确认）"
    echo "  restart [svc]   - 快速重启容器 (不重编镜像)"
    echo "  prune           - 清理所有旧镜像与未使用的构建缓存 (释放磁盘)"
    echo "  logs [svc]      - 实时查看容器运行日志"
    echo "  status          - 查看全部容器健康状态"
    echo ""
    echo "全局开关:"
    echo "  --skip-version-check - 跳过 versions.lock 校验（仅紧急放行，日志会写明）"
    echo "================================================================="
}

case "$ACTION" in
    dev)
        require_business_dsns
        echo "🚀 启动本地热重载开发模式 (Zero-Rebuild Dev Mode)..."
        export DOCKER_BUILDKIT=1
        docker compose $COMPOSE_ENV -f docker-compose.yml build auth
        docker compose $COMPOSE_ENV -f docker-compose.yml up -d --wait postgres
        migrate_auth_checked
        docker compose $COMPOSE_ENV -f docker-compose.yml -f docker-compose.dev.yml up -d
        echo "✅ 开发环境已就绪！源码已挂载，代码修改即时热重载生效。"
        ;;

    fast)
        check_version_lock
        export_version_identity
        require_business_dsns
        export DOCKER_BUILDKIT=1
        # 候选先验（审计 §6）：整套 up 在前、检查在后会让坏配置先挂载再生效，
        # 因此 up 之前必须验过；只在可能更新网关时验（全量或目标即网关），
        # 单服务增量不为无关更新引入网关依赖。
        if [ -z "$TARGET" ] || [ "$TARGET" = "gateway" ]; then
            check_gateway_candidate
        fi
        if [ -n "$TARGET" ]; then
            echo "⚡ 增量更新指定服务 [$TARGET]..."
            docker compose $COMPOSE_ENV build "$TARGET"
            if [ "$TARGET" = "backend" ]; then
                docker compose $COMPOSE_ENV -f docker-compose.yml build backend-migrate
                migrate_up_checked
                seed_checked
                check_refs_report
            fi
            if [ "$TARGET" = "auth" ]; then migrate_auth_checked; fi
            if [ "$TARGET" = "mcp" ]; then docker compose $COMPOSE_ENV run --rm --no-deps mcp-migrate; fi
            docker compose $COMPOSE_ENV up -d --no-deps "$TARGET"
        else
            echo "⚡ 增量构建并更新全部服务 (复用 BuildKit 缓存)..."
            # 不带服务名即构建**所有**带 build 段的服务：拆分后子系统在兄弟仓库，
            # 只写 backend frontend 会让互动/存储/账号停留在旧镜像。
            docker compose $COMPOSE_ENV build
            docker compose $COMPOSE_ENV up -d --wait postgres
            docker compose $COMPOSE_ENV -f docker-compose.yml build backend-migrate
            migrate_up_checked
            seed_checked
            check_refs_report
            migrate_auth_checked
            docker compose $COMPOSE_ENV run --rm --no-deps mcp-migrate
            docker compose $COMPOSE_ENV up -d --remove-orphans
        fi
        reload_gateway
        # 先补版本 tag 再清悬空层：本批镜像有了名字就不会被当成无主镜像清掉，
        # 也让"回退到上一版"变成换 tag + recreate（见 tag_release_images 的注释）。
        tag_release_images
        echo "🧹 自动清理悬空层..."
        docker image prune -f >/dev/null 2>&1 || true
        echo "✅ 极速部署完成！"
        ;;

    prod)
        check_version_lock
        export_version_identity
        require_business_dsns
        assert_compose_credential_isolation
        echo "🏭 启动生产集群模式..."
        export DOCKER_BUILDKIT=1
        # 同一份 backend 上下文与 Dockerfile（只换 target），迁移前必须同批构建；
        # 只构建 backend 会复用旧迁移器镜像，迁移跑在旧二进制上；
        # 同源离线断言见 check_deploy.py，有自测覆盖。
        docker compose $COMPOSE_ENV -f docker-compose.yml build backend backend-migrate
        echo "🚀 启动数据库与核心基础设施 (Postgres / RustFS)..."
        docker compose $COMPOSE_ENV up -d --wait postgres
        docker compose $COMPOSE_ENV up -d rustfs
        migrate_up_checked
        seed_checked
        check_refs_report
        check_gateway_candidate
        docker compose $COMPOSE_ENV build auth
        migrate_auth_checked
        docker compose $COMPOSE_ENV build mcp
        docker compose $COMPOSE_ENV run --rm --no-deps mcp-migrate
        docker compose $COMPOSE_ENV up -d --build --remove-orphans
        reload_gateway
        tag_release_images
        docker image prune -f >/dev/null 2>&1 || true
        echo "✅ 生产环境已启动！"
        ;;

    pull)
        check_version_lock
        export_version_identity
        require_business_dsns
        assert_compose_credential_isolation -f docker-compose.prod.yml
        echo "📦 拉取预构建生产容器镜像 (GHCR)..."
        PULL_OVERRIDE=$(mktemp "${TMPDIR:-/tmp}/metafusion-pull.XXXXXX.yml")
        trap 'rm -f -- "$PULL_OVERRIDE"' EXIT
        require_pinned_manifest "$PULL_OVERRIDE"
        # 三个预构建镜像均按清单摘要拉取；迁移器也必须在升级数据库前就位。
        docker compose $COMPOSE_ENV -f docker-compose.yml -f docker-compose.prod.yml -f "$PULL_OVERRIDE" pull backend frontend backend-migrate
        # 兄弟服务没有 GHCR 发布物。逐个构建锁定检出，避免复用旧镜像及并行构建耗尽部署机内存。
        for service in auth community storage mcp docs-site auth-admin auth-user community-admin storage-admin; do
            docker compose $COMPOSE_ENV -f docker-compose.yml -f docker-compose.prod.yml -f "$PULL_OVERRIDE" build "$service"
        done
        echo "🚀 启动数据库与核心基础设施..."
        docker compose $COMPOSE_ENV up -d --wait postgres
        docker compose $COMPOSE_ENV up -d rustfs
        migrate_up_checked -f docker-compose.prod.yml -f "$PULL_OVERRIDE"
        seed_checked -f docker-compose.prod.yml -f "$PULL_OVERRIDE"
        check_refs_report -f docker-compose.prod.yml -f "$PULL_OVERRIDE"
        check_gateway_candidate
        migrate_auth_checked -f docker-compose.prod.yml -f "$PULL_OVERRIDE"
        docker compose $COMPOSE_ENV -f docker-compose.yml -f docker-compose.prod.yml -f "$PULL_OVERRIDE" run --rm --no-deps mcp-migrate
        docker compose $COMPOSE_ENV -f docker-compose.yml -f docker-compose.prod.yml -f "$PULL_OVERRIDE" up -d --no-build --pull never --remove-orphans
        reload_gateway
        echo "✅ 生产镜像拉取与启动完成！"
        ;;

    migrate)
        CMD=${TARGET:-"up"}
        if [ "$CMD" = "up" ]; then
            # up 走带停写与校验的入口，完成后不自动启动 backend。
            migrate_up_checked
        else
            if [ "$CMD" = "down" ]; then
                stop_catalog_writes
            fi
            echo "🗄️ 执行数据库版本化迁移 (mf-migrate $CMD)..."
            run_migrate "$CMD"
        fi
        ;;

    seed)
        # 显式入口：与 prod/pull 里自动跑的是同一个 seed_checked，
        # 部署后想单独确认模板状态时用它（失败即非零；HTTP 启动不合并种子）。
        seed_checked
        echo "✅ 种子定义已显式合并"
        ;;

    check-refs)
        # 部署前置检查的硬门禁形态：库干净返回 0，有悬挂引用返回非零。
        # 处理数据后必须重验通过，再部署。
        echo "🔍 悬挂引用体检（mf-migrate check-refs）..."
        run_migrate check-refs
        ;;

    restart)
        if [ -n "$TARGET" ]; then
            echo "🔄 重启服务 [$TARGET]..."
            docker compose $COMPOSE_ENV restart "$TARGET"
        else
            echo "🔄 重启全部服务..."
            docker compose $COMPOSE_ENV restart
        fi
        echo "✅ 重启完毕！"
        ;;

    prune)
        echo "🧹 正在深度清理 Docker 磁盘占用..."
        docker image prune -f
        docker builder prune -f --keep-storage 1GB
        echo "📊 当前 Docker 存储概览:"
        docker system df
        ;;

    logs)
        if [ -n "$TARGET" ]; then
            docker compose $COMPOSE_ENV logs -f "$TARGET"
        else
            docker compose $COMPOSE_ENV logs -f
        fi
        ;;

    status)
        docker compose $COMPOSE_ENV ps
        ;;

    help|--help|-h)
        print_usage
        ;;

    *)
        echo "❌ 未知命令: $ACTION"
        print_usage
        exit 1
        ;;
esac
