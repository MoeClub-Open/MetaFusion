# 统一审计留痕（audit log）跨服务契约

本文维护四服务统一审计的表结构、动作码、写入、读取、脱敏与验证契约。DDL 在独立仓库复制，
自动检查保证一致；业务修订与 OAuth 子域事务审计另有职责，不能把统一审计称为完整业务账本。

## 1. 规范 DDL 与同步落点

以下定义共享表契约；执行落点见 §6.1，授权与 owner 收敛唯一由 `deploy/sql/roles-least-privilege.sql` 管理。`scripts/check_audit_schema.py` 比对预建块与各仓副本的 SQL 语句，不证明目标实例结构或权限已匹配。新库预建须显式启用 `audit_bootstrap`，表已存在时的守卫只避免重复 DDL，不负责修复缺列、缺索引或错误 owner。

```sql
-- 表由部署时的 mf_audit_owner 预建；四个服务的运行角色执行这段 DDL 时整段空转。
-- 为什么必须用 to_regclass 守卫：PostgreSQL 的 CREATE INDEX IF NOT EXISTS 会**先做表所有权检查**、
-- 再看索引是否存在（CREATE TABLE IF NOT EXISTS 不同，它只要求 schema 的 CREATE）。各仓建表路径复用
-- 整段 DDL，不做守卫时非 owner 会遇到 42501 must be owner of table audit_log。
DO $audit_ddl$
BEGIN
  PERFORM pg_advisory_xact_lock(740205);   -- 四个服务共用的建表锁：先取锁再判断，才能串行化首次建表
  IF to_regclass('audit.audit_log') IS NULL THEN
    CREATE SCHEMA IF NOT EXISTS audit;
    CREATE TABLE audit.audit_log (
      id               uuid PRIMARY KEY,
      occurred_at      timestamptz NOT NULL DEFAULT now(),
      service          text NOT NULL,
      action           text NOT NULL,
      actor_user_id    uuid,
      actor_username   text NOT NULL DEFAULT '',
      credential_type  text NOT NULL DEFAULT '',
      actor_ip         text NOT NULL DEFAULT '',
      actor_user_agent text NOT NULL DEFAULT '',
      target_type      text NOT NULL DEFAULT '',
      target_id        text NOT NULL DEFAULT '',
      changes          jsonb NOT NULL DEFAULT '{}'::jsonb,
      result           text NOT NULL DEFAULT 'success' CHECK (result IN ('success','failure')),
      error_code       text NOT NULL DEFAULT '',
      request_method   text NOT NULL DEFAULT '',
      route            text NOT NULL DEFAULT '',
      http_status      int NOT NULL DEFAULT 0,
      request_id       text NOT NULL DEFAULT ''
    );
    CREATE INDEX audit_log_occurred_at_idx ON audit.audit_log(occurred_at DESC);
    CREATE INDEX audit_log_service_action_idx ON audit.audit_log(service, action, occurred_at DESC);
    CREATE INDEX audit_log_actor_idx ON audit.audit_log(actor_user_id, occurred_at DESC);
    CREATE INDEX audit_log_target_idx ON audit.audit_log(target_type, target_id, occurred_at DESC);
  END IF;
END
$audit_ddl$;
```

字段语义：

