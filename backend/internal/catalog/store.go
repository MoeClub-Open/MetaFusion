package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	auditlog "github.com/metafusion/metafusion-app/internal/audit"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/migrations"
)

// 领域哨兵错误：respond（http.go）按错误链判定 HTTP 状态码，所以写路径与
// retirement/生命周期里的拒绝必须用同一批哨兵，而不是各自 fmt.Errorf 出同名字符串——
// 字符串比较在 %w 包裹后就失效（403/409 会退化成 400）。
// 哨兵文本即响应里的机器码（forbidden / version_conflict），与前端字典逐字对齐。
var (
	errForbidden       = errors.New("forbidden")
	errVersionConflict = errors.New("version_conflict")
	// errInvalidState 是"当前状态不允许这个动作"的稳定码（删除或合并不接受终态条目）。
	// 它不需要 respond 特判——默认分支就是 400 + 错误文本；登记成哨兵是为了让调用方与
	// 测试能 errors.Is 判定，而不是比较字符串（%w 包裹后字符串比较会失效）。
	errInvalidState = errors.New("invalid_state")
)

type Store struct {
	DB *sql.DB
	// OpenSearch 提供文本匹配、排序和分页；数据库负责当页可见性与权威 DTO 回读。
	OpenSearch *OpenSearchClient
	// Verifier 是账号服务令牌的验签器（只有公钥）。为 nil 或未配置密钥时，
	// 需要身份的接口按匿名处理——目录不再有"查库兜底"这条路径。
	Verifier *TokenVerifier
	// PAT 是个人访问令牌（mfp_ 前缀）的内省器，见 pat.go。为 nil（未配置 AUTH_URL）时，
	// 带 mfp_ 前缀的请求一律 503 auth_unavailable：身份只能问账号服务，目录不查它的表。
	PAT *PATIntrospector
	// Audit 是审计留痕写入器（internal/audit，见 audit.go 的接线）。为 nil 时写路由不写审计行
	// （单测里大量夹具只有 Store{DB:…}，不该为了"没接审计"而让请求变形）。
	Audit *auditlog.Recorder
	// defStatus 记录最近一次启动期定义种子合并的结果（见 definitions.go 的 DefinitionStatus）。
	// 由服务侧透出在 /health：这一项要回答的是"站点可用，但定义是不是没更新"。
	defStatusMu sync.Mutex
	defStatus   DefinitionStatus
}
type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	maxOpen := 64
	if value := os.Getenv("CATALOG_DB_MAX_OPEN_CONNS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 256 {
			db.Close()
			return nil, fmt.Errorf("invalid_catalog_db_max_open_conns")
		}
		maxOpen = parsed
	}
	maxIdle := min(32, maxOpen)
	if value := os.Getenv("CATALOG_DB_MAX_IDLE_CONNS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > maxOpen {
			db.Close()
			return nil, fmt.Errorf("invalid_catalog_db_max_idle_conns")
		}
		maxIdle = parsed
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(time.Hour)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// Authenticate 只做无状态 RS256 验签：目录侧不再回退查 auth.sessions / auth.oauth_tokens。
// 那两张表归账号服务所有，跨系统读对方的表会让"各司其职"名存实亡，因此这里连 ctx 都不需要。
// 验签器缺失或验签失败一律按匿名处理（fail closed），不会静默放行。
func (s *Store) Authenticate(token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" || s.Verifier == nil {
		return nil, sql.ErrNoRows
	}
	claims, err := s.Verifier.Verify(token)
	if err != nil {
		return nil, err
	}
	return ClaimsToUser(claims), nil
}

// Initialize 是本地安装与测试的显式组合入口（S1）：基线 + 结构增量 + 空库内容种子 +
// 种子增量合并，一次到达可服务状态。生产常驻进程不再调用它，改走 CheckSchemaContract
// （只读，见 startup.go 的职责映射）；生产的结构/种子/体检分别由 mf-migrate up、
// mf-migrate seed、mf-migrate check-refs 承担。Existing catalog data are untouched.
// 其写入职责与 mf-migrate up 分工：migrate 只负责结构迁移（backend/migrations），
// 定义/货架/外部库三类"内容种子"只在这里逐行 ON CONFLICT DO NOTHING 补齐
// （第一方 OAuth 客户端随账号拆分归账号服务），
// 因种子会随版本新增条目（如货架新增 slug），不属于一次性结构迁移。
// 任一轨道先执行都安全：种子用 ON CONFLICT 保护已有行（后台自定义不被覆盖）。
//
// 定义种子语义（空库全量播种、存量**只增不改**）：catalog.definition_config 非空时
// 不覆盖既有文档——后台改过的关系/词表/字段（禁用某关系码、entry_role 降级）全部保留；
// 播种之后再由 EnsureSeedDefinitions 做一次增量合并，只补种子里新增而当前缺失的键，
// 这样新版本新增的关系码/字段能到达存量实例，又不会覆盖任何人工决定。
// 见 TestDefinitionsSeedOnlyWhenEmpty 与 TestMergeSeedDefinitionsIsAdditiveOnly。
func (s *Store) Initialize(ctx context.Context) error {
	// 本地安装与显式迁移命令共用同一个带版本记账的迁移器；
	// 结构必须先于内容种子，存量库须通过基线账本验证。
	if err := migrator.New(s.DB, migrations.FS).Up(ctx); err != nil {
		return err
	}
	return s.SeedContent(ctx)
}

// write is for independent configuration and preferences. Catalog facts and
// definitions use writeCatalog so cross-row invariants participate in SSI.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// definitionsLockKey 是定义配置写入协调的 advisory 键（M02）：保存取独占，
// 定义敏感写（实体/关系/合并改写）取共享。旧定义写穿影响检查即被串行化：
// 配置保存等待在途写完成，在途写等待保存完成。
// 与 migrator 的 88481001、审计契约的 740205 都不同键。
//
// releaseLockClass 是发行粒度的 advisory 类键（M01）：Release.subjects 与
// TrackContent 按 Release 互斥（删 subject 与加收录不再各看各的旧快照），
// 不同发行互不阻塞，不恢复全库写锁。键 = (740204, hashtext(release_id))。
//
// 加锁顺序：身份 term（commit 创建）→ 定义共享 → 发行 → 实体行，集合稳定排序。
// 跨行一致性由 SERIALIZABLE 校验；这里只协调定义发布与发行热点，不锁全库结构。
const definitionsLockKey = 740203
const releaseLockClass = 740204

