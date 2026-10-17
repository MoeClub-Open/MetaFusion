# 数据层角色与最小权限

四个业务服务共用 PostgreSQL 实例与数据库，各用独立运行角色和 schema。唯一授权来源为 `deploy/sql/roles-least-privilege.sql`，库侧断言为 `deploy/sql/verify-role-isolation.sql`；服务职责见[拆分契约](./service-split-migration.md)。本文维护身份、共享对象和撤权条件，发布步骤见[部署与恢复手册](./deployment-runbook.md)。

## 1. 角色模型

| 服务 | 运行角色（LOGIN） | 对象归属角色（NOLOGIN） | schema |
| --- | --- | --- | --- |
| catalog（含 AI 助手模块） | mf_catalog | mf_catalog_owner | catalog、assistant |
| auth | mf_auth | mf_auth_owner | auth |
| community | mf_community | mf_community_owner | community |
| storage | mf_storage | mf_storage_owner | storage |
| 共享审计 | 四个运行角色读写 | mf_audit_owner | audit |

运行角色为 NOSUPERUSER、NOCREATEDB、NOCREATEROLE、NOREPLICATION、NOBYPASSRLS。授权按 schema 覆盖现有对象和默认权限，不另维护逐表白名单；业务 schema 不向其他服务授予使用权。库 owner 保留为结构迁移和运维身份。

| 对象 | 权限边界 |
| --- | --- |
| 本域表、序列与函数 | 运行角色执行本域业务读写 |
| 其它业务 schema | 不读、不写；身份和可见性走 HTTP |
| audit.audit_log | 四个运行角色 SELECT/INSERT，不得 UPDATE/DELETE/TRUNCATE |
| public.schema_migrations | 目录迁移账本，由运维身份管理，不授予运行角色 |

目标实例是否落实权限，以库侧断言和受限角色真实启动为准；实例快照不作为跨部署契约。

AI 助手与目录运行在同一 backend 进程和数据库。`assistant` 保存 Provider/Model 配置、加密凭据、积分账本、调用用量、预算、会话、生成介绍缓存及公开来源配置；不另建账号、令牌或目录事实副本。它与 `catalog` 共享 `mf_catalog_owner` 和运行身份 `mf_catalog`，auth/community/storage 均不得访问。授权与隔离脚本将两个 schema 视为同一服务的本域，避免逐 schema 撤权时撤掉另一域的授权；覆盖检查仍逐域执行。

## 2. 对象所有权

目录结构来自 `backend/migrations/`，其它域来自各仓 DDL 或迁移入口。授权脚本收敛本域 schema 与全部对象的 owner，并为 owner 和迁移身份创建的新对象设置默认权限。新迁移后仍须重验授权，尤其共享对象的表权限。

ALTER OWNER 会获取对象锁，存量接管应放在维护窗口。不能仅因当前角色能 CRUD 就推断它能执行 DDL，也不能仅因对象已存在就认为 IF NOT EXISTS 无权限要求。

## 3. 从 DDL 运行角色到纯 CRUD

当前授权脚本默认 Tier 1：运行角色为本域 owner 的成员，并持库级 CREATE，以支持仍在启动执行 DDL 的互动和存储。它们不能访问别的业务 schema，但仍能改本域结构和新建 schema。目录与账号 HTTP 已只读检查；目录 DDL/种子通过 mf-migrate 显式执行，账号 DDL/初始化通过 auth-server -migrate 显式执行。目录、账号可按下面验收顺序撤销运行角色 DDL，不能据代码变化推断目标实例已撤权。

Tier 2 的目标是运行角色仅有 CRUD。按服务实施 owner 迁移与只读启动后，先验空库安装、存量升级、共享审计归属、默认权限和受限启动，再执行脚本第 6 节的 owner 成员与库级 CREATE 撤销。不能先撤权后依赖旧启动路径；测试夹具所需 CREATEDB 也不能授予线上运行角色。

## 4. 跨域例外

