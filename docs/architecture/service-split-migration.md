# 服务边界与迁移契约

本文维护各服务的数据所有权、网关路由、身份协议及尚未完成的边界工作。目录模型见[架构基准](./spec-driven-requirements.md)，发布、备份和回退见[部署与恢复手册](./deployment-runbook.md)。运行时以处理器、目标实例及已执行迁移为准。

## 1. 运行单元

| 运行单元 | 职责与数据 | 对外入口 | 仓库 |
| --- | --- | --- | --- |
| catalog | 八实体、动态定义、结构、关系、修订、搜索、货架、外部库与通知；catalog.* | 目录 API 与目录管理台 | MetaFusion（本仓） |
| auth | 账号、会话、令牌、OAuth2/OIDC、权限组和开发者中心；auth.* | 账号 API、自助应用与管理台 | metafusion-auth |
| community | 论坛、短评、合集、收藏和私信；community.* | 互动 API 与管理台 | metafusion-community |
| storage | 文件、哈希、去重、上传、绑定和访问控制；storage.* | 存储 API 与管理台 | metafusion-storage |
| gateway | 同域入口、分流、限流和安全响应头；无业务数据 | `/`、`/docs` 与 API | 本仓 deploy/nginx.conf；metafusion-api-gateway 提供验收脚本 |
| docs | 用户教程与 API 文档；无业务数据 | `/docs` | metafusion-docs |
| skills | 编目操作与身份判断规则；非运行时 | 无 | metafusion-skills |

主前端通过同域网关调用各服务。`NEXT_PUBLIC_RESOURCE_STATION_URL` 用于外部资源站，未配置时隐藏入口；`NEXT_PUBLIC_DOCS_URL` 默认同域 `/docs`。账号、互动和存储使用网关路径，不另设外部地址开关。

## 2. 路由归属