func lockDefinitionsShared(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared($1::bigint)", definitionsLockKey)
	return err
}

func lockDefinitionsExclusive(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", definitionsLockKey)
	return err
}

func lockRelease(ctx context.Context, tx *sql.Tx, releaseID string) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1::int, hashtext($2))", releaseLockClass, releaseID)
	return err
}

// lockReleaseScope 取本次实体写入所属发行的 M01 锁：release 改 subjects、
// track 改 contents，同一发行的两类写互斥；不同发行并行。
// 新建 release（ID 未分配、无人可达）与归属缺失（后继报 parent_required/
// invalid_reference）不取锁，其余一律按所属发行取。调用方保证在定义共享锁之后调用。
func lockReleaseScope(ctx context.Context, tx *sql.Tx, e Entity) error {
	var releaseID string
	switch e.Kind {
	case "release":
		releaseID = e.ID
	case "medium":
		releaseID = e.ReleaseID
	case "track":
		if e.MediumID == "" {
			return nil
		}
		if err := tx.QueryRowContext(ctx, "SELECT release_id FROM catalog.mediums WHERE id=$1", e.MediumID).Scan(&releaseID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("invalid_reference")
			}
			return err
		}
	default:
		return nil
	}
	if releaseID == "" {
		return nil
	}
	return lockRelease(ctx, tx, releaseID)
}

// newID 生成时间有序的 UUIDv7 作为主键。
//
// 随机 UUIDv4 做主键时插入点散落整棵 B-tree：亿级下页分裂、索引膨胀与缓存命中都会明显变差，
// 且按时间范围扫描（导出/同步/分区裁剪）没有局部性。v7 前缀是毫秒时间戳，插入集中在最右侧。
// 代价是 ID 泄漏创建时间（毫秒级，与 created_at 同级信息）；生成失败退回 v4，不返回空 ID。
func newID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func definitions(ctx context.Context, q queryer) (DefinitionConfig, error) {
	var v DefinitionConfig
	var b []byte
	err := q.QueryRowContext(ctx, "SELECT etag,document,updated_at FROM catalog.definition_config WHERE singleton=true").Scan(&v.ETag, &b, &v.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(b, &v.Document)
	}
	return v, err
}

// definitionsCache caches the single live document by etag. Transactional
// reads bypass it; every load checks the current etag to observe other replicas.
var (
	definitionsCacheMu sync.Mutex
	definitionsCache   DefinitionConfig
	definitionsCacheDB *sql.DB
	definitionsCacheOK bool
)

func (s *Store) Definitions(ctx context.Context) (DefinitionConfig, error) {
	// 空 DB（纯映射单测的 Store{}）直接回退读库，保持原有错误语义。
	if s.DB == nil {
		return definitions(ctx, s.DB)
	}
	definitionsCacheMu.Lock()
	cached, cachedDB, ok := definitionsCache, definitionsCacheDB, definitionsCacheOK
	definitionsCacheMu.Unlock()
	// 缓存按 *sql.DB 区分：测试夹具为每个用例建隔离库（同 id/base_version
	// 但不同 document），跨库复用会串定义。生产单库进程内则命中同一指针。
	if ok && cachedDB == s.DB {
		// 轻量失效检查：只读 id/base_version，不反序列化整份 document。
		var etag string
		if err := s.DB.QueryRowContext(ctx, "SELECT etag FROM catalog.definition_config WHERE singleton=true").Scan(&etag); err == nil && etag == cached.ETag {
			return cached, nil
		}
	}
	v, err := definitions(ctx, s.DB)
	if err != nil {
		return v, err
	}
	definitionsCacheMu.Lock()
	definitionsCache, definitionsCacheDB, definitionsCacheOK = v, s.DB, true
	definitionsCacheMu.Unlock()
	return v, err
}

// InvalidateDefinitionsCache 清空进程内定义缓存，供测试在改动定义后强制刷新。
// 生产路径靠版本号失效，不需要调用。
func InvalidateDefinitionsCache() {
	definitionsCacheMu.Lock()
	definitionsCache, definitionsCacheDB, definitionsCacheOK = DefinitionConfig{}, nil, false
	definitionsCacheMu.Unlock()
}
func get(ctx context.Context, q queryer, id string) (Entity, error) {
	var e Entity
	var b []byte
	var state, createdBy string
	err := q.QueryRowContext(ctx, "SELECT document,state,created_by::text FROM catalog.entities WHERE id=$1", id).Scan(&b, &state, &createdBy)
	if err != nil {
		return e, err
	}
	if err = json.Unmarshal(b, &e); err != nil {
		return e, err
	}
	// Visibility and ownership use the same authoritative columns as batch
	// reads. A stale embedded snapshot must not expose a deleted entity or
	// grant its previous owner access after ownership has changed.
	e.State, e.CreatedBy = state, createdBy
	switch e.Kind {
	case "content_unit":
		err = q.QueryRowContext(ctx, "SELECT work_id,coalesce(parent_id::text,'') FROM catalog.content_units WHERE id=$1", id).Scan(&e.WorkID, &e.ParentID)
	case "expression":
		err = q.QueryRowContext(ctx, "SELECT work_id,coalesce(content_unit_id::text,'') FROM catalog.expressions WHERE id=$1", id).Scan(&e.WorkID, &e.ContentUnitID)
	case "medium":
		err = q.QueryRowContext(ctx, "SELECT release_id,coalesce(parent_id::text,'') FROM catalog.mediums WHERE id=$1", id).Scan(&e.ReleaseID, &e.ParentID)
	case "track":
		err = q.QueryRowContext(ctx, "SELECT medium_id,coalesce(parent_id::text,'') FROM catalog.tracks WHERE id=$1", id).Scan(&e.MediumID, &e.ParentID)
		if err != nil {
			return e, err
		}
		var rows *sql.Rows
		rows, err = q.QueryContext(ctx, "SELECT expression_id,position,locator,attributes,sources FROM catalog.track_contents WHERE track_id=$1 ORDER BY position", id)
		if err != nil {
			return e, err
		}
		defer rows.Close()
		e.Contents = []Inclusion{}
		for rows.Next() {
			var c Inclusion
			var loc, attrs, sources []byte
			if err = rows.Scan(&c.ExpressionID, &c.Position, &loc, &attrs, &sources); err != nil {
				return e, err
			}
			if err = json.Unmarshal(sources, &c.Sources); err != nil {
				return e, err
			}
			if len(loc) > 0 {
				if err = json.Unmarshal(loc, &c.Locator); err != nil {
					return e, err
				}
			}
			if len(attrs) > 0 {
				if err = json.Unmarshal(attrs, &c.Attributes); err != nil {
					return e, err
				}
			}
			e.Contents = append(e.Contents, c)
		}
		err = rows.Err()
	case "release":
		var rows *sql.Rows
		rows, err = q.QueryContext(ctx, "SELECT work_id,role,position,attributes FROM catalog.release_subjects WHERE release_id=$1 ORDER BY position,work_id", id)
		if err != nil {
			return e, err
		}
		defer rows.Close()
		e.Subjects = []Subject{}
		for rows.Next() {
			var x Subject
			var attrs []byte
			if err = rows.Scan(&x.WorkID, &x.Role, &x.Position, &attrs); err != nil {
				return e, err
			}
			if len(attrs) > 0 {
				if err = json.Unmarshal(attrs, &x.Attributes); err != nil {
					return e, err
				}
			}
			e.Subjects = append(e.Subjects, x)
		}
		err = rows.Err()
	}
	return e, err
}
func visible(e Entity, u *User) bool {
	// 正常条目公开可见；终态条目仅供创建者与生命周期管理者查阅。
	return e.State == "active" || u != nil && (u.Can(PermissionLifecycleManage) || ownedBy(e, *u))
}