| 字段 | 语义 | 约定 |
| --- | --- | --- |
| `id` | 自生成的 uuid v4（应用侧生成，不依赖 pgcrypto） | 主键 |
| `occurred_at` | 服务端时间，默认 now() | 排序键 |
| `service` | `catalog` / `auth` / `community` / `storage` | 无 CHECK：新增服务不该要改旧服务的 DDL |
| `action` | 稳定机器码 `域.动作`，如 `user.role_changed` | 只增不改；改名等于改历史 |
| `actor_user_id` | 操作者 id | **不建外键**：账号删了审计还得在（与 `auth.oauth_audit.client_id` 同一理由） |
| `actor_username` | 操作者用户名**快照** | 账号改名/删号后仍可读 |
| `credential_type` | `session` / `pat` / `oauth` / `anonymous` / `system` | catalog/auth 按认证身份区分 session/pat/oauth；未登录为 anonymous；其余服务的限制见 §7 |
| `actor_ip` | 来源 IP（gin `c.ClientIP()`，取网关透传后的值） | |
| `actor_user_agent` | User-Agent，截断到 512 字符 | |
| `target_type` / `target_id` | 被动对象类型与 id（`user` / `entity` / `topic` / `asset`…） | id 一律用字符串，跨服务类型不一 |
| `changes` | 变更前后摘要 JSON，**必须脱敏**（§4） | 单条上限 8 KB |
| `result` | `success` / `failure` | CHECK 约束 |
| `error_code` | 失败时的稳定错误码（与响应体 `error` 字段一致），成功时为空 | |
| `request_method` / `route` | HTTP 方法 + **路由模板**（如 `/api/admin/users/:id/role`） | 存模板不存原始路径：原始路径没有额外信息，模板才可聚合 |
| `http_status` | 响应码 | |
| `request_id` | 透传 `X-Request-Id`，缺省生成并回写关联标识；格式由各入口生成器决定 | 与网关/应用日志关联，不依赖 UUID 格式 |

**不建**的列与理由：不存请求体原文（脱敏不可靠、体积不可控）；不存响应体（同上）；
不存操作者邮箱（敏感值，§4）。

## 2. 动作码清单

命名：`<域>.<过去式动作>`，全小写 + 下划线。域按业务对象分（`user` / `group` / `settings` /
`invite` / `oauth_client` / `pat` / `session` / `entity` / `relation` / `definition` /
`external_database` / `shelf` / `rate_limit` / `import` / `proposal` / `module` / `preference` / `notification` /
`board` / `topic` / `post` / `ban` / `asset` / `binding` / `file` / `moderation`）。

清单按服务分别落在各自实现的 `actions` 注册表里（路由模板 → 动作码），并由
「写路由覆盖守卫测试」（§6.3）保证没有漏网的写端点。

## 3. 写入语义

每个服务自带 `internal/audit` 包，动作码由本服务的路由注册表管理。

- `Recorder`：一个后台 goroutine + 有界 channel（容量 1024）。
  - `Record(entry)`：**非阻塞**入队；入队失败（队列满）时打 error 级日志并丢弃，**绝不阻塞业务响应**。
  - 落库失败：error 级日志 + 丢弃，**不回滚业务写入**。
  - `RecordSync(ctx, entry) error`：同步写接口；当前用于测试，没有业务强一致例外。
- 中间件 `audit.Middleware(rec, actions)`：挂在服务的写路由之前。
  - 请求进入：构造草稿（service / actor / IP / UA / request_id / method / route / action）。
  - `c.Next()` 返回后：填 `http_status` 与 `result`（<400 → success，否则 failure；
    错误码取 `c.GetString(audit.ErrorCodeKey)`，没有则回落 `http_<status>`），`Record` 入队。
  - 只有注册表里有动作码的请求才写审计；GET 与显式豁免路由不写。
  - `X-Request-Id` 缺省生成并回写响应头。
- 处理器补充被动对象与变更摘要：`audit.Describe(c, audit.Detail{TargetType, TargetID, Changes})`。
  为拿"变更前"值而多做一次读是允许的（只有写端点会多一次读）。

目录协作操作的成功记录额外捕获副本名称、提交说明、PR 标题、对象/提交数量和是否合入等轻量摘要。
只使用成功结果填充，不复制完整文档或把失败请求描述为已应用；历史行不回填、不改写。

统一审计旁路写入，不参与业务事务；当前动作没有强一致例外。业务成功而审计失败时记录错误并丢行。
OAuth 子域的 auth.oauth_audit 继续在业务事务内写入，历史行不复制到统一表，两者不能互相替代。

## 4. 脱敏（硬性）

写入前必须过滤，测试用正则断言库里**不存在**口令/令牌/完整邮箱：

- 键名精确黑名单（大小写不敏感）：`password`、`old_password`、`new_password`、
  `password_hash`、`token`、`access_token`、`refresh_token`、`token_hash`、
  `secret`、`client_secret`、`secret_hash`、`authorization`、`cookie`、
  `api_key`、`code_verifier` → 值替换成 `"[redacted]"`。
  （账号服务的实现额外遮了 `code_challenge`：它不是凭据（哈希），多遮一个属**允许偏差**，
  其余三个服务严格按上面这份清单实现。）
