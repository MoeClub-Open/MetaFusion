# 部署与恢复手册

本手册维护现行安装、升级、验收和回退流程。服务归属及 API 矩阵见[拆分契约](./service-split-migration.md)，数据库身份见[数据库角色](./database-roles.md)，对象存储与备份边界见[存储运行约定](./storage-operations.md)。实例 SHA、镜像摘要、备份路径和验收结果记在不提交的 `docs-local/deploy/runbook.md`。

## 发布输入

主仓与 auth、community、storage、gateway、docs 仓库平铺在同一父目录，编排按 `deploy/versions.lock` 固定兄弟仓版本。预构建部署使用同 SHA 的 CI、CodeQL 和 Release 成功产物，将 `release-manifest` 构件放到主仓根 `release-manifest.yaml`。

```bash
python3 scripts/check_versions.py
python3 scripts/check_release_manifest.py --strict --expect-tag sha-<short-sha> \
  --expect-sha <full-sha> --expect-lock deploy/versions.lock release-manifest.yaml
```

清单 v2 必须包含 backend、frontend、migrator 的真实 digest。`deploy.sh pull` 按 digest 拉取三个镜像，从锁定检出构建兄弟服务及其 UI；迁移器必须与后端同源。部署要求各服务运行 DSN，以及仅交给一次性 `auth-migrate` 的 `AUTH_MIGRATION_DATABASE_URL`。该结构身份不得注入账号 HTTP 服务；私钥仅归 auth，配置检查不得打印凭据。

