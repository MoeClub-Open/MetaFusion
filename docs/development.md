# 本地开发与验证

本文维护主仓的开发入口、配置分工与检查选择。协作规则见 [AGENTS.md](../AGENTS.md)，安装、授权和升级见 [部署与恢复手册](architecture/deployment-runbook.md)；用户教程与完整 API 请求只在 [metafusion-docs](https://github.com/MoeclubM/metafusion-docs) 维护。

## 环境与配置

Go 版本取 `backend/go.mod`，Bun 版本取 `frontend/package.json` 的 `packageManager`，依赖按锁文件安装。Python 3 用于根目录契约检查；`check_deploy.py`、`check_env_matrix.py` 另需 PyYAML。Compose 和部署脚本需要 Docker Compose v2 与 Bash；Windows 可在 WSL 中运行 Bash 脚本。

Next 应用使用 TypeScript 7，源码分析脚本通过独立的 `typescript-api` 包使用 TypeScript 6 编译器 API。Tailwind 4 的 PostCSS 插件配置在 `frontend/package.json`，主题由 `tailwind.config.ts` 与全局 CSS 提供；`check-design-tokens.ts` 使用同一 PostCSS 入口生成并检查样式。维护脚本直接由 Bun 运行 TypeScript，业务源码及配置的类型检查与脚本分开。

Next 应用将 Tailwind 间接依赖的 `postcss-selector-parser` 统一到修复 CPU 耗尽问题的 7.1.6；主前端也将 Markdown 与编辑器共用的 KaTeX 统一到 0.19，避免旧渲染器残留及样式版本不一致。修改这些 overrides 后必须重跑设计令牌、公式渲染测试和各应用构建。

仅做编译、前端单元测试或离线契约检查时不必启动完整服务。需要整站联调时，将 auth、community、storage、gateway、docs 与主仓并列检出，发布组合取 `deploy/versions.lock`。开发分支可独立演进，发布前必须核对组合；检查缺仓而 SKIP 不等于兼容性通过。

从根目录 `.env.example` 建立本地 `.env`，自行设置隔离环境的配置。`deploy.sh` 会加载根目录或 `deploy/` 的 `.env`；直接运行 Go 不自动加载该文件，需向进程注入环境变量。Next.js 按自身规则加载 `frontend/.env.local`。

| 进程 | 配置入口与边界 |
| --- | --- |
| 目录 HTTP | `DATABASE_URL`、`OPENSEARCH_URL` 必填；`PORT` 默认 8080。认证配置见 `backend/cmd/server/main.go`，私钥只归 auth |
| 目录迁移器 | 读取 `DB_HOST`、`DB_PORT`、`DB_USER`、`DB_PASSWORD`、`DB_NAME`、`DB_SSLMODE`，**不读取 `DATABASE_URL`**；使用有迁移权限的独立身份 |
| Compose | 将 `CATALOG_DATABASE_URL` 等各域 DSN 注入对应容器；迁移器用运维 `DB_*`，不能把其身份交给常驻服务 |
| 主前端 | 浏览器使用同域 `/api`；Next 开发服务器通过 `BACKEND_ORIGIN` 转发，默认 `http://127.0.0.1:8080`。完整联调时应显式指向实际网关来源，开发服务器同时转发 `/login`、`/setup` 与 `/auth-user-assets/*`，让账号页面及其资源保持同源；auth/community/storage 请求也由网关分流 |

## 启动目录与前端

以下命令只用于已准备好 PostgreSQL 与 OpenSearch 的隔离开发环境。先设置上表中的环境变量，并按数据库角色契约准备共享审计与运行权限；迁移身份和 HTTP 身份分别使用各自配置。

在 `backend/` 中初始化或升级目录库：

```text
go run ./cmd/migrate up
go run ./cmd/migrate seed
go run ./cmd/migrate check-refs
go run ./cmd/server
```

HTTP 启动不会替代前三步。只联调公开读取可不接账号服务；未配置验签来源时，认证写入按匿名拒绝。需要保存时接入 auth/JWKS 或 PAT 内省，不能凭前端登录状态判断目录已取得身份。

另开终端，在 `frontend/` 中运行：

```text
bun install --frozen-lockfile
bun run dev
```

Next 默认使用 3000 端口。只连接目录后端时可联调目录读取，不具备账号登录与初始化功能；账号页面由 `metafusion-auth/user` 维护，完整联调应按上表连接网关。检查真实实体回读和 `/health` 的搜索就绪状态；目录 `/ready` 只检查数据库。

整站开发采用根目录的 `bash deploy/deploy.sh dev`，前提是目录库的迁移、种子与权限已经准备好。它先显式执行账号迁移，再挂载目录、主前端和文档源码；目录 Go 代码修改后需 `bash deploy/deploy.sh restart backend` 重新编译，兄弟服务的构建方式以覆盖编排为准。`dev` **不会执行目录迁移、seed 或 check-refs**。

增量 `bash deploy/deploy.sh fast` 的全量更新与 `fast backend` 会在启动目录服务前依次执行目录迁移 `up`、`seed` 和 `check-refs`，任何一步失败均保持目录服务停写；其它指定服务的 `fast` 更新不执行目录迁移。涉及结构或协议升级时，按部署手册核对发布组合与停写要求，也可使用 `prod/pull` 或显式迁移流程。

## 按改动选择检查

完整 CI 是 `.github/workflows/ci.yml`，下面列出常用本地入口；检查应覆盖实际改动和依赖条件。

| 改动 | 运行目录 | 检查 |
| --- | --- | --- |
| 后端 | `backend/` | `go test ./...`、`go vet ./...`、`go build ./cmd/server ./cmd/migrate`；并发逻辑加 `go test -race ./internal/catalog/` |
| 路由 / OpenAPI | `backend/` | `go test ./internal/catalog/ -run TestOpenAPI`（含路由双向覆盖） |
| 前端 | `frontend/` | `bunx tsc --noEmit`、`bun test tests`、`bun run build`；UI 核四语与实际响应 |
| 文案 / 页面样式 | `frontend/` | `bun scripts/check_i18n_usage.ts`、`bun scripts/check_jsx_literals.ts`、`bun scripts/check-design-tokens.ts`；四语键集合比对步骤见 CI |
| 页面外壳 | 根目录 | `bun scripts/check_page_shell.ts` |
| 编排 / 环境配置 | 根目录 | `python scripts/check_deploy.py`、`python scripts/check_env_matrix.py`，以及对应 Compose 的 `config --quiet`；不要打印解析后的真实配置 |
| 路由 / 跨仓契约文档 | 根目录 | `python scripts/check_gateway_matrix.py`、`python scripts/check_doc_routes.py`；端点检查也读取邻近文档站和技能仓 |
| 审计 DDL | 根目录 | `python scripts/check_audit_schema.py`；同时保留各服务数据库与写路由覆盖测试 |
| CI 入口 | 根目录 | `python scripts/check_ci_scripts.py`；支持 `--selftest` 的脚本可先离线自测 |
| 文档 | 所属仓库 | 核相对路径、标题锚点与命令来源，`git diff --check`；文档站另外运行其 `bun run build` |

后端数据库集成测试读取 `MF_V2_TEST_DSN`，要求 PostgreSQL URL 的库名以 `mf_v2_test` 开头，且身份能在该测试服务器创建和删除临时数据库。搜索集成测试读取 `MF_OPENSEARCH_TEST_URL`，会建立和清理测试索引；两者均使用专用测试实例。未配置时相关测试会跳过，`go test` 成功不能据此宣称数据库或搜索集成已验证。CI 会提供 PostgreSQL 与 OpenSearch 并执行这些检查。

生成物从来源重建，不手工改：权限码/kind 用 `bun run contracts:build`，语言表用 `bun run langs:build`，主题令牌用 `bun run theme:build`（均在 `frontend/`）。对应 `contracts:check`、`langs:check` 和设计令牌检查守住漂移；改契约时应让实际兄弟仓到位，避免缺仓沿用旧生成值。

新增迁移须同步 CI 的不可逆 down 守卫版本，已发布迁移不可改写。只暂存本任务路径并检查 staged diff，再按内聚变更提交；本地检查不能替代目标实例迁移、回读或部署验收。

本地临时文件和构建缓存可用 `pwsh -File scripts/clean-local.ps1` 预览；加 `-Apply` 执行删除。`-IncludeGitArtifacts` 额外纳入脚本中明确列出的旧运维文件，不扫描其他 Git 内部文件。脚本在删除前核对全部路径均在仓库内、不包含受版本控制的文件或链接；环境审批仍适用于删除操作。