- 键名子串黑名单：含 `password` / `secret` / `token` / `hash` 的键一律 `"[redacted]"`。
  `hash_verified`、`token_prefix` 等也会被遮罩；需保留非敏感信息时使用不含这些词根的键名。
- **邀请码**（`auth.invites.code`）是准凭据：调用方必须用 `audit.MaskSecret(code)`
  写成 `abcd…`（保留前 4 位）后再放进 `changes`。
- **邮箱**：值里任何匹配邮箱正则的字符串 → `a***@domain`（保留首字母与域名）；键名含
  `email` 的字段一律遮罩。审计行里**不出现完整邮箱**。
- 长度：UA 512、单个字符串值 512、`changes` 序列化后 8 KB（超限时截断并加 `"_truncated": true`）。

## 5. 读取面

读取统一使用账号服务的 `GET /api/admin/audit-logs`，管理台与本人「我的操作记录」共用此入口，作用域按 `auth.audit.read` 区分：

| 档次 | 闸门 | 作用域 |
| --- | --- | --- |
| 全量 | 登录 + `auth.audit.read` | 下表所有过滤条件都能用，含 `actor_user_id` / `actor` 跨用户过滤 |
| 自助 | 仅登录（`requireUser`） | 强制 `actor_user_id` = 调用者本人；其余过滤条件（service / action / target / result / 时间 / request_id）照常可用 |

自助调用可省略 `actor_user_id` 或显式指定本人；指定他人或提供非空 `actor` 前缀均返回 `403 forbidden`，不静默改写越权条件。第三方 OAuth 身份不能读取此安全历史，返回 403；匿名返回 `401 authentication_required`。身份闸门与查询作用域分别校验。

| 参数 | 说明 |
| --- | --- |
| `service` | 精确匹配，可重复/逗号分隔 |
| `action` | 精确匹配，可重复/逗号分隔 |
| `actor_user_id` | UUID 精确匹配；自助只接受本人（非法 UUID → 400 `invalid_query`） |
| `actor` | 仅全量档使用的操作者用户名前缀（`ILIKE 'x%'`），大小写不敏感 |
| `target_type` / `target_id` | 精确匹配 |
| `result` | `success` / `failure`，其它值 → 400 `invalid_query` |
| `from` / `to` | RFC3339 时间，闭区间；非法 → 400 `invalid_query` |
| `request_id` | 精确匹配（与日志关联） |
| `page` / `per_page` | 默认 1 / 50，`per_page` 上限 200，越界 → 400 `invalid_query` |

响应：`{"items":[…],"total":N,"page":1,"per_page":50}`，排序 `occurred_at DESC, id DESC`（稳定分页）。

响应固定回显请求的 `page` / `per_page`（前端翻页与总页数都据此），`total` 是与 `items` 同一套过滤条件下的行数；过滤器参数为空串等于"不过滤"；`from` 只接受带时区的完整 RFC3339，`from > to` 直接 400 `invalid_query: to`；`changes` 为空对象时 `items[].changes` 是 `{}`（不是 null）。

匿名/系统操作的 `actor_username` 是空串（该列 NOT NULL DEFAULT ''），只有 `credential_type`（`anonymous`）能区分"匿名"与"用户名恰好为空"。

管理台：账号服务 `admin/` 的最小只读页（列表 + 过滤 + 分页），不做导出/图表/详情抽屉。
本人视角：站点设置页的「我的操作记录」始终显式传当前用户的 `actor_user_id`，包括管理员。
支持服务、操作、结果、UTC 日期范围、对象 ID、请求编号筛选及每页 20/50/100 条；筛选与分页使用同一查询条件。
按日期分组显示本地化操作名、对象摘要、结果与凭据类型；详情弹窗展示可读变更，IP、接口、请求编号及原始 JSON 放在折叠详情中。
标题优先使用操作时捕获的 `target_name`；旧记录打开详情时可按现有对象读取权限补读当前名称，并明确标记为当前名称，不能当成历史快照。
未知动作保留原始码供详情排查。切换用户立即清空私密列表，过期请求取消；读取失败与空记录分别展示。