// GetManyVisible 一次查询批量取实体，仅返回 u 可见者。
// 用于关系对端解析：真实条目署名可达数百条，逐条 Get 会形成 N+1 且易被固定上限截断。
func (s *Store) GetManyVisible(ctx context.Context, ids []string, u *User) (map[string]Entity, error) {
	out := map[string]Entity{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id::text, document, state, created_by::text FROM catalog.entities WHERE id = ANY($1::uuid[])", pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state, createdBy string
		var b []byte
		if err = rows.Scan(&id, &b, &state, &createdBy); err != nil {
			return nil, err
		}
		var e Entity
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		// 可见性以库列为准（document 里的 state 可能滞后）。
		e.ID, e.State, e.CreatedBy = id, state, createdBy
		if visible(e, u) {
			out[id] = e
		}
	}
	return out, rows.Err()
}
func (s *Store) Get(ctx context.Context, id string, u *User) (Entity, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Entity{}, fmt.Errorf("invalid_id")
	}
	e, err := get(ctx, s.DB, id)
	if err == nil && !visible(e, u) {
		return Entity{}, sql.ErrNoRows
	}
	if err != nil {
		return e, err
	}
	return visibleEntityContents(ctx, s.DB, e, u)
}

// reference 是写侧身份归一：merged/deleted 行的引用一律拒绝（invalid_reference），
// 调用方先经 ResolveIdentity/identity 端点拿到 canonical 再写——归一靠"拒绝+指路"，
// 不在写路径里静默改写目标（静默改写会让调用方记错自己引的是谁）。
func reference(ctx context.Context, q queryer, u *User) func(string, []string) error {
	return func(id string, kinds []string) error {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return fmt.Errorf("invalid_reference")
		}
		e, err := get(ctx, q, id)
		if err != nil || !contains(kinds, e.Kind) || !visible(e, u) || e.State == "deleted" || e.State == "merged" {
			return fmt.Errorf("invalid_reference")
		}
		return nil
	}
}

// audit keeps entity and relation revisions. Definition updates only pass
// aggregate counts as snapshot, so this table is not a hidden definition archive.
func audit(ctx context.Context, tx *sql.Tx, id string, version int64, u User, note string, sources []Source, snapshot any, eventType string) error {
	// actor_name 与 actor_id 一起落库：读取修订历史不再需要 JOIN auth.users
	// （账号表归账号服务，跨 schema 读会让两个系统在数据层重新耦合）。
	if _, err := tx.ExecContext(ctx, "INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7)", id, version, u.ID, u.Username, note, encode(sources), encode(snapshot)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO catalog.outbox(id,type,entity_id,version,payload) VALUES($1,$2,$3,$4,$5)", uuid.NewString(), eventType, id, version, encode(snapshot))
	return err
}
func (s *Store) Save(ctx context.Context, input Edit, u User) (Entity, error) {
	if !u.CanBypassReview() {
		return Entity{}, fmt.Errorf("commit_required")
	}
	var e Entity
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var err error
		e, err = s.saveEntityTx(ctx, tx, input, u)
		return err
	})
	return e, err
}