| 例外 | 身份与用途 |
| --- | --- |
| 共享审计表 | mf_audit_owner 负责结构与清理；运行角色只追加及读取，读取面在账号服务 |
| community-migrate | 切流搬运的独立管理身份，可读旧单体数据、写 community；不并入任何运行角色 |
| public 迁移账本 | mf-migrate 使用 DB_* 中的运维身份，不能改指向运行角色以规避 owner 分离 |

### 4.1 共享审计表

审计 DDL 取事务锁后，仅在 `to_regclass('audit.audit_log') IS NULL` 时创建表与索引。表已存在时不执行建表和建索引，避免非 owner 执行 CREATE INDEX；规范与同步落点见[审计契约](./audit-log.md#1-规范-ddl-与同步落点)。存在性守卫不验证表结构，源码副本检查也不能代替目标实例验收。

审计 schema 与表归 mf_audit_owner，运行角色不能是该 owner；owner 的隐式权限不能靠 REVOKE 消除。默认授权脚本不改变 audit 归属，须显式 `-v audit_bootstrap=1` 预建或收敛，再验 F 段。该参数只控制共享审计，本域对象仍按脚本第 2 节收敛。

预建以运维身份执行守卫后再修改 owner，不能提前 SET ROLE mf_audit_owner 执行 CREATE SCHEMA。运行角色须获 audit schema 使用权及当前启动契约要求的权限，否则无法解析或执行守卫；表权限在对象创建后授予。verify-role-isolation 的 owner 与只追加断言失败不能忽略。

### 4.2 新共享对象

新增共享 schema、表、扩展或函数前，明确唯一结构身份，其余服务只校验。多仓仍持 DDL 副本时，使用对象不存在才建的守卫，并以自动检查保证一致；不要依赖 IF NOT EXISTS 消除所有权要求。各服务的迁移账本互相独立，不能证明共享对象由谁创建。

## 5. 配置与身份

业务容器只接收显式 DATABASE_URL，不回退共用 DB_*。Compose 四个运行 DSN 必填；搬运工具另用独立管理 DSN。

| 输入 | 使用者 |
| --- | --- |
| CATALOG_DATABASE_URL | 目录运行进程 |
| AUTH_DATABASE_URL | 账号运行进程 |
| AUTH_MIGRATION_DATABASE_URL | 一次性 auth-migrate，账号结构 owner 身份 |
| COMMUNITY_DATABASE_URL | 互动运行进程 |
| STORAGE_DATABASE_URL | 存储运行进程 |
| COMMUNITY_MIGRATE_DATABASE_URL | 一次性 community-migrate |
| DB_* | mf-migrate、数据库容器或运维；不是业务运行配置 |

键位登记与注入检查见 `scripts/check_env_matrix.py`，凭据写入未跟踪的部署配置，不进文档、日志或对话。迁移工具有意使用结构身份，不需要给运行角色补 public 账本权限。

## 6. 授权、验收与回退

1. 备份并确认维护窗口，准备各域运行 DSN 与运维身份。授权脚本需要库 owner 或超级用户，口令通过受控 psql 变量传入；不提供口令变量时只建角色，口令另设。
2. 执行 `roles-least-privilege.sql` 收敛本域对象和权限。新库或 audit owner 不符时，显式传 `-v audit_bootstrap=1`，收敛共享表归属和权限。
3. 执行 `verify-role-isolation.sql`，A/B/C/D/F 均通过；owner 错误先修归属，不能仅补 GRANT。
4. 用各运行角色重建相关服务，检查 ready、真实读取和必要写入；离线 SQL 通过不能证明启动契约可用。
5. 运行 `python scripts/check_audit_schema.py` 及 `--selftest` 核对跨仓 DDL；每次新迁移、共享表变更或撤权后重验。

授权脚本可重复执行。撤权或角色回退按脚本第 6 节顺序：先将服务切换至已验证且权限足够的显式 DSN，再撤权，最后删除已无人使用的角色；运行配置不能退回 DB_*。完整恢复按部署手册，实例版本和结果只记 docs-local。

尚未统一的 owner 迁移、运行权限和物理分库需求，只维护于[剩余边界工作](./service-split-migration.md#5-尚未完成的边界工作)。