读取与界面表述须保留以下完整性边界，个人页以折叠说明展示：

- 审计是旁路、最终一致（§3）：队列满或落库失败会丢行，且不补写；
- `changes` 是脱敏 + 截断后的摘要（§4），实体改动的完整前后值在 `catalog.revisions.snapshot`；
- `auth.oauth_audit` 不在本表内（§7 明确不回填），OAuth 授权记录走开发者中心的自助读取面；
- 账号注销后其审计行仍在（`actor_user_id` 无外键），但本人已无法登录查看。

## 6. 迁移与测试

### 6.1 迁移落点（只追加）

| 仓库 | 落点 | 说明 |
| --- | --- | --- |
| catalog（主仓） | `backend/migrations/000021_catalog_baseline.up.sql` 的 audit-ddl 区间 | 显式 `mf-migrate up` 建立，HTTP 启动只读检查 |
| auth | 无版本化迁移：`internal/audit` 的 `Schema` 常量由 `store.Init` 执行 | 照该仓既有做法；`cmd/server/main.go` 启动即执行 |
| community | `migrations/000007_audit_log.up.sql` | 启动与迁移工具读同一份（`internal/store.Init`） |
| storage | `internal/store/migrations/000002_audit_log.up.sql` | 启动时按版本号顺序应用 |

已发布迁移不可改写；预建块与各仓执行落点的规范 SQL 语句须一致，说明注释与包裹形式可以不同。后续结构变更须增加迁移并同步契约和检查，不通过改旧文件修复存量库。

### 6.1.1 建表守卫

回归须断言 §1 的完整守卫、守卫内 PERFORM 锁，以及不使用 CREATE INDEX IF NOT EXISTS。
表已存在时不执行所有权相关 DDL；缺表时锁内创建 schema、表与索引。迁移文件发布后不可改写已记账内容。

### 6.1.2 库侧授权（部署时必查）

共享对象归 mf_audit_owner，运行角色只有表 SELECT/INSERT，不能是 owner；schema 权限、显式预建和 F 段断言
见[数据库角色](./database-roles.md#41-共享审计表)。清理或归档使用运维身份，不能依赖应用角色权限。

### 6.2 真库用例（必须有）
- 每个被审计动作各产生**恰好一行**（按 request_id / 动作码查）；
- 敏感值正则断言：`password`、`mf_pat_`/`mfp_`、`sk_`、完整邮箱正则，在整行
  （`changes` 的文本形式）里**零命中**；
- 失败路径也留痕（result=failure + error_code）；
- 读取面（账号服务）：过滤组合、分页边界、非法参数 400。

### 6.3 写路由覆盖守卫（必须有）
遍历 gin 路由树，取所有 `POST/PUT/PATCH/DELETE` 路由（含 `/api` 组），断言：
每条路由要么在 `actions` 注册表里，要么在**豁免表**里且带一句豁免理由。
新增写端点忘了登记动作码 → 测试失败。

## 7. 已知取舍与未覆盖

- **不含读取操作**：审计只记写操作（含登录/登出这类写会话的动作）；GET 不记。
- **system 当前无写入方**：种子和迁移不走 HTTP 审计路径，不能据此判断系统内部动作不存在。
- **`auth.oauth_audit` 保留不迁移**：OAuth 子域已有的事务内审计继续写入；新表不复制它的历史行，
  完整 OAuth 史须核对事务审计与统一审计两处。
- **credential_type 是写入时的映射**：catalog/auth 已使用认证身份的 OAuth 判定；community/storage 仍记录 pat/session，旧行不改写，不能仅凭旧值或这两个服务的 session 值区分 JWT 用途。
- **不做保留策略与分区**：表会持续增长，清理/归档是运维议题（需要时再加）。
- **审计行对应用角色只可追加**（`deploy/sql/roles-least-privilege.sql` 已 REVOKE UPDATE/DELETE/TRUNCATE），但**不做防篡改的哈希链**：拥有 `mf_audit_owner` 的人仍可改写历史行。清理/归档只能用该 owner 角色。