// saveEntityTx shares validation, facts, revisions, outbox and notifications.
// The caller owns transaction boundaries and scoped lock order.
func (s *Store) saveEntityTx(ctx context.Context, tx *sql.Tx, input Edit, u User) (Entity, error) {
	if u.FromPAT && !u.Can(PermissionEntityEdit) && !u.Can(PermissionLifecycleManage) {
		return Entity{}, errForbidden
	}
	// Validation and evidence inheritance may mutate nested maps/slices. Every
	// attempt owns a fresh copy; retries cannot change the request or prior state.
	var e Entity
	raw, err := json.Marshal(input.Entity)
	if err != nil {
		return e, err
	}
	if err = json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if err = validateSources(input.EditNote, input.Sources); err != nil {
		return e, err
	}
	if !input.identityLocksHeld {
		if err = lockEntityIdentityWrite(ctx, tx, e, u); err != nil {
			return e, err
		}
	}
	err = func() error {
		// M02：定义共享 → 发行，见 definitionsLockKey。
		if err := lockDefinitionsShared(ctx, tx); err != nil {
			return err
		}
		// M01 再按所属发行取锁（新建 release/归属缺失跳过，见 lockReleaseScope）。
		if err := lockReleaseScope(ctx, tx, e); err != nil {
			return err
		}
		var v DefinitionConfig
		var err error
		if input.definitions != nil {
			v = *input.definitions
		} else {
			v, err = definitions(ctx, tx)
		}
		if err != nil {
			return err
		}
		var old Entity
		// create 决定了这一步是"无版本可比的纯插入"还是"必须带版本条件的更新"。
		create := e.ID == ""
		if create {
			if input.ExpectedVersion != 0 {
				return errVersionConflict
			}
			e.ID = input.createID
			if e.ID == "" {
				e.ID = newID()
			}
			e.Version = 1
			e.CreatedBy = u.ID
			// R1 幂等声明与业务写入同一事务：已存在响应即重放返回（不写业务），
			// 否则占位并继续，提交前回填首创响应（见 audit 调用点之后）。
			if input.idempotency != nil {
				prior, claimed, cerr := claimIdempotencyTx(ctx, tx, input.idempotency)
				if cerr != nil {
					return cerr
				}
				if !claimed {
					if e, cerr = replayEntity(prior); cerr != nil {
						return cerr
					}
					return errIdempotentReplay
				}
			}
		} else {
			old, err = get(ctx, tx, e.ID)
			if err != nil {
				return err
			}
			if !visible(old, &u) || old.State == "deleted" || old.State == "merged" {
				return errForbidden
			}
			if old.Version != input.ExpectedVersion {
				return errVersionConflict
			}
			if input.contentPatch != nil {
				e, err = applyTrackContentPatch(ctx, tx, old, input.contentPatch, u)
				if err != nil {
					return err
				}
				if err = lockReleaseScope(ctx, tx, e); err != nil {
					return err
				}
			}
			if old.Kind != e.Kind || old.WorkID != e.WorkID || old.ReleaseID != e.ReleaseID || old.MediumID != e.MediumID {
				return fmt.Errorf("immutable_scope")
			}
			e.CreatedBy = old.CreatedBy
			e.Version = old.Version + 1
		}
		if err = prepareEntityWriteState(old, &e, u); err != nil {
			return err
		}
		if !input.preserveContents {
			assignInclusionSources(&e, input.Sources)
		}
		if err = preserveHiddenTrackContents(ctx, tx, old, e, u); err != nil {
			return err
		}
		e.Title = strings.TrimSpace(e.Title)
		e.UpdatedAt = time.Now().UTC()
		ref, err := entityWriteReference(ctx, tx, e, u)
		if err != nil {
			return err
		}
		historical := !create
		mediumFormat := ""
		if e.Kind == "track" && e.MediumID != "" {
			medium, lookupErr := get(ctx, tx, e.MediumID)
			if lookupErr != nil || medium.Kind != "medium" {
				return fmt.Errorf("invalid_reference")
			}
			mediumFormat, _ = medium.Attributes["format"].(string)
		}
		toValidate := e
		if input.contentPatch != nil || input.preserveContents {
			// Preserve opaque, unchanged records in storage without revalidating
			// their visibility or returning them to the editor.
			toValidate, err = visibleEntityContents(ctx, tx, e, &u)
			if err != nil {
				return err
			}
		}
		if err = v.Document.validateEntity(toValidate, ref, historical, mediumFormat); err != nil {
			return err
		}
		if err = validateMediumSchemeChange(ctx, tx, v.Document, old, e); err != nil {
			return err
		}
		// ExternalLinks 两层复核：格式层 + external_databases 预设层。
		if err = v.Document.validateExternalLinks(e); err != nil {
			return err
		}
		// 旧导入幂等键仅可原值保留或删除，不再允许任何写入者新声明或改写。
		if err = guardImportKey(e.ExternalLinks, old.ExternalLinks); err != nil {
			return err
		}
		if err = validateExternalLinksAgainstDB(ctx, tx, e); err != nil {
			return err
		}
		if err = v.Document.retiredEntity(e, old); err != nil {
			return err
		}
		if err = validateEntityStructuralReferences(e, ref); err != nil {
			return err
		}
		if err = validateChangedEntitySources(e, old); err != nil {
			return err
		}
		if input.identityReview && entityNeedsIdentityReview(old, e) {
			if err = reviewEntityIdentity(ctx, tx, e, input.ReviewedCandidateIDs, u, nil, 0); err != nil {
				return err
			}
		}
		if e.ExternalLinks == nil {
			e.ExternalLinks = []ExternalLink{}
		}
		stored := e
		stored.WorkID = ""
		stored.ContentUnitID = ""
		stored.ReleaseID = ""
		stored.MediumID = ""
		stored.ParentID = ""
		stored.Contents = nil
		stored.Subjects = nil
		// 乐观并发的"版本检查 + 写入"必须是**一次原子操作**：原先是"先读版本、再无条件写"，
		// READ COMMITTED 下两个并发写各自读到同一版本、都通过检查，后写覆盖先写 = 丢更新。
		// 版本条件进 WHERE 后，后到者先在行锁上排队，锁释放时重算条件、版本已变 → 0 行 →
		// version_conflict，整个事务（含侧表与审计行）一起回滚。
		if create {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, e.ID, e.Kind, e.Version, e.Title, e.State, e.CreatedBy, encode(stored), e.UpdatedAt); err != nil {
				return err
			}
		} else {
			var res sql.Result
			if res, err = tx.ExecContext(ctx, `UPDATE catalog.entities SET version=$3,title=$4,state=$5,document=$6,updated_at=$7 WHERE id=$1 AND version=$2`, e.ID, input.ExpectedVersion, e.Version, e.Title, e.State, encode(stored), e.UpdatedAt); err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errVersionConflict
			}
		}
		switch e.Kind {
		case "content_unit":
			_, err = tx.ExecContext(ctx, "INSERT INTO catalog.content_units(id,work_id,parent_id) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET parent_id=EXCLUDED.parent_id", e.ID, e.WorkID, nullable(e.ParentID))
		case "expression":
			_, err = tx.ExecContext(ctx, "INSERT INTO catalog.expressions(id,work_id,content_unit_id) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET content_unit_id=EXCLUDED.content_unit_id", e.ID, e.WorkID, nullable(e.ContentUnitID))
		case "medium":
			_, err = tx.ExecContext(ctx, "INSERT INTO catalog.mediums(id,release_id,parent_id) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET parent_id=EXCLUDED.parent_id", e.ID, e.ReleaseID, nullable(e.ParentID))
		case "track":
			_, err = tx.ExecContext(ctx, "INSERT INTO catalog.tracks(id,medium_id,parent_id) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET parent_id=EXCLUDED.parent_id", e.ID, e.MediumID, nullable(e.ParentID))
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM catalog.track_contents WHERE track_id=$1", e.ID); err != nil {
				return err
			}
			for _, c := range e.Contents {
				if _, err = tx.ExecContext(ctx, "INSERT INTO catalog.track_contents(track_id,expression_id,position,locator,attributes,sources) VALUES($1,$2,$3,$4,$5,$6)", e.ID, c.ExpressionID, c.Position, encode(c.Locator), encode(c.Attributes), encode(inclusionSources(c.Sources))); err != nil {
					return err
				}
			}
		case "release":
			if _, err = tx.ExecContext(ctx, "DELETE FROM catalog.release_subjects WHERE release_id=$1", e.ID); err != nil {
				return err
			}
			for _, x := range e.Subjects {
				if _, err = tx.ExecContext(ctx, "INSERT INTO catalog.release_subjects(release_id,work_id,role,position,attributes) VALUES($1,$2,$3,$4,$5)", e.ID, x.WorkID, x.Role, x.Position, encode(x.Attributes)); err != nil {
					return err
				}
			}
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog.external_links WHERE entity_id=$1`, e.ID); err != nil {
			return err
		}
		for _, link := range e.ExternalLinks {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog.external_links(entity_id,entity_kind,kind,url,external_id,role,origin,source_refs) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8)`,
				e.ID, e.Kind, link.Kind, link.URL, link.ExternalID, link.Role, link.Origin, encode(link.SourceRefs)); err != nil {
				return err
			}
		}
		missing, err := undeclaredReleaseSubject(ctx, tx, e)
		if err != nil {
			return err
		}
		if missing {
			return fmt.Errorf("undeclared_release_subject")
		}
		if err := audit(ctx, tx, e.ID, e.Version, u, input.EditNote, input.Sources, e, "entity.saved"); err != nil {
			return err
		}
		// R1 首创响应与业务写入同一事务回填：崩溃只会整体回滚，重试按已存在行重放。
		if create && input.idempotency != nil {
			if err := setIdempotencyResponseTx(ctx, tx, input.idempotency, e); err != nil {
				return err
			}
		}
		// 通知与实体写入**同一事务**：审核结果与收录事件不会出现"改了却没通知"的半成品。
		// create 时 old 是零值，用 nil 表示"没有旧版本"（零值 Entity 的 State 也是空串，
		// 明确区分新建事件与已有条目的资料修订）。
		var before *Entity
		if !create {
			prior := old
			before = &prior
		}
		return notifySaveOutcome(ctx, tx, before, e, u)
	}()
	if errors.Is(err, errIdempotentReplay) {
		return e, nil
	}
	return e, err
}

