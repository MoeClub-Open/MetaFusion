-- ==============================================================================
-- 每服务独立数据库角色（同库多角色最小权限）—— 数据层隔离的唯一授权脚本
--
-- 背景（2026-09 审计 P0）：四个服务原先共用同一个库用户 metafusion 与同一个库
-- metafusion_db，schema 只是命名约定——任一服务拿到自己那份凭据，就能读写别人的全部数据。
-- 本脚本把"独立 schema"从命名约定变成库侧权限边界，且**不需要数据搬迁**（不是每服务独立库）。
--
-- 角色模型（每个服务一组）：
--   | 服务 | 运行角色（登录，写进连接串） | 结构归属角色（NOLOGIN） | schema    |
--   | ---- | ---------------------------- | ----------------------- | --------- |
--   | 目录 | mf_catalog                   | mf_catalog_owner        | catalog / assistant |
--   | 账号 | mf_auth                      | mf_auth_owner           | auth      |
--   | 互动 | mf_community                 | mf_community_owner      | community |
--   | 存储 | mf_storage                   | mf_storage_owner        | storage   |
--
-- 为什么运行角色仍持有本域 DDL（owner 成员身份 = Tier 1），而不是纯 CRUD：
--   目录 HTTP 启动只读校验；目录 DDL/种子由运维身份显式执行 up/seed。
--   auth `store.Init`、community `store.Init`、storage `store.Init` 仍在启动路径迁移；DDL 见
--   `backend/migrations/000021_catalog_baseline.up.sql`、`../metafusion-auth/internal/store/store.go`、
--   `../metafusion-community/migrations/000001_init.up.sql`、
--   `../metafusion-storage/internal/store/migrations/000001_init.up.sql`）。
--   PostgreSQL 对 `CREATE SCHEMA IF NOT EXISTS` / `CREATE TABLE IF NOT EXISTS`
--   **先做权限检查、再看对象是否存在**（实测：结构已建好、数据都在，只给 CRUD 的角色仍报
--   `permission denied for schema <x>`；`CREATE SCHEMA IF NOT EXISTS` 还额外要求库级 CREATE），
--   三个仓库里还有 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`（要求对象所有权）。
--   因此其余三域的运行角色尚不能只持有 CRUD；要统一落地纯 CRUD 运行角色，前提是
--   "启动只校验、迁移由 owner 单独跑"（审计 §4.3 第 3 条，未实现）。
--   本脚本的做法是：运行角色 = 本域结构归属角色的成员，**权限边界仍然只在本域 schema 内**，
--   这正是 P0 要堵住的"跨服务读写"。第 4 节的 CRUD 与默认权限已经备好：把启动迁移拆出去之后，
--   执行第 6 节的 Tier 2 降级段即可切成纯 CRUD，无需再改授权清单。
--
-- 顺带保留的库级权限：CREATE ON DATABASE —— auth/community/storage 的启动路径仍执行
--   `CREATE SCHEMA IF NOT EXISTS`（对象已存在时 PostgreSQL 依然检查库级 CREATE）；目录 HTTP 只读校验。
--   最坏后果是某服务能在库里新建自己的 schema，不能读写别人的 schema 对象。
--
-- 跨域例外（不在本脚本的按服务授权里，见 docs/architecture/database-roles.md 第 4 节）：
--   1) 互动服务的 community-migrate（切流窗口一次性搬运，要读 modules.* 与 catalog.favorites）；
--   历史搬运由**库 owner 身份**执行；本脚本第 3 节把四个 owner 角色授给库 owner，
--   使运维迁移路径在原身份下保持 DDL 能力（deploy.sh 用的就是库 owner 凭据）。
--   2) 共享审计表 audit.audit_log：四个服务都要写它（契约见 docs/architecture/audit-log.md）。
--      schema 与表由第 3b 节**预建**、owner 固定为 mf_audit_owner（运行角色不能是 owner，
--      owner 隐式持有全部权限且 REVOKE 不掉）；第 4b 节只给 USAGE/CREATE 与 SELECT/INSERT。
--      DDL 与四份服务副本逐字一致，由 scripts/check_audit_schema.py 自动比对（CI）。
--   另外，目录的**迁移工具**（backend/cmd/migrate → internal/config）只读 DB_*、不读 DATABASE_URL，
--   因此 deploy.sh migrate 用的仍是库 owner 身份与 public.schema_migrations 账本——
--   这是刻意保留的“结构变更身份”；若将来把它也切到服务角色，见第 6.4 节要补的权限。
--
-- 用法（必须由库 owner 或超级用户执行；幂等，可重复跑）：
--   psql "$OWNER_DSN" -v ON_ERROR_STOP=1 -f sql/roles-least-privilege.sql
-- 口令不写进仓库，用 psql 变量在同一次调用里设置（不给变量就跳过建口令，稍后用 ALTER ROLE 补）：
--   psql ... -f sql/roles-least-privilege.sql \
--     -v catalog_password="$CATALOG_DB_PASSWORD" -v auth_password="$AUTH_DB_PASSWORD" \
--     -v community_password="$COMMUNITY_DB_PASSWORD" -v storage_password="$STORAGE_DB_PASSWORD"
-- 校验：同目录 verify-role-isolation.sql（断言覆盖率与越权拒绝，失败即非零退出）。
-- 回滚：见文末第 6 节（顺序是"先换回连接串 → 再撤权 → 最后删角色"）。
-- ==============================================================================

\set ON_ERROR_STOP on

-- ------------------------------------------------------------------------------
-- 1. 角色：每个服务一个运行角色（LOGIN）+ 一个结构归属角色（NOLOGIN）。
--    运行角色只拿"能登录"，实例级能力（SUPERUSER / CREATEDB / CREATEROLE /
--    REPLICATION / BYPASSRLS）显式收口：重跑时也会把手工放开的属性收回来。
--    口令只在给了 psql 变量时设置，值不落仓库、不进日志。
-- ------------------------------------------------------------------------------
DO $roles$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT unnest(ARRAY['mf_catalog_owner', 'mf_auth_owner', 'mf_community_owner', 'mf_storage_owner',
                              'mf_audit_owner']) AS name
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r.name) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', r.name);
    END IF;
    EXECUTE format('ALTER ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', r.name);
  END LOOP;

  FOR r IN SELECT unnest(ARRAY['mf_catalog', 'mf_auth', 'mf_community', 'mf_storage']) AS name
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r.name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', r.name);
    END IF;
    EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', r.name);
  END LOOP;
END
$roles$;

\if :{?catalog_password}
ALTER ROLE mf_catalog PASSWORD :'catalog_password';
\endif
\if :{?auth_password}
ALTER ROLE mf_auth PASSWORD :'auth_password';
\endif
\if :{?community_password}
ALTER ROLE mf_community PASSWORD :'community_password';
\endif
\if :{?storage_password}
ALTER ROLE mf_storage PASSWORD :'storage_password';
\endif

-- ------------------------------------------------------------------------------
-- 2. schema 归属：本域 schema 及其**全部对象**归本域 owner 角色所有。
--    存量库（结构由各服务以共用用户建出）走的是"接管"路径：ALTER SCHEMA/表/序列/视图/
--    函数/类型的 OWNER，逐对象收敛到目标归属角色名，因此重跑是空操作。
--    并发写窗口内 ALTER ... OWNER 会取 ACCESS EXCLUSIVE 锁，请放在维护窗口执行。
-- ------------------------------------------------------------------------------
DO $ownership$
DECLARE
  m record;
  obj record;
BEGIN
  FOR m IN
    SELECT * FROM (VALUES
      ('catalog',   'mf_catalog_owner'),
      ('assistant', 'mf_catalog_owner'),
      ('auth',      'mf_auth_owner'),
      ('community', 'mf_community_owner'),
      ('storage',   'mf_storage_owner')
      -- 共享审计 schema 刻意不在这里：它的归属只能由第 3b 节的预建一次钉死。
      -- 事后抢归属会直接把"当前能起来的那个服务"打断（它的启动 DDL 里有 CREATE INDEX IF NOT EXISTS，
      -- 而 PostgreSQL 对 CREATE INDEX 先查表所有权、再看索引是否存在）——实测报
      -- pq: must be owner of table audit_log (42501)，见 docs/architecture/database-roles.md 第 4 节。
    ) AS t(schema_name, owner_role)
  LOOP
    EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I AUTHORIZATION %I', m.schema_name, m.owner_role);
    EXECUTE format('ALTER SCHEMA %I OWNER TO %I', m.schema_name, m.owner_role);

    -- 表 / 序列 / 视图 / 物化视图 / 分区表 / 外部表。
    -- 顺序与过滤都很重要：与表列链接的序列（serial / identity，pg_depend deptype a|i）
    -- 不能单独改 owner（PostgreSQL 报 "cannot change owner of sequence ... is linked to table"），
    -- 它会随所属表的 ALTER TABLE ... OWNER TO 一起走；独立序列排在表之后处理。
    FOR obj IN
      SELECT c.relkind, c.relname
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = m.schema_name
        AND c.relkind IN ('r', 'S', 'v', 'm', 'p', 'f')
        AND pg_get_userbyid(c.relowner) <> m.owner_role
        AND NOT (c.relkind = 'S' AND EXISTS (
          SELECT 1 FROM pg_depend d
          WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
            AND d.refclassid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
        ))
      ORDER BY CASE c.relkind WHEN 'S' THEN 1 ELSE 0 END, c.relname
    LOOP
      EXECUTE format(
        'ALTER %s %I.%I OWNER TO %I',
        CASE obj.relkind
          WHEN 'S' THEN 'SEQUENCE'
          WHEN 'v' THEN 'VIEW'
          WHEN 'm' THEN 'MATERIALIZED VIEW'
          WHEN 'f' THEN 'FOREIGN TABLE'
          ELSE 'TABLE'
        END,
        m.schema_name, obj.relname, m.owner_role);
    END LOOP;

    -- 函数 / 存储过程（catalog.check_parent_cycle() 这类）
    FOR obj IN
      SELECT p.proname, pg_get_function_identity_arguments(p.oid) AS args
      FROM pg_proc p
      JOIN pg_namespace n ON n.oid = p.pronamespace
      WHERE n.nspname = m.schema_name
        AND pg_get_userbyid(p.proowner) <> m.owner_role
    LOOP
      EXECUTE format('ALTER FUNCTION %I.%I(%s) OWNER TO %I', m.schema_name, obj.proname, obj.args, m.owner_role);
    END LOOP;

    -- 枚举与域（业务 schema 里目前没有，留着以防后续迁移引入）
    FOR obj IN
      SELECT t.typname
      FROM pg_type t
      JOIN pg_namespace n ON n.oid = t.typnamespace
      WHERE n.nspname = m.schema_name
        AND t.typtype IN ('e', 'd')
        AND pg_get_userbyid(t.typowner) <> m.owner_role
    LOOP
      EXECUTE format('ALTER TYPE %I.%I OWNER TO %I', m.schema_name, obj.typname, m.owner_role);
    END LOOP;
  END LOOP;
END
$ownership$;

-- ------------------------------------------------------------------------------
-- 3. 运维身份保留 DDL：将四个 owner 角色授予库 owner，供显式迁移与备份恢复使用。
--    库 owner 名字从 pg_database 动态取，不写死；换库名/换 owner 都不用改这里。
-- ------------------------------------------------------------------------------
DO $ops$
DECLARE
  db_owner name;
BEGIN
  SELECT pg_get_userbyid(datdba) INTO db_owner FROM pg_database WHERE datname = current_database();
  IF db_owner IS NOT NULL THEN
    EXECUTE format(
      'GRANT mf_catalog_owner, mf_auth_owner, mf_community_owner, mf_storage_owner TO %I', db_owner);
  END IF;
END
$ops$;

-- ------------------------------------------------------------------------------
-- 3b. 预建共享审计表（bootstrap）——**默认关闭，需显式 -v audit_bootstrap=1**：
--     audit schema 与 audit.audit_log 由本脚本建出、owner = mf_audit_owner。
--
--     为什么需要它（2026-09-19 复核发现的两个真实缺陷）：
--       ① PostgreSQL 的对象 owner **隐式持有全部权限且 REVOKE 不掉**。若让服务先建表，
--          "谁先启动谁成为 owner"——那个运行角色能 UPDATE/DELETE 审计行（留痕可被篡改），
--          而第 4b 节的 REVOKE 对它只是空操作，F 段断言也会在那种实例上失败。
--       ② 授权顺序：表不存在时第 4b 节只能授 schema 权限，漏跑一次会让审计行静默写失败
--          （业务不受影响、留痕整段缺失），而没有任何检查会红。
--
--     当前契约 DDL 已有表存在性守卫，预建不会重复执行 CREATE INDEX。默认关闭是因为
--     预建或调整 audit 归属属于显式 bootstrap 动作；普通重跑只收敛权限，不隐式改 owner。
--     新库或归属不正确的既有库显式开启本段，再执行第 4b 节授权和隔离校验。
--
--     开启方式：psql ... -v audit_bootstrap=1 -f sql/roles-least-privilege.sql
--
--     下面这段 DDL 必须与四个服务各自那份**逐字一致**（契约见 docs/architecture/audit-log.md §1）：
--       backend/migrations/000021_catalog_baseline.up.sql 的 audit-ddl 区间（catalog）
--       ../metafusion-auth/internal/audit/audit.go（auth）
--       ../metafusion-community/migrations/000007_audit_log.up.sql 与 internal/audit/audit.go（community）
--       ../metafusion-storage/internal/store/migrations/000002_audit_log.up.sql 与 internal/audit/audit.go（storage）
--     脚本里这一段是**唯一会被自动化比对的副本**：`python scripts/check_audit_schema.py` 逐条语句
--     比对上面全部来源，漂移即以非零码失败（已接进 CI）。改这里之前先改契约与四份副本。
--     事务：BEGIN 让 pg_advisory_xact_lock 覆盖到建表结束，与同时启动的服务串行化。
-- ------------------------------------------------------------------------------
\if :{?audit_bootstrap}
BEGIN;
-- >>> audit-ddl begin（scripts/check_audit_schema.py 比对这个区间里的语句）
DO $audit_ddl$
BEGIN
  PERFORM pg_advisory_xact_lock(740205);
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
-- <<< audit-ddl end
-- 守卫块之后把归属交给 mf_audit_owner（非契约语句，幂等）。
-- 为什么不提前 SET LOCAL ROLE：守卫块里含 CREATE SCHEMA IF NOT EXISTS，而 PostgreSQL 对该语句
-- **即使 schema 已存在也要求库级 CREATE**，该角色没有（见 docs/architecture/database-roles.md §4.1）。
ALTER SCHEMA audit OWNER TO mf_audit_owner;
ALTER TABLE audit.audit_log OWNER TO mf_audit_owner;   -- 主键与四条索引随表一起走
DO $audit_notice$
BEGIN
  RAISE NOTICE '[3b] 已预建共享审计表（守卫形态）并钉死 owner=mf_audit_owner';
END
$audit_notice$;
COMMIT;
\else
\echo '[3b] 跳过预建（未给 -v audit_bootstrap=1）：只做第 4b 节的按服务授权，不动 audit 归属。'
\echo '     当前 DDL 已有存在性守卫；新库或归属未固定的实例须显式 bootstrap，再验收只追加权限。'
\endif

-- ------------------------------------------------------------------------------
-- 4. 授权：本域给全（运行期 CRUD + 序列 + 函数 + 默认权限），别的域一律不给。
--    - 本域 CRUD 现在由 owner 成员身份覆盖，是 Tier 2 的落点（见第 6 节），现在就授出，
--      免得降级时才发现清单缺表。
--    - ALTER DEFAULT PRIVILEGES 按 owner 角色登记：将来迁移新建的表/序列自动带上同样的授权。
--    - 其它 schema 显式 REVOKE：新角色本来就没权限，写出来是为了让"授权范围"一眼可见，
--      并把历史手工授过的残留权限收回来（REVOKE 对无权限对象是空操作，幂等）。
-- ------------------------------------------------------------------------------
DO $grants$
DECLARE
  g record;
  other record;
  db_owner name;
BEGIN
  SELECT pg_get_userbyid(datdba) INTO db_owner FROM pg_database WHERE datname = current_database();
  FOR g IN
    SELECT * FROM (VALUES
      ('mf_catalog',   'mf_catalog_owner',   'catalog'),
      ('mf_catalog',   'mf_catalog_owner',   'assistant'),
      ('mf_auth',      'mf_auth_owner',      'auth'),
      ('mf_community', 'mf_community_owner', 'community'),
      ('mf_storage',   'mf_storage_owner',   'storage')
    ) AS t(app_role, owner_role, schema_name)
  LOOP
    -- 连库 + 本域 schema 使用权
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), g.app_role);
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', g.schema_name, g.app_role);

    -- 本域现有对象的运行期权限
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %I TO %I', g.schema_name, g.app_role);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', g.schema_name, g.app_role);
    EXECUTE format('GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %I TO %I', g.schema_name, g.app_role);

    -- 将来由本域 owner 建出的对象
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', g.owner_role, g.schema_name, g.app_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT USAGE, SELECT ON SEQUENCES TO %I', g.owner_role, g.schema_name, g.app_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT EXECUTE ON FUNCTIONS TO %I', g.owner_role, g.schema_name, g.app_role);
    -- 同一套默认权限也要登记在**库 owner**（运维/迁移身份，deploy.sh 的 POSTGRES_USER）名下：
    -- 目录的迁移工具（backend/cmd/migrate）只读 DB_*，跑迁移时用的就是库 owner，
    -- 它新建的表归库 owner 所有，不登记这一份的话新表对运行角色是零权限（实测：000002 建出的
    -- 表在重跑本脚本之前，mf_catalog 连 SELECT 都没有）。
    IF db_owner IS NOT NULL THEN
      EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', db_owner, g.schema_name, g.app_role);
      EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT USAGE, SELECT ON SEQUENCES TO %I', db_owner, g.schema_name, g.app_role);
      EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA %I GRANT EXECUTE ON FUNCTIONS TO %I', db_owner, g.schema_name, g.app_role);
    END IF;

    -- Tier 1：启动路径的 DDL 需要本域 owner 成员身份 + 库级 CREATE（理由见文件头）
    EXECUTE format('GRANT %I TO %I', g.owner_role, g.app_role);
    EXECUTE format('GRANT CREATE ON DATABASE %I TO %I', current_database(), g.app_role);

    -- 别的业务 schema：一条都不给（含 schema 使用权）
    FOR other IN
      SELECT unnest(ARRAY['catalog', 'assistant', 'auth', 'community', 'storage']) AS schema_name
    LOOP
      -- assistant 是同一目录进程的模块，catalog 两个 schema 共享一个运行身份。
      -- 不能按当前行单个 schema 撤权，否则处理 assistant 时会撤回 catalog 的授权。
      CONTINUE WHEN other.schema_name = g.schema_name
        OR (g.app_role = 'mf_catalog' AND other.schema_name IN ('catalog', 'assistant'));
      IF EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = other.schema_name) THEN
        EXECUTE format('REVOKE ALL ON SCHEMA %I FROM %I', other.schema_name, g.app_role);
        EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM %I', other.schema_name, g.app_role);
        EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM %I', other.schema_name, g.app_role);
      END IF;
    END LOOP;
  END LOOP;
END
$grants$;

-- ------------------------------------------------------------------------------
-- 4b. 共享 audit schema 的按服务授权（跨域例外）
--     audit.audit_log 由第 3b 节预建、owner = mf_audit_owner；四个运行角色只拿「追加」权限：
--       USAGE + CREATE ON SCHEMA audit —— 启动路径要执行 CREATE SCHEMA/TABLE IF NOT EXISTS，
--                                         而 PostgreSQL 先查权限再看对象是否存在（见文件头）
--       SELECT + INSERT ON audit.audit_log —— 写审计与排障读
--     刻意**不授** UPDATE / DELETE / TRUNCATE：审计行不可改不可删。表归 mf_audit_owner，
--     因此这三条 REVOKE 对四个运行角色是**真的生效**的（而不是对 owner 的空操作）——
--     这正是第 3b 节预建的意义。本段最后还会把 owner 交回 mf_audit_owner 并报 NOTICE：
--     owner 决定最终权限，留着「看起来授了权、其实 owner 说了算」才是真正的隐患。
-- ------------------------------------------------------------------------------
DO $audit$
DECLARE
  r record;
  table_owner name;
BEGIN
  -- schema 都不存在 = 审计功能还没部署（默认路径没有预建，服务也还没启动过）：空操作。
  IF NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'audit') THEN
    RAISE NOTICE '[4b] 未发现 audit schema：审计功能尚未部署，跳过共享审计授权（第 3b 节预建或服务首次启动会建它）';
    RETURN;
  END IF;

  -- 表还不存在 = 审计功能还没建过表（默认路径没有预建，服务也还没启动过）：只授 schema 权限。
  -- 这不是错误，但**必须重跑本脚本**才能补上表权限，否则服务起来后审计行会静默写失败——
  -- verify-role-isolation.sql 的 F 段会以"缺 SELECT/INSERT"报错兜住这一点。
  IF to_regclass('audit.audit_log') IS NULL THEN
    RAISE NOTICE '[4b] audit.audit_log 尚不存在：只授 audit schema 权限；表建出来后重跑本脚本补齐表权限（F 段断言会盯着）';
    FOR r IN SELECT unnest(ARRAY['mf_catalog', 'mf_auth', 'mf_community', 'mf_storage']) AS app_role
    LOOP
      EXECUTE format('GRANT USAGE, CREATE ON SCHEMA audit TO %I', r.app_role);
    END LOOP;
    RETURN;
  END IF;

  SELECT pg_get_userbyid(c.relowner) INTO table_owner
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'audit' AND c.relname = 'audit_log';
  IF table_owner IS DISTINCT FROM 'mf_audit_owner' THEN
    -- 刻意不在这里抢归属：默认路径执行的是"收敛权限"，不该顺手改所有权——
    -- 运维漏看一次就变更了对象归属，是不可接受的副作用。
    -- 但也不能只留给 F 段去红：这里就把"怎么改"一步一步写出来（可执行的前置检查）。
    RAISE NOTICE '';
    RAISE NOTICE '┌──────────────────────────────────────────────────────────────────────────────';
    RAISE NOTICE '│ [4b] 前置检查未通过：audit.audit_log 的 owner 是 %（期望 mf_audit_owner）', table_owner;
    RAISE NOTICE '│ 危害：owner 隐式持有全部权限且 REVOKE 不掉，该角色能 UPDATE/DELETE 审计行 —— "只追加"不成立；';
    RAISE NOTICE '│       verify-role-isolation.sql 的 F 段会因此硬失败（这是刻意的）。';
    RAISE NOTICE '│ 原因：表是某个服务先启动时建的（四个服务账本互相独立，新库上谁先起谁建）。';
    RAISE NOTICE '│ 修法（在部署机上执行，幂等，可在服务运行中执行）：';
    RAISE NOTICE '│   docker compose --env-file .env -f deploy/docker-compose.yml exec -T postgres \';
    RAISE NOTICE '│     psql -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -v audit_bootstrap=1 -f - \';
    RAISE NOTICE '│     < deploy/sql/roles-least-privilege.sql';
    RAISE NOTICE '│ 预期输出：[3b] 已预建共享审计表（守卫形态）并钉死 owner=mf_audit_owner，';
    RAISE NOTICE '│           且此后 verify-role-isolation.sql 的 F 段从红转绿；服务无需重启';
    RAISE NOTICE '│           （契约 DDL 有存在性守卫，归属变化不再让服务启动失败）。';
    RAISE NOTICE '│ 说明：本步是显式动作，不由默认路径代劳——默认路径只收敛权限。';
    RAISE NOTICE '└──────────────────────────────────────────────────────────────────────────────';
    RAISE NOTICE '';
  END IF;

  FOR r IN SELECT unnest(ARRAY['mf_catalog', 'mf_auth', 'mf_community', 'mf_storage']) AS app_role
  LOOP
    EXECUTE format('GRANT USAGE, CREATE ON SCHEMA audit TO %I', r.app_role);
    EXECUTE format('GRANT SELECT, INSERT ON audit.audit_log TO %I', r.app_role);
    EXECUTE format('REVOKE UPDATE, DELETE, TRUNCATE ON audit.audit_log FROM %I', r.app_role);
  END LOOP;
END
$audit$;

-- public schema：默认谁都不需要它——四个服务的业务对象都在自己的 schema 里。
-- 例外只有一处、且属于**运维身份**而不是服务：目录迁移工具的账本表 public.schema_migrations
-- （backend/internal/migrator 用的是不带 schema 的 schema_migrations，按 search_path 落在 public）。
-- 它由库 owner 身份读写，所以这里对四个运行角色保持零权限；一旦把迁移工具也切到服务角色
-- （需要给 backend/internal/config 加 DATABASE_URL 支持，属代码改动），要同时补的权限见第 6.4 节。
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM mf_catalog, mf_auth, mf_community, mf_storage;
DO $public$
BEGIN
  EXECUTE format('REVOKE CREATE ON DATABASE %I FROM PUBLIC', current_database());
END
$public$;

-- ------------------------------------------------------------------------------
-- 5. 授权现状自述（只读；判据断言在同目录 verify-role-isolation.sql）
-- ------------------------------------------------------------------------------
SELECT
  a.rolname AS app_role,
  ARRAY(
    SELECT n.nspname
    FROM pg_namespace n
    WHERE n.nspname IN ('catalog', 'assistant', 'auth', 'community', 'storage', 'audit')
      AND has_schema_privilege(a.rolname, n.nspname, 'USAGE')
    ORDER BY n.nspname
  ) AS usable_schemas,
  a.rolsuper AS is_superuser, a.rolcreatedb AS can_create_db, a.rolcreaterole AS can_create_role,
  ARRAY(
    SELECT m.rolname FROM pg_auth_members am
    JOIN pg_roles m ON m.oid = am.roleid
    WHERE am.member = a.oid ORDER BY m.rolname
  ) AS member_of
FROM pg_roles a
WHERE a.rolname IN ('mf_catalog', 'mf_auth', 'mf_community', 'mf_storage')
ORDER BY a.rolname;

-- ==============================================================================
-- 6. 回滚 / 降级
-- ==============================================================================
-- 6.1 Tier 2 降级（把运行角色切成纯 CRUD，前提是"启动只校验、迁移由 owner 单独跑"已落地；
--     每一步都可单独执行，第 4 节的 CRUD 与默认权限已经在那，不需要补授权）：
--   REVOKE CREATE ON DATABASE <db> FROM mf_catalog;
--   REVOKE mf_catalog_owner FROM mf_catalog;      -- 关键一步：去掉本域 DDL
--   -- 结构变更改由 owner 身份执行：
--   --   psql "postgres://mf_catalog_owner@.../<db>" -c '...'
--   -- 校验：verify-role-isolation.sql 会同时断言"本域 CRUD 齐全"与"本域 DDL 已收回"。
--
-- 6.2 完全回滚（回到四服务共用 metafusion 的旧状态；先让服务换回原连接串并确认健康，
--     顺序不能反——反了会让在跑的服务当场 42501）：
--   -- ① 在各 *_DATABASE_URL 中显式配置目标回退身份，重建并确认服务已换连接串。
--      四个运行 DSN 仍必填；不能用 DB_* 回退，也不能通过置空 DSN 切换身份。
--   -- ② 撤权
--   REVOKE ALL ON ALL TABLES IN SCHEMA catalog FROM mf_catalog;
--   REVOKE ALL ON ALL SEQUENCES IN SCHEMA catalog FROM mf_catalog;
--   REVOKE USAGE, CREATE ON SCHEMA catalog FROM mf_catalog;
--   REVOKE CREATE ON DATABASE <db> FROM mf_catalog;
--   REVOKE mf_catalog_owner FROM mf_catalog;
--   ALTER DEFAULT PRIVILEGES FOR ROLE mf_catalog_owner IN SCHEMA catalog
--     REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM mf_catalog;
--   ALTER DEFAULT PRIVILEGES FOR ROLE mf_catalog_owner IN SCHEMA catalog
--     REVOKE USAGE, SELECT ON SEQUENCES FROM mf_catalog;
--   -- 对 assistant 重复本节 catalog schema 的显式撤权；二者属于同一运行身份。
--   -- ③ 对象归属交回库 owner（否则回滚后的 metafusion 建不了/改不了表）
--   --   REASSIGN OWNED BY mf_catalog_owner TO <db_owner>;   -- 注意：会接管 owner 角色的全部对象，
--   --   本脚本的 owner 角色只拥有本域对象，因此这一条是安全的；不确定时按 schema 逐对象 ALTER OWNER。
--   -- ④ 删角色
--   DROP OWNED BY mf_catalog;   -- 清掉该角色持有的对象与授权（本模型里它不建对象，通常是空操作）
--   DROP OWNED BY mf_catalog_owner;
--   DROP ROLE mf_catalog;
--   DROP ROLE mf_catalog_owner;
--   -- 其它三个服务同样处理（schema/角色名成对替换即可）。
--
-- 6.3 重授权（误撤权限后恢复）：直接重跑本文件即可——全部语句幂等，会把权限收敛回目标状态。
--     迁移新增了表/序列之后也建议重跑一次：默认权限只覆盖登记之后、由登记角色建出的对象。
--
-- 6.4 把目录迁移工具也切到服务角色（可选，需代码改动：backend/internal/config 不读 DATABASE_URL）：
--   GRANT USAGE, CREATE ON SCHEMA public TO mf_catalog;                      -- 账本表所在 schema
--   GRANT SELECT, INSERT, UPDATE, DELETE ON public.schema_migrations TO mf_catalog;
--   然后按第 6.1 节把 mf_catalog 降级为纯 CRUD。
--   实测（本机真库）：只给业务 schema 权限时 mf-migrate status 报
--   pq: permission denied for schema public (42501)——账本表在 public，就是这条。
-- ==============================================================================
