# AGENTS — MetaFusion 项目协作指南

本文件适用于本仓库；进入子目录前检查更具体的 `AGENTS.md`。规则冲突时遵循上级平台指令、用户明确要求和相应目录规则，不把旧文档当作运行时事实。

## 0. 本地记录

- `docs-local/` 是不进 git 的本机记录。按任务需要读取；仅在产生可复用的路径、部署信息、阶段决定或已知坑时更新，不记录操作流水。
- 不为一次性任务单独产出报告文件。可复用的路径、部署信息、阶段决定、已知坑和仍在生效的契约，直接写进本文件对应章节或 `docs-local/` 下的既有文件；同一批次的预检快照、过程回读和进度快照被最终结论取代后即删除，不以"留作历史"为由累积。
- 真实凭据不进版本库、日志或对话；本机可使用已忽略的专用凭据文件。

## 1. 协作与任务边界

- 使用中文回复。开始前用 `git status --short --branch` 了解现状；保留用户和他人改动，不为获得干净工作区而 reset / clean / stash。
- 先定位代码、契约与相关规则，再做最小必要修改。可并行且互不依赖的工作可交给子代理；代码开发、只读审查、真实数据写入仍是不同任务。
- 未获明确授权，不推送、发布、部署、写入远程实例或删改用户数据；范围变化、破坏性操作或身份歧义先说明。
- 不输出无关的 `.env`、密钥、PAT 或数据库导出。

## 2. Git 工作流

- 校验后按内聚的逻辑变更提交，避免把无关或未验证改动混在一起。
- 按明确路径暂存并检查 staged diff；暂存区已有内容时确认归属，只提交本任务文件。
- 提交信息采用 Conventional Commits 并遵循近期风格。需要新分支时从合适基线创建；未经授权不 force push。
- 检查失败时区分本次引入与既有/环境问题，只修本次问题；无法验证的内容在交付中说明。

## 架构基准与运行时

规范见 [架构基准](docs/architecture/spec-driven-requirements.md)：元数据主系统共用数据库一体化运行，类型/关系/字段走服务端 definitions 动态加载与多语言解析，外围论坛与存储独立解耦。不硬编码文案，不写夸张 Slogan。

统一入口 `/api`（实现 `backend/internal/catalog`），八种实体的规范前端详情路由均为 `/catalog/[id]`；实体 kind 只选择内容布局，不参与 URL 命名。固定实体骨架（Agent、Collection、Work、ContentUnit、Expression、Release、Medium、Track）+ Release.subjects + 跨 Work TrackContent；见 [元数据目录教程](https://github.com/MoeclubM/metafusion-docs/blob/main/docs/catalog.md)。

## 3. 项目导航与事实来源

MetaFusion 是类似 MusicBrainz / Bangumi 的开放元数据目录与受控资源分享站，不是通用知识库。

| 任务 | 优先入口 |
| --- | --- |
| 后端 API / 数据模型 | `backend/internal/catalog/`（统一入口 `/api`，路由见 `http.go:Register`） |
| 数据库与完整性约束 | `backend/migrations/*.sql` 是目录库结构迁移源，使用 `mf-migrate up` 显式执行；空库内容种子使用 `mf-migrate seed`，HTTP 服务启动只做兼容性只读检查；复合外键与校验逻辑见 `backend/internal/catalog/store.go`，只把已执行迁移视为目标实例能力 |
| 前端与国际化 | `frontend/src/`、`frontend/src/messages/{zh-CN,en-US,zh-TW,ja-JP}.json` |
| 用户和开发者文档 | `../metafusion-docs/`（独立仓库）：使用指南、编目规范、对外 API 与 Agent 接入；项目内部开发、迁移与部署记录仍在主仓 `docs/` |
| 子系统边界与迁移 | [子系统拆分与迁移契约](docs/architecture/service-split-migration.md)、[部署与恢复手册](docs/architecture/deployment-runbook.md)、[资源存储运行约定](docs/architecture/storage-operations.md)；账号 / 互动 / 存储分别在 `../metafusion-auth`、`../metafusion-community`、`../metafusion-storage` |
| 解耦状态与剩余工作 | [拆分契约](docs/architecture/service-split-migration.md)的“尚未完成的边界工作”：当前边界、服务 UI / 协议复用与待完成项；不另维护重复路线文档 |
| 部署与 CI | `deploy/docker-compose.yml`、`.github/workflows/ci.yml` |


技术栈：Go + Next.js / Bun + PostgreSQL + RustFS（S3）+ OpenSearch 2.x。

涉及 API 或数据行为时，以目标实例响应 + 实际处理器 + 已执行迁移为准；有矛盾记差异、停掉依赖写入，不改文案掩盖。接口或外部行为变化时，只同步直接受影响的 OpenAPI、用户和开发者文档及主仓内部契约说明。

- 加迁移时的固定耦合：`.github/workflows/ci.yml` 里 `mf-migrate down` 那一步的判据是"**最新一条迁移必须不可逆**"，并 grep 它的版本名。因此每新增一条 `NNNNNN_*.up.sql` 都要同步把该步骤的 grep 改成新的最新版本（当前锚点是 `000028_catalog_shared_budget`），否则 CI 会在 down 步骤红——而本机没有 PostgreSQL 时不会发现。

## 4. 按改动范围验证

根据改动范围和风险选择必要检查；命令在“目录”列位置运行，版本以 manifest 和 CI 为准。

| 改动范围 | 目录 | 参考检查 |
| --- | --- | --- |
| 后端 | `backend/` | `go test ./...`、`go vet ./...`、`go build ./cmd/server ./cmd/migrate` |
| 前端 | `frontend/` | `bunx tsc --noEmit`、`bun run build`；UI 改动看中英显示 |
| 文档站 | `../metafusion-docs/`（独立仓库） | `bun run build`；核侧栏与示例字段；改完那边提交 |
| Compose | 仓库根目录 | `docker compose -f deploy/docker-compose.yml config --quiet`；不输出密钥 |
| AGENTS / 技能 | 所属仓库 | 路径、Markdown、契约一致；有验证器就跑 |