// undeclaredReleaseSubject 只复核本次写入可能打破的收录归属：release 重写
// subjects、track 重写 contents，均只涉及其所在发行；medium 改动不触及映射表，
// 限定其发行复核；其余 kind 不触及映射表且归属字段在 Save 内不可变
// （immutable_scope），跳过复核。语义与原全表 EXISTS 一致——任一发行存在
// 未声明收录即拒绝，只是不再为一次单实体写入扫描全库。
func undeclaredReleaseSubject(ctx context.Context, tx *sql.Tx, e Entity) (bool, error) {
	var releaseID, trackID any
	switch e.Kind {
	case "release":
		releaseID = e.ID
	case "track":
		trackID = e.ID
	case "medium":
		releaseID = e.ReleaseID
	default:
		return false, nil
	}
	var missing bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
	 SELECT 1 FROM catalog.track_contents c
	 JOIN catalog.tracks t ON t.id=c.track_id
	 JOIN catalog.mediums m ON m.id=t.medium_id
	 JOIN catalog.expressions x ON x.id=c.expression_id
	 WHERE ($1::uuid IS NULL OR m.release_id=$1)
	 AND ($2::uuid IS NULL OR t.id=$2)
	 AND NOT EXISTS(SELECT 1 FROM catalog.release_subjects s
	 WHERE s.release_id=m.release_id AND s.work_id=x.work_id))`, releaseID, trackID).Scan(&missing)
	return missing, err
}

type ListOptions struct {
	Kind, Query, State, WorkID, ContentUnitID, ReleaseID, MediumID, ParentID, Field, Value string
	// SearchIDs 是 OpenSearch 提供的候选 ID 顺序。它仅由 HTTP 列表处理器内部填写，
	// 最终查询仍用数据库可见性和筛选谓词收口，不作为外部参数暴露。
	SearchIDs []string
	// Kinds 命中任一结构层级。
	Kinds []string
	// Tags 按 TagsMode（any / all，默认 any）过滤 attributes.tags，走 jsonb 容器包含，
	// 由 entities_attribute_tags 函数索引支撑，避免全表扫描。
	Tags     []string
	TagsMode string
	// OriginalLanguage 按 document->>'original_language' 精确匹配：八层级通用列，
	// 探索页侧栏的原语言筛选即它（值如 ja/zh/en，大小写按入库原样比）。
	OriginalLanguage string
	// HasPictures 只留有封面的：document->'pictures' 为非空数组。CASE 保序
	// （WHEN 为真才求 array_length）：缺键/标量/对象走 ELSE=0 按无封面过滤。
	HasPictures bool
	// Sort / Order 是白名单排序键与方向（见 listSortKeys），Locale 只在 Sort=title
	// 时参与"取哪个语种的题名"；未知键由 normalizeListSort 拒掉（HTTP 400 invalid_sort），
	// 不静默退回默认序——静默忽略正是"传了 sort=title 却拿到 updated_at 序列"的成因。
	Sort, Order, Locale string
	Offset, Limit       int
}

// listFilter builds the shared WHERE clause for List and Count so the list
// total is a real COUNT(*) over the same predicate set, not len(items).
// listFilterNestedKey 供纯 SQL 构造（如单测）时在 ctx 中显式携带 Definitions
// 快照：生产路径仍读已发布版本；携带快照时跳过 DB 查询，避免离线断言依赖数据库。
type listFilterNestedKey struct{}

// withListFilterDefinitions 把 Definitions 快照注入 ctx，供 listFilter 解析
// field 点分路径时使用（同包测试用）。
func withListFilterDefinitions(ctx context.Context, d Definitions) context.Context {
	return context.WithValue(ctx, listFilterNestedKey{}, d)
}

// nestedQuoteKey 把已解析的字段码内联为 SQL 字面量：字段码受 codePattern
// 约束，此处再转义单引号兜底；用户取值一律走绑定参数。
func nestedQuoteKey(k string) string { return "'" + strings.ReplaceAll(k, "'", "''") + "'" }

// compileNestedPath 把点分路径编译成谓词：group 节点用 -> 逐层下钻，
// list 中途节点用 jsonb_array_elements 的 EXISTS 实现"任一元素命中"，
// 叶子一律按现有等值语义做 ->> 文本比较。base 为叶子父级的 JSON 表达式，
// lookup 为首段的查表域（实体属性表或某结构组的子字段表），seed 为链路上
// 已确认的祖先字段（含入口本身）。
func compileNestedPath(base string, lookup map[string]Field, seed []Field, path []string, value string, args *[]any) (string, error) {
	parent := base
	node := Field{}
	chain := append([]Field{}, seed...)
	froms := []string{}
	for i, sg := range path {
		var child Field
		if i == 0 {
			var ok bool
			child, ok = lookup[sg]
			if !ok {
				return "", fmt.Errorf("unknown_field")
			}
		} else {
			if node.Type != "group" {
				return "", fmt.Errorf("unknown_field")
			}
			var ok bool
			child, ok = node.Fields[sg]
			if !ok {
				return "", fmt.Errorf("unknown_field")
			}
		}
		node = child
		chain = append(chain, child)
		if i < len(path)-1 {
			if node.Type != "group" && node.Type != "list" {
				return "", fmt.Errorf("unknown_field")
			}
			parent += "->" + nestedQuoteKey(sg)
			for node.Type == "list" {
				if node.Items == nil {
					return "", fmt.Errorf("unknown_field")
				}
				alias := fmt.Sprintf("elem%d", len(froms)+1)
				froms = append(froms, "jsonb_array_elements("+parent+") AS "+alias)
				parent = alias
				node = *node.Items
				chain = append(chain, node)
			}
		}
	}
	for _, f := range chain {
		if !f.Enabled {
			return "", fmt.Errorf("field_not_searchable")
		}
	}
	if !node.Searchable {
		return "", fmt.Errorf("field_not_searchable")
	}
	*args = append(*args, value)
	cond := fmt.Sprintf("%s->>%s=$%d", parent, nestedQuoteKey(path[len(path)-1]), len(*args))
	if len(froms) == 0 {
		return cond, nil
	}
	return "EXISTS(SELECT 1 FROM " + strings.Join(froms, ", ") + " WHERE " + cond + ")", nil
}

// nestedFieldPredicate 解析 field 点分路径并编译成 WHERE 谓词：
// 结构属性伪字段 locator./inclusion_attributes./subject_attributes. 编译成
// catalog.track_contents / catalog.release_subjects 的 EXISTS 子查询，
// kind 显式不匹配时谓词恒假（返回空集）而不是报错；其余路径命中实体
// document->'attributes'，group 逐层下钻、list 中途节点按任一元素命中。
func nestedFieldPredicate(doc Definitions, o ListOptions, args *[]any) (string, error) {
	segs := strings.Split(o.Field, ".")
	for _, sg := range segs {
		if sg == "" || !codePattern.MatchString(sg) {
			return "", fmt.Errorf("unknown_field")
		}
	}
	switch segs[0] {
	case "locator", "inclusion_attributes", "subject_attributes":
		root, ok := doc.Fields[segs[0]]
		if !ok || root.Type != "group" {
			return "", fmt.Errorf("unknown_field")
		}
		want := "track"
		table, corr, base := "catalog.track_contents tc", "tc.track_id = catalog.entities.id", "tc.locator"
		if segs[0] == "subject_attributes" {
			want = "release"
			table, corr, base = "catalog.release_subjects rs", "rs.release_id = catalog.entities.id", "rs.attributes"
		} else if segs[0] == "inclusion_attributes" {
			base = "tc.attributes"
		}
		cond, err := compileNestedPath(base, root.Fields, []Field{root}, segs[1:], o.Value, args)
		if err != nil {
			return "", err
		}
		if (o.Kind != "" && o.Kind != want) || (len(o.Kinds) > 0 && !contains(o.Kinds, want)) {
			// kind 不匹配时恒假：已绑定的取值参数不再需要，弹出以保持
			// 参数位置与谓词一一对应。
			*args = (*args)[:len(*args)-1]
			return "1=0", nil
		}
		return "EXISTS(SELECT 1 FROM " + table + " WHERE " + corr + " AND (" + cond + "))", nil
	default:
		return compileNestedPath("document->'attributes'", doc.Fields, nil, segs, o.Value, args)
	}
}

func listFilter(ctx context.Context, s *Store, o ListOptions, u *User, args *[]any) ([]string, error) {
	parts := []string{"state NOT IN ('deleted','merged')"}
	add := func(clause string, value any) {
		*args = append(*args, value)
		parts = append(parts, fmt.Sprintf(clause, len(*args)))
	}
	if u == nil {
		parts = append(parts, "state='active'")
	} else if !u.Can(PermissionLifecycleManage) {
		// 当前仅有 active 与已排除的终态；使用同一主体可见性口径。
		add("(state='active' OR created_by=$%d)", u.ID)
	}
	if o.Kind != "" {
		add("kind=$%d", o.Kind)
	}
	if len(o.Kinds) > 0 {
		add("kind = ANY($%d)", pq.Array(o.Kinds))
	}
	if o.State != "" {
		add("state=$%d", o.State)
	}
	if o.OriginalLanguage != "" {
		add("document->>'original_language'=$%d", o.OriginalLanguage)
	}
	if o.HasPictures {
		parts = append(parts, "(CASE WHEN jsonb_typeof(document->'pictures')='array' THEN jsonb_array_length(document->'pictures') ELSE 0 END>0)")
	}
	if o.Query != "" {
		return nil, fmt.Errorf("text_query_requires_search")
	}
	if len(o.SearchIDs) > 0 {
		*args = append(*args, pq.Array(o.SearchIDs))
		parts = append(parts, fmt.Sprintf("id = ANY($%d::uuid[])", len(*args)))
	}
	if o.WorkID != "" {
		add("(id IN(SELECT id FROM catalog.content_units WHERE work_id=$%[1]d) OR id IN(SELECT id FROM catalog.expressions WHERE work_id=$%[1]d) OR id IN(SELECT release_id FROM catalog.release_subjects WHERE work_id=$%[1]d))", o.WorkID)
	}
	if o.ReleaseID != "" {
		add("id IN(SELECT id FROM catalog.mediums WHERE release_id=$%d)", o.ReleaseID)
	}
	if o.ContentUnitID != "" {
		add("id IN(SELECT id FROM catalog.expressions WHERE content_unit_id=$%d)", o.ContentUnitID)
	}
	if o.MediumID != "" {
		add("id IN(SELECT id FROM catalog.tracks WHERE medium_id=$%d)", o.MediumID)
	}
	if o.ParentID != "" {
		add("id IN(SELECT id FROM catalog.content_units WHERE parent_id=$%[1]d UNION ALL SELECT id FROM catalog.mediums WHERE parent_id=$%[1]d UNION ALL SELECT id FROM catalog.tracks WHERE parent_id=$%[1]d)", o.ParentID)
	}
	if o.Field != "" {
		// definitions 快照默认读已发布版本；纯 SQL 构造（如单测）可经 ctx
		// 显式携带快照（withListFilterDefinitions），避免离线断言依赖数据库。
		var doc Definitions
		if dd, ok := ctx.Value(listFilterNestedKey{}).(Definitions); ok && dd.Fields != nil {
			doc = dd
		} else {
			v, err := s.Definitions(ctx)
			if err != nil {
				return nil, err
			}
			doc = v.Document
		}
		if !strings.Contains(o.Field, ".") {
			f, ok := doc.Fields[o.Field]
			if !ok || !f.Searchable {
				return nil, fmt.Errorf("field_not_searchable")
			}
			*args = append(*args, o.Field, o.Value)
			parts = append(parts, fmt.Sprintf("document->'attributes'->>$%d=$%d", len(*args)-1, len(*args)))
		} else {
			cond, ferr := nestedFieldPredicate(doc, o, args)
			if ferr != nil {
				return nil, ferr
			}
			parts = append(parts, cond)
		}
	}
	if len(o.Tags) > 0 {
		// 用容器包含（@>）而非展开比较，以命中
		// entities_attribute_tags 函数 GIN 索引。
		ors := make([]string, 0, len(o.Tags))
		for _, tag := range o.Tags {
			if strings.TrimSpace(tag) == "" {
				continue
			}
			*args = append(*args, encode([]string{tag}))
			ors = append(ors, fmt.Sprintf("document->'attributes'->'tags' @> $%d::jsonb", len(*args)))
		}
		if len(ors) > 0 {
			join := " OR "
			if o.TagsMode == "all" {
				join = " AND "
			}
			parts = append(parts, "("+strings.Join(ors, join)+")")
		}
	}
	return parts, nil
}

// Count returns the real total for ListOptions over the same predicates List
// uses. List endpoints use it instead of len(items) so pagination totals stay
// exact as the dataset grows.
func (s *Store) Count(ctx context.Context, o ListOptions, u *User) (int64, error) {
	args := []any{}
	parts, err := listFilter(ctx, s, o, u, &args)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.entities WHERE "+strings.Join(parts, " AND "), args...).Scan(&n)
	return n, err
}

// entityListSortKeys 是实体列表排序键白名单，白名单之外在 HTTP 层被拒（400 invalid_sort）。
// "最新创建"排在 id 上：entities 表没有 created_at 列（基线迁移是唯一结构来源，不为一个
// 排序键改结构），而主键由 uuid.NewV7() 生成、按时间有序，id 序即创建时间序且能走主键索引
// ——与货架排序 shelfOrderClause 的 created 取值同一口径。
var entityListSortKeys = map[string]bool{"updated_at": true, "created_at": true, "title": true}

// sortLocaleOK 只接受形如 zh-CN / ja / en-US 的语种码。它只当绑定参数用，
// 形态不合法的取值被丢弃（排序退到原文语种/en-US/基础题名），不因此拒绝整次列表请求。
func sortLocaleOK(s string) bool {
	if len(s) < 2 || len(s) > 20 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			continue
		case (r == '-' || r == '_') && i > 0:
			continue
		case r >= '0' && r <= '9' && i > 0:
			continue
		default:
			return false
		}
	}
	return true
}

// normalizeListSort 校验并归一化排序参数：未知排序键/方向返回稳定码（HTTP 层映射 400），
// 不静默退回默认序——静默忽略正是"传了 sort=title 却拿到 updated_at 序列"的成因。
func normalizeListSort(o *ListOptions) error {
	o.Sort = strings.ToLower(strings.TrimSpace(o.Sort))
	o.Order = strings.ToLower(strings.TrimSpace(o.Order))
	if o.Sort != "" && !entityListSortKeys[o.Sort] {
		return fmt.Errorf("invalid_sort")
	}
	if o.Order != "" && o.Order != "asc" && o.Order != "desc" {
		return fmt.Errorf("invalid_order")
	}
	o.Locale = strings.TrimSpace(o.Locale)
	if !sortLocaleOK(o.Locale) {
		o.Locale = ""
	}
	return nil
}

// entityListOrderClause 生成实体列表的 ORDER BY：排序键与方向都经白名单映射成 SQL 片段，
// 用户输入**不进 SQL 文本**（title 排序只把请求语种作为绑定参数 $n 带进去）。
// 末位一律补 id，同一排序键下的并列值靠它定序，分页不会抖动。
func entityListOrderClause(o ListOptions, args *[]any) (string, error) {
	structural := o.ReleaseID != "" || o.MediumID != "" || o.ParentID != "" || o.ContentUnitID != ""
	// 方向缺省按排序键的直觉取值：时间类"最新在前"，题名类 A→Z；显式 order 覆盖它。
	dir := "DESC"
	if o.Sort == "title" {
		dir = "ASC"
	}
	if o.Order == "asc" {
		dir = "ASC"
	} else if o.Order == "desc" {
		dir = "DESC"
	}
	switch o.Sort {
	case "":
		if structural {
			// 结构子项按 position 排序：Go 落库恒为数字（types.go Position int），
			// 正则守卫只防直接 SQL 写入的脏串（PG 无 TRY_CAST，裸 ::int 会报 22P02
			// 导致整页 500；脏串按 0 排而不中断列表）。
			return "CASE WHEN document->>'position' ~ '^-?[0-9]+$' THEN (document->>'position')::int ELSE 0 END, updated_at DESC, id", nil
		}
		if len(o.SearchIDs) > 0 {
			*args = append(*args, pq.Array(o.SearchIDs))
			return fmt.Sprintf("array_position($%d::uuid[], id), id", len(*args)), nil
		}
		return "updated_at DESC, id", nil
	case "updated_at":
		return "updated_at " + dir + ", id", nil
	case "created_at":
		return "id " + dir, nil
	case "title":
		// 多语言题名：请求语种 → 原文语种 → en-US → 基础题名，与前端 lib/titles.ts
		// 的选取链同序（首位由调用方按界面语言传入）。只按基础题名排会让日文/中文界面
		// 看到"顺序与显示的题名无关"，这里让排序键与页面显示的题名是同一个值。
		*args = append(*args, o.Locale)
		return fmt.Sprintf("COALESCE(NULLIF(document->'translations'->$%d::text->>'title',''), NULLIF(document->'translations'->(document->>'original_language')->>'title',''), NULLIF(document->'translations'->'en-US'->>'title',''), title) %s, id", len(*args), dir), nil
	}
	return "", fmt.Errorf("invalid_sort")
}

func (s *Store) List(ctx context.Context, o ListOptions, u *User) ([]Entity, error) {
	args := []any{}
	parts, err := listFilter(ctx, s, o, u, &args)
	if err != nil {
		return nil, err
	}
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 50
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
	orderClause, err := entityListOrderClause(o, &args)
	if err != nil {
		return nil, err
	}
	args = append(args, o.Limit, o.Offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM catalog.entities WHERE "+strings.Join(parts, " AND ")+fmt.Sprintf(" ORDER BY "+orderClause+" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// 批量取回：先查 ID 再逐个 Get 的 N+1 改为一次 getMany（IN + 按 kind 批量
	// 补侧表），顺序按 ids 回填。getMany 只返回可见者：IDs 与返回的差集是
	// "状态翻转/不可见"的并发竞态，直接跳过（不整页失败）。
	// 分页总数仍由 Count 的真实 COUNT(*) 保证，不用 len(items) 代替。
	if len(ids) == 0 {
		return []Entity{}, nil
	}
	got, err := s.getMany(ctx, ids, u)
	if err != nil {
		return nil, err
	}
	items := make([]Entity, 0, len(ids))
	for _, id := range ids {
		if e, ok := got[id]; ok {
			items = append(items, e)
		}
	}
	return items, nil
}

// Revisions 返回某目标的修订历史。目标可以是实体，也可以是关系：
// 关系修订的 target_id 就是关系 ID 本身（见 audit / SaveRelation），
// 关系在 entities 表没有对应行，因此不能沿用实体的可见性判定。
func (s *Store) Revisions(ctx context.Context, id string, u *User) ([]map[string]any, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("invalid_id")
	}
	// 实体修订逐行按实体可见性过滤；关系修订的可见性改由其两端实体验证，
	// 所以需要区分本次查询的目标类型。
	entityScoped := true
	if _, err := s.Get(ctx, id, u); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		r, rerr := relationByID(ctx, s.DB, id)
		if rerr != nil {
			return nil, rerr
		}
		src, serr := get(ctx, s.DB, r.SourceID)
		if serr != nil {
			return nil, serr
		}
		tgt, terr := get(ctx, s.DB, r.TargetID)
		if terr != nil {
			return nil, terr
		}
		if !visible(src, u) || !visible(tgt, u) ||
			src.State == "deleted" || src.State == "merged" || tgt.State == "deleted" || tgt.State == "merged" {
			return nil, sql.ErrNoRows
		}
		entityScoped = false
	}
	// 身份取自修订行里的快照列，**不 JOIN auth.users**：账号表归账号服务，目录侧读它
	// 就等于把两个系统的数据层重新绑在一起（也挡住了将来换库/换实例的可能）。
	// 老库迁移过来的存量行可能没有快照，回退为 system/editor，只影响显示名。
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id, r.version, COALESCE(r.actor_id::text, ''), COALESCE(NULLIF(r.actor_name, ''), 'system'), r.edit_note, r.sources, r.snapshot, r.created_at, COALESCE(r.commit_id::text,'')
		FROM catalog.revisions r
		WHERE r.target_id = $1
		ORDER BY r.version DESC, r.id DESC
		LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	historicalTracks := map[string]Entity{}
	for rows.Next() {
		var revID, version int64
		var actorID, actorName, note, commitID string
		var sources, snapshot json.RawMessage
		var at time.Time
		if err = rows.Scan(&revID, &version, &actorID, &actorName, &note, &sources, &snapshot, &at, &commitID); err != nil {
			return nil, err
		}
		// 实体修订的 snapshot 是 Entity，逐行按实体可见性过滤；
		// 关系修订的 snapshot 是 Relation，授权已在查询前按两端实体完成，不再套实体判定。
		if entityScoped {
			var historical Entity
			if err = json.Unmarshal(snapshot, &historical); err != nil {
				return nil, err
			}

			if !visible(historical, u) {
				continue
			}
			if historical.Kind == "track" && len(historical.Contents) > 0 {
				// Each revision needs its own key: all snapshots have the same
				// entity ID, while their inclusion records can differ.
				historicalTracks[fmt.Sprint(len(out))] = historical
			}
		}
		out = append(out, map[string]any{
			"id":         revID,
			"version":    version,
			"actor_id":   actorID,
			"actor_name": actorName,
			"edit_note":  note,
			"sources":    sources,
			"snapshot":   snapshot,
			"created_at": at,
		})
		if commitID != "" {
			out[len(out)-1]["commit_id"] = commitID
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// Current expression visibility also governs historical read projections.
	// One batch query handles all visible revisions; stored snapshots are untouched.
	if err = filterVisibleTrackContents(ctx, s.DB, historicalTracks, u); err != nil {
		return nil, err
	}
	for i, revision := range out {
		historical, ok := historicalTracks[fmt.Sprint(i)]
		if !ok {
			continue
		}
		// Preserve unrelated snapshot fields verbatim as JSON values;
		// only the structural contents projection is replaced.
		var document map[string]json.RawMessage
		if err = json.Unmarshal(revision["snapshot"].(json.RawMessage), &document); err != nil {
			return nil, err
		}
		if document["contents"], err = json.Marshal(historical.Contents); err != nil {
			return nil, err
		}
		filtered, marshalErr := json.Marshal(document)
		if marshalErr != nil {
			return nil, marshalErr
		}
		revision["snapshot"] = json.RawMessage(filtered)
	}
	return out, nil
}

// ListAll is used for bounded entity directories, which must not silently truncate at 100.
func (s *Store) ListAll(ctx context.Context, o ListOptions, u *User) ([]Entity, error) {
	out := []Entity{}
	o.Limit = 100
	for {
		items, err := s.List(ctx, o, u)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(items) < o.Limit {
			return out, nil
		}
		o.Offset += o.Limit
	}
}