唯一生效的路由矩阵是 `deploy/nginx.conf`。下表登记路径族，逐条精确匹配、正则和静态资源重写以配置为准；完整请求格式归[公开 API 文档](https://github.com/MoeclubM/metafusion-docs)。

| 归属 | 路径 | 说明 |
| --- | --- | --- |
| auth | `/api/setup`、`/api/auth/*` | 初始化、注册登录、会话、邀请、PAT 与自助授权 |
| auth | `/api/admin/users*`、`/api/admin/groups*`、`/api/admin/permissions`、`/api/admin/settings`、`/api/admin/invites*` | 账号与权限管理；同 admin 前缀逐条分流 |
| auth | `/api/admin/audit-logs` | 跨服务审计的唯一读取面，见[审计契约](./audit-log.md) |
| auth | `/api/oauth/*`、`/api/oidc/jwks`、`/api/.well-known/openid-configuration`、`/.well-known/*` | 授权、discovery 与公钥；只有 auth 签发令牌 |
| auth | `/api/developer/*`、`/api/admin/oauth/*` | 开发者自助登记与管理员客户端治理 |
| catalog | `/api/catalog/*`、`/api/importer/*`、`/api/exchange/*`、`/api/openapi.json` | 目录、导入、交换与运行时 API 契约 |
| catalog | `/api/admin/catalog-definitions*`、`/api/admin/external-databases*`、`/api/admin/shelves*`、`/api/admin/rate-limits` | 目录定义、外部库、货架与额度策略 |
| catalog | `/api/notifications*` | 通知收件箱；internal 投递由调用服务凭 INTERNAL_API_TOKEN 授权 |
| catalog | `/api/version`、`/api/capabilities` | 运行版本身份和[声明式能力](./capabilities.md) |
| community | `/api/community/*`、`/api/favorites/*`、`/api/messages/*` | 论坛、短评、合集、举报/申诉、收藏和私信 |
| auth | `GET /api/users/:id` | 公开账号资料，正则精确分流 |
| community | `/api/users/:id/favorites`、`/api/users/:id/stats` | 收藏与互动统计，正则分流 |
| catalog | `/api/users/:id/contributions` | 目录贡献，走目录兜底 |
| storage | `/api/storage/*` | 上传、资产、绑定、下载和内容 |
| storage | `/storage/preview/*` | 显式返回 404，不直代私有桶 |
| auth | `/admin/account/*` | 独立账号管理台，转发 auth-admin；无尾斜杠时补齐 |
| auth | `/login`、`/setup`、`/auth-user-assets/_next/static/*` | 独立账号自助应用，转发 auth-user；专属静态前缀避免同域冲突 |
| community | `/admin/community/*` | 独立互动管理台，转发 community-admin |
| storage | `/admin/storage/*` | 独立存储管理台，转发 storage-admin |
| mcp | `/mcp`、`/mcp/*` | 独立云端 MCP、授权与权限管理，业务调用转现有 API |
| mcp | `/.well-known/oauth-protected-resource/mcp`、`/.well-known/oauth-authorization-server/mcp/oauth` | MCP OAuth 发现，不与账号 OIDC 混用 |

`/api/users/*` 有多种归属，不能按顶层前缀整体改上游。网关仓 `cutover-check.sh` 检查服务标记头，主仓 `scripts/check_gateway_matrix.py` 检查路径登记、归属和限流，`scripts/check_versions.py` 检查组合版本。新增路径须同步具体分流与契约检查，不能靠目录兜底掩盖漏项。

## 3. 数据所有权与调用

各服务只读写本域 schema，不建跨业务外键、不跨服务 JOIN。运行角色与共享审计的唯一授权源为 `deploy/sql/roles-least-privilege.sql`，验证为 `verify-role-isolation.sql`；详细身份、对象归属和撤权条件见[数据库角色](./database-roles.md)。

community/storage 通过目录 HTTP 解析当前实体身份与可见性，使用 `/api/catalog/entities/{id}/identity` 的 canonical、kind 与别名。404 表示不存在或不可见，超时与上游故障必须明确失败，不能默认公开、返回假空集合或无限重试。capabilities 只声明部署状态，不探活外围服务。

目录合并写入 entity.merged outbox，修订和事件与事实同事务。OpenSearch 与启用的 storage-lifecycle-v1 分别以 consumer/event_id 的 deliveries 确认成功；存储生命周期通过受密钥保护的内部 HTTP 投递，详情见[存储运行约定](./storage-operations.md)。尚无通用跨业务广播消费者。搜索消费与重投细节见[核心实现](./catalog-core-implementation.md#搜索与索引同步)。

目录结构由 `backend/migrations/*.sql` 与显式 `mf-migrate up` 管理，内容种子用 seed。目录与账号 HTTP 启动只读检查，账号使用独立结构 DSN 的 auth-migrate。互动和存储启动应用各仓嵌入迁移并写入自己的 schema_migrations。新增迁移入口须核对锁的作用域和顺序，不能把运行写入锁或共享审计锁当成迁移锁。

存储不复制作品、专辑或曲目表，目录不保存对象物理路径。binding_role 表示文件用途，TrackContent.locator 表示目录中的区间与位置，两者不能重复存储。资产持久化、鉴权与稳定 URL 只维护于[存储运行约定](./storage-operations.md)。

## 4. 身份与权限

云端 MCP 在独立 `metafusion-mcp` 仓库维护，只拥有 `mcp` schema，保存客户端、资源绑定令牌、加密的受限 PAT 与不可变提交草稿。通过账号 HTTP 登录与 PAT 权限交集复用既有 API；不接管 Agent 的外部信息获取。运行角色只持本域 CRUD，DDL 由显式 mcp-migrate 完成。目录同主体业务读写共用默认180 RPM，PostgreSQL 共享固定分钟窗口；admin 默认无限，显式账号/组策略可覆盖。网关仅在登录、注册、刷新、安装与令牌精确路径保护未验证凭据，不按共享出口 IP 限业务请求。认证失败次数由账号库共享计数。

只有 auth 持签发私钥。目录、互动和存储验签 RS256 JWT，公钥来自静态配置或账号 JWKS；目录不派生签发私钥、不查账号表、不签发令牌。PAT 通过账号服务内省，会话续期由账号服务处理。

issuer/audience 由四服务一致配置，默认 `https://findverse.cc/api` / `metafusion`；变更须处理存量令牌有效期。Compose 的验签服务 JWKS 指向 `http://auth:8081/api/oidc/jwks`，目录也可配置 AUTH_JWT_PUBLIC_KEY。

auth 将组展开为 permissions，业务服务按权限码判定自己的操作；空权限不授权，组名与历史 role 不提供兜底。目录还复核具体实体可见性和编辑范围，第三方 OAuth 身份不能执行目录治理。签发、验签和业务授权的职责不能因协议复用改变。

## 5. 尚未完成的边界工作

以下是保留的后续需求，不代表当前已交付；实施须有实际收益与行为等价验收。

| 范围 | 当前边界与后续要求 |
| --- | --- |
| 迁移与运行权限 | 互动、存储仍在启动执行 DDL；目录和账号已有 owner 显式迁移及只读启动。各域须验空库安装、存量升级、共享审计归属、新对象授权与受限启动，再撤运行角色 DDL；不能先撤权 |
| 协议复用 | 尚未统一接入 metafusion-sdk。仅抽取稳定身份、验签、错误、分页、health 与请求标记；比较 Cookie/PAT/JWT、JWKS 失败、空权限、过期和各服务分页语义，按版本接入并删除被替代实现。业务模型、SQL 与可见性仍归各服务 |
| 独立发布 | 主仓 backend/frontend/migrator 有固定摘要发布物，兄弟服务与 UI 由锁定检出构建。独立镜像须扩展发布清单、同 SHA 检查及整组回退，不能使用 latest 或来源不明旧镜像 |
| 普通用户 UI | 管理台和账号登录/初始化应用已独立，其余账号/开发者页及目录内社区/资源区块仍有主仓耦合。先明确设计令牌、四语字典、会话和导航协议，以及区块 locale、身份、entity_id、能力与故障隔离，再按发布需求迁页；不复制业务状态掩盖耦合 |
| 收藏隐私 | 社区接口仍返回 visible=true，前端开关为只读占位。真正的公开/私有设置应由互动服务定义并验证权限 |
| 跨业务事件 | 已有存储生命周期定向投递，尚无通用广播。新增消费者先定义推送/拉取、event_id 幂等、重试/死信、可观测性、重放与 merged 重定向 |
| 物理分库与矩阵迁移 | 独立 DSN 已提供权限边界，物理分库尚未实施，不复制目录身份或引入跨库 SQL。若迁移网关矩阵，须一次切换权威来源、固定版本输入并保留双向检查 |

契约生成与跨仓检查持续覆盖权限码、kind、OpenAPI 和路由；接口变化只同步直接受影响契约。容量基线见[核心实现](./catalog-core-implementation.md#查询规模费用与接口边界)。转码、媒体分析、消息队列和独立库不会因路线描述成为现有能力。

## 6. 发布与回退边界

回退不能仅把前缀指回主仓：旧处理器可能已移除，旧服务可能不理解新 schema 或 JSON。先停受影响写入，核对路径、迁移和切流后的新数据，再按[部署与恢复手册](./deployment-runbook.md)使用已验证的镜像与兄弟仓组合；需要时恢复备份并处理恢复点后的写入。