首次安装先从根目录 `.env.example` 建立本地配置，准备数据库和各域运行身份，按[授权步骤](./database-roles.md#6-授权验收与回退)执行角色脚本并验证隔离。共享审计预建须显式传 `-v audit_bootstrap=1`；普通重跑不会自动收敛其 owner。随后用 `prod/pull` 应用目录迁移与种子、启动服务，验收通过后由 `/setup` 创建首个管理员。不要将未初始化数据库交给 HTTP 进程自动建库。

## 备份与升级

1. 确认 tracked 文件无他人待提交修改，目标实例与清单一致。升级前用 `scripts/backup.sh --no-prune` 生成数据库、角色、配置和对象备份，保留最后可恢复副本。
2. 校验 `checksums.sha256`，用 `scripts/restore_drill.sh --run <backup-run> --scratch <isolated-db> --tables <affected-tables> --yes` 在独立库恢复并核对；对象演练按存储运行约定执行。
3. 涉及 JSON、来源或归属契约切换时，停止所有旧目录写入进程，记录受影响表行数和既有行指纹。备份后仍有写入时，备份不是切换时的完整快照，须在停写窗口补齐。
4. 执行匹配发布的升级。`prod/pull` 准备好当前迁移器后，先停止 `backend`，再执行显式 `up` → 回读账本 → `seed` → `check-refs` → 启动服务 → 校验并切换网关。外部导入或其他目录写入进程也须停写；脚本只负责本编排的 `backend`。

```bash
IMAGE_TAG=sha-<short-sha> deploy/deploy.sh pull
```

源码构建使用 `prod`。`dev` 启动开发覆盖编排并先应用账号迁移，目录库仍须事先准备。`fast` 无目标参数表示全量更新，全量或 `backend` 更新依次执行目录 `up`、`seed`、`check-refs`；其它目标不修改目录库，账号更新先应用账号迁移。`dev` 不执行目录迁移、种子或引用检查；首次安装使用 `prod/pull` 完整流程。旧单体的一次性 `cutover/retire` 入口已移除；未完成拆分的实例先使用对应历史发布工具完成搬运与权限验收。

目录与账号 HTTP 启动只做只读兼容检查。账号先构建匹配发布的 `auth` 镜像，再执行 `docker compose -f deploy/docker-compose.yml run --rm --no-deps auth-migrate`；CLI 是 `/app/auth-server -migrate`，无需签发私钥。用户名大小写冲突会阻断迁移并列出冲突标识；先人工确认并修复，不自动重命名或删号。迁移失败时 `deploy.sh` 保持账号入口停止。手工运维时 `mf-migrate up`、`seed`、`check-refs` 是不同任务；不能通过重启或健康响应推断它们已完成。种子对存量定义、货架和外部库只补缺失项，保留人工配置和停用状态。

`mf-migrate` 的操作超时默认 15 分钟；大库应先在备份恢复库测量 022 建索引和 023 引用规范化的耗时，必要时调整。全局 `-timeout` 必须放在命令前，例如 `mf-migrate -timeout 30m up`、`mf-migrate -timeout 30m check-refs -json`，不接受零或负时长。直接使用 Compose 时同样放在命令前：`docker compose -f deploy/docker-compose.yml run --rm --no-deps backend-migrate -timeout 30m up`，并先停写。

`deploy.sh migrate up/down` 也会先停止 `backend`，完成后不自动启动。迁移、账本、种子或引用检查失败时，保持所有目录写入端停写，核对失败原因、账本与备份并处理；不得仅重启旧后端继续写入新协议数据。

`check-refs` 检查属性实体引用、归属、subjects、contents 和关系端点。非零可能是悬挂引用或检查本身失败，均阻断 `prod/pull` 启动；处理后必须重验通过。体检不替代数据库约束、账本或用户事实核验。

## 验收与回退

- `/api/version` 的完整 SHA、容器 `Config.Image` 与清单一致，版本锁无未解释的缺席项。
- 账本无 PENDING、DIRTY、MODIFIED 或未验证摘要；definitions ETag、规则及人工配置符合预期，受影响旧行和修订指纹保持。
- 各服务 `/ready` 就绪，网关仓 `scripts/cutover-check.sh` 验证标记头与归属。宿主未发布业务端口时，服务 URL 取实际容器网络地址，不能使用默认 127.0.0.1。
- 目录 `/health.search_ready=true`，实际关键词加结构/动态字段过滤及 next_cursor 翻页成功。`/ready` 只证明 PostgreSQL 可用，不能代替搜索验收。
- 页面和对应写入路径按改动范围验证。浏览器夹具不能证明真实保存成功，回读须匹配实际对象和版本。

网关自检的旧互动表行数检查仅用于数据搬运。普通更新没有搬运时可跳过，并明确记录 SKIP，不记为通过。

网关配置以文件挂载；Git 更新可更换 inode，普通 `up -d` 和旧容器 reload 不保证读到新文件。`deploy.sh` 会先校验候选，再强制重建 gateway 并重载，手工更新也须完成候选校验、重建和 `nginx -t`。

边缘认证保护只覆盖 `/api/auth/login`、`register`、`refresh`、`/api/setup` 与 `/api/oauth/token` 五个精确路径，共用每真实客户端 IP 5 请求/秒、突发20的 `auth_limit`，超限429。账号登录失败次数保存在账号数据库并按账号/IP判定，多副本共享。目录等业务额度由服务验证身份后计数；`/mcp` 共享出口不进入认证 IP 桶，MCP 内部登录/令牌仍到各自认证接口。

真实 IP 链为 Cloudflare → 宿主 OpenResty → 回环发布的 gateway。必须验证实际 Docker bridge 网关与 `set_real_ip_from` 的单个宿主地址一致；不同网段时只替换该单点，不扩大到私网。OpenResty 只允许 Cloudflare 来源并覆盖客户端自带的 CF 头，公网入口伪造 `CF-Connecting-IP`/`X-Forwarded-For` 不能换认证桶。gateway 用还原后的 `$remote_addr` 覆盖上游 XFF。回环发布不能证明宿主调用者是 OpenResty，宿主进程属于特权信任边界。`/internal` 与 `/internal/` 在公网网关直接404。

`scripts/check_gateway_matrix.py` 与 `--selftest` 检查认证路径、业务桶禁止、XFF覆写和可信来源范围。CI 设置 `MF_REQUIRE_INTEGRATION_TESTS=1`，缺 PostgreSQL/OpenSearch 测试地址立即失败；主仓独立前端任务明确使用 `generate-contracts.mjs --check --allow-missing-sources` 做部分检查，跨仓任务必须使用纯 `--check`，缺少权限来源失败。

回退先停受影响写入，再使用上一批已验证清单及其兄弟仓组合。当前结构和 JSON 仍兼容上一版时可仅回退镜像；旧写入端会丢字段时须恢复已验证备份，并处理恢复点之后的写入。`down` 拒绝不可逆迁移，`force` 只清 dirty 标记，不等于执行或撤销 SQL。

## 迁移边界与数据库整理

安装源已合并为 `000021_catalog_baseline`；历史 001–020 SQL 保存在 `backend/internal/migrator/testdata/legacy_catalog/`，只作回归夹具，不嵌入发布二进制。`baseline.json` 固定历史版本、名称和原始 SHA256，当前基线发布后同样不可改。

- 新空库：执行完整终态 DDL，记账 021；`seed` 单独发布内容种子。
- 已完成 020 的存量库：验证完整历史集合、名称、摘要和 dirty 状态，事务内只新增 021，不重放建表、不删除旧账本、不重写 applied_at 或 checksum。
- 部分旧库、未知版本、缺失或漂移摘要：`up/status` 非零拒绝；先用合并前发布 `f8d66fe` 完成历史升级，并在备份恢复库核实摘要，不能把空摘要自动认领为当前文件。

会话锁 88481001、账本查询和迁移事务使用同一个数据库连接；释放失败的连接丢弃，避免带锁返回连接池。`status` 仍显示保留的旧记录，并显式标注异常。021 的 down 不可逆，回退不能恢复旧 migrator 去重放账本。

历史协议切换（014 定义收敛、016 types/图片时间跨度、019 收录来源）仅由合并前发布升级，旧写入端须在窗口内停止。020 移除 entities_search、entities_document 两个退役索引，业务行和历史修订保持。结构等价、完整旧账本保留、重复 up、异常拒绝及单连接/并发锁验证见 `baseline_test.go`；018 升级和编辑回归继续读取历史夹具。

日常整理先核活动连接、长事务、无效/重复索引、约束与悬挂引用，再执行普通 `VACUUM (ANALYZE)` 更新统计和回收死元组。按 schema 限定对象，通过新迁移删除经核实的旧结构；零扫描次数不足以判定索引无用。不默认删除修订、审计、outbox、账号或资源绑定，也不把 `VACUUM FULL`、删除卷或全局镜像清理当作例行维护。

## 当前引用与搜索协议切换

021 安装基线保持不可变；022 增加结构筛选和 JSONB 反查索引；023 按已发布 definitions 一次性规范化 entity/group/list 声明中的 UUID 引用，涵盖实体、关系、发行 subjects、轨位 contents 的 locator 与属性。普通文本及未知键不改，历史修订保留；改变的当前拥有者只升一次版本，并发出维护 outbox 事件。非法值或规范化后关系冲突中止整条迁移，须在备份恢复库确认问题，不静默删边或重定向引用。运行时只接受规范小写、带连字符的 UUID 引用。024 新增同步身份候选索引与归一函数；025 增加提交回执与修订归属。026 将父子环检查改为 SERIALIZABLE，移除全局结构锁，发布 `catalog.schema_contract=26`；后面三条迁移不改实体事实。新版启动还检查候选函数、有效索引和提交结构，不能跳过迁移直接换镜像。

030 以一次不可逆迁移把 `document.external_ids` 转为 `external_links` 数组与旁表 `catalog.external_links`（role 一律 NULL、origin=migration），并删除旧键与 `entities_metafusion_import_key`；身份候选术语改为读 links。不做双写。存量升级执行 030 增量；空库仍走 021+增量至 030。整库安装基线重拍留到本迁移在生产验证后再做。


026 切换必须先停止所有旧目录写入端，再迁移并启动匹配的服务端。旧镜像不能单独回退到契约26；恢复必须使用迁移前数据库备份及对应镜像/配置。原始 SQL 改父节点也须 SERIALIZABLE；连接异常不能作为可自动重放 COMMIT 的依据。

切换时停旧写入端，依次执行 up、seed、check-refs，随后启动当前版本与 OpenSearch。新版建立 v4 物理索引，重建现有条目的正文与外链检索内容，完成后原子切换搜索别名；旧索引不自动删除，确认当前索引及备份后由运维按名称清理。首次重建或故障期间 q 返回503，无关键词浏览仍可用。

四个运行 DSN 均显式必填；DB_* 只供数据库容器、迁移或运维，不能代替运行配置。RustFS 凭据统一使用 `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY`，同步更新备份配置；不再注入旧 ROOT 别名。内置 OpenSearch 单节点配置供起步使用，生产节点数、副本、认证和资源预算须按实测确定，见[核心实现与容量边界](./catalog-core-implementation.md#查询规模费用与接口边界)。

## 目录提交迁移与连接预算

035 collaboration 将目录协作切换为服务器工作副本、不可变提交与 PR；扩展原 commits，不另设第二套提交表。历史已应用提交按已有修订关联迁为已合入历史，未决提交转为独立副本和 open PR，被拒提交转为 closed PR；原始请求、结果、作者及已知修订保留，不伪造被早期清理过的请求或父链。

切换前停旧目录与 MCP 写入端，使用同源 migrator 执行 up，再同时启用新后端、前端、助手、MCP 和技能；旧提交审核及单对象事实写接口已删除，不能混用旧客户端。迁移不可逆，回退须恢复匹配的整库备份及应用组。先在独立恢复库验证 pending、rejected、历史作者与关系删除快照的迁移结果，再执行目标实例升级。

工作副本、提交、原始请求、修订和贡献关联永久保留，恢复不能只回滚实体表。`CATALOG_OUTBOX_RETENTION_DAYS=7` 可设0永久保留，允许0–36500天；`CATALOG_RETENTION_INTERVAL_SECONDS=3600`，允许60–86400秒。清理每轮共享30秒预算，最多20批，每事务最多500个已确认事件；未确认消费者事件保留。删除/合并贡献独立记录，不依赖队列长期存在。来源资格策略与提交载荷过期清理配置均已移除。

自托管封面的副本保留、私有预览与合入绑定要求目录配置 `STORAGE_URL`，目录与存储两侧显式填写同值的 `STORAGE_CATALOG_LIFECYCLE_SECRET`；没有自动生成或共享密钥的机制。默认空值仅适用于不使用自托管封面的安装，涉及资产的副本操作会明确返回 `503 storage_unavailable`。主编排提供内部存储 URL，metadata-only 可接外部存储服务；上线前同时执行存储 011 迁移。内部接口和资产回收验收见[存储运行约定](./storage-operations.md)。

CATALOG_DB_MAX_OPEN_CONNS 默认64（1–256），CATALOG_DB_MAX_IDLE_CONNS 默认32（0–maxOpen），其他服务与 MCP 默认32/16。PostgreSQL 编排默认 max_connections=512，两份编排均给 `/dev/shm` 256 MiB，CI PostgreSQL 同值。缩小 open 时同步设置 idle；副本连接总和须给迁移和运维留预算。提高上限不是容量优化证据，压测仅在独立容量环境运行 bench_catalog_reads.mjs。

## 云端 MCP 接线

将 `metafusion-mcp` 与 `metafusion-skills` 按 versions.lock 检出于兄弟目录。配置 MCP_PUBLIC_URL、MCP_DATABASE_URL（mf_mcp）、MCP_MIGRATION_DATABASE_URL（库 owner，仅迁移作业）、MCP_ENCRYPTION_KEY（base64 编码的32字节随机值）和 MCP_OBJECT_ORIGINS（实际对象存储 HTTPS origin）。技能与文档完整 SHA 由部署入口从已校验版本锁导出。保持原加密密钥；丢失或更换会使既有授权不可解密，需要重新授权。

首次部署先备份，库 owner 执行 deploy/sql/mcp-role.sql 建角色及默认权限，再运行 mcp-migrate，最后执行 verify-mcp-role.sql。运行服务只持 mcp schema 的 CRUD；不能把迁移 DSN 注入常驻服务。常规部署入口先构建 MCP，再显式迁移，后启动业务服务。公开核验 `/health/mcp`、两份 OAuth 发现文档、未授权 `/mcp` 的401挑战及 `/mcp/manage`。

个人 Key 与 MCP 委托管理升级须同批发布 auth、MCP 与前端，先显式执行账号迁移，新增 `purpose`、`deleted_at` 并按既有 `mcp.connect` scope 分类旧 MCP 凭据。个人列表不再包含委托凭据；缩权更新原凭据，撤销失败可在管理页重试清理。技能文件已迁至 `references/*.md`、本地工具迁至 `scripts/`；版本锁中的技能 SHA 必须包含该目录调整，导出的 `MCP_SKILLS_REVISION` 须与之相同，否则云端引用会返回不可用。验收应包含读取新引用路径、满额个人 Key 下建立 MCP 连接、缩权与撤销；`/mcp/manage` 应转至 `/developer?tab=mcp`。

账号旧显式 auth_rate_limit_per_minute=15 不会随代码默认值自动改变；升级时按已批准配置改为180。目录旧显式策略同样保留，须通过管理 API 核对实际全局、组和账号规则。目录同主体读写共用每分钟默认180预算，并由 PostgreSQL 跨副本计数；admin 默认无限，但显式账号或 admin 组规则可覆盖。身份来自验签，不依据调用方传入的 role 或通配 scope。

## 目录生命周期格式收敛

031 引入提交审核；032 将目录实体列与历史实体快照统一为 `state`，仅 active/deleted/merged，删除运行时旧字段回退。历史快照的题名、正文、属性、作者、来源、版本与时间保留，旧发布标记一次性归为正常或对应终态；业务属性中的 status 不改。已结案管理员提交记为 reviewed/exempt，其原始请求及摘要保留作不可变证据，不重新执行。旧 entity.unpublished 事件迁为普通保存事件，消费者只使用当前生命周期协议。存储域010同步将生命周期投影列改为state。

切换前停止旧目录写入，显式应用目录032并检查，再启用同批目录、社区与存储；目录启动要求与发布匹配的精确结构契约，不支持旧列、双写或降级读取。技能工具仅提供TypeScript，要求Node.js24+，不保留.mjs启动器；MCP技能资源与版本锁同步发布。

新版全量备份完成校验、隔离恢复及线上事实/历史和服务验收后，可按本次明确授权删除旧发布备份、过期回退配置和旧镜像锚点；保留一份新版完整恢复备份及其受限配置，不使用全局docker prune。

目录免审资格按账号当前权限组计算，PAT/MCP 的账号内省通过 `catalog_review_exempt` 传递，操作权限仍为账号权限与 scopes 的交集。账号服务、目录服务与 Key 编辑界面须同批升级；不以 Key 是否勾选 `catalog.review.bypass` 决定免审，也不根据组名回退授权。管理员的既有编辑 Key/MCP 连接无需重建，只读 Key 不可借本人条目所有权写入；撤回免审资格受最长60秒内省缓存限制，已入队提交不自动应用。

## AI 助手配置与恢复

034 为同进程 AI 助手建立 `assistant` schema，不新增独立服务、数据库或迁移命令。升级仍使用 `mf-migrate up`，并用库 owner 重跑 `roles-least-privilege.sql` 与 `verify-role-isolation.sql`。目录运行身份必须同时覆盖 `catalog` 和 `assistant`，其余服务不得访问 `assistant`；现有运行角色的 DDL 权限阶段不因新增模块自动收敛。

`AI_ENCRYPTION_KEY` 仅注入 backend，用于 AES-256-GCM 加密平台 Provider 和个人 BYO API 密钥。使用 `openssl rand -base64 32` 生成 base64 编码的32字节随机值，写入受限、未跟踪的部署配置，不输出真实值。留空允许读取后台配置与记录，凭据写入及模型调用不可用。该密钥与 `MCP_ENCRYPTION_KEY` 各自管理，不复用。

全库备份包含 `assistant` 中的配置、积分、预算、token 用量、会话和缓存；恢复可用凭据还必须保留与备份对应的 `AI_ENCRYPTION_KEY`。仅恢复数据库而丢失或直接更换密钥会使原凭据无法解密，须重新录入或先完成明确的凭据重新加密迁移。密钥只进入受限配置备份，不放入版本库或公开备份清单。

上线前先在后台配置 Provider、Model 的价格和额度、平台与供应方预算，再启用助手。使用可控测试模型验证：平台调用预留与结算、明确未调用或被拒绝时释放、用量未知时的保守记账、BYO 不扣积分、重复请求不重复扣费、编辑按原审核规则进入提交、重复采纳不重复奖励，以及介绍缓存命中与依赖变化后的失效。核对实际 token 统计和费用，不能仅凭问答返回成功断言账本正确。公开 MediaWiki 来源在后台单独配置，读取保留许可与署名；接入百科不自动写入目录事实。

按上游模型协议配置 `max_tokens_parameter=max_tokens|max_completion_tokens`，BYO 的 `upstream_model` 覆盖不放宽平台登记的 token 上限。中断恢复由 backend 每分钟回收超过10分钟的预留：未调用的释放，已调用但无权威用量的按预留上界记为 unknown。后台 `/api/assistant/admin/requests` 可查封存请求与部分结果；仅超过10分钟的 pending 可带说明取消，取消不撤销已执行修改、不删除用量或释放该请求 ID 重新执行。
