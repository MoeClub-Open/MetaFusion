package catalog

// 站内通知（收件箱）。表结构与设计取舍见 docs/architecture/catalog-core-implementation.md，
// 完整报告见 docs-local/report-f1-notifications/REPORT.md。
//
// 落点：目录服务 —— 五类事件里四个产生端在目录（审核结果、收录、导入完成），
// 前端 /api 的主系统也是目录，而 catalog.user_preferences 已确立"目录库存按人裸 UUID 数据"的先例。
// 跨服务写入只剩一条："评论被回复"由互动服务按 POST /api/notifications/internal 投递
// （共享密钥 + 终端用户令牌），出站走互动服务自己的 internal/upstream。
//
// 三条不可动摇的语义：
//   1. 聚合：同一 (收件人, dedupe_key) 只有一行，count 累加、read_at 归零、updated_at 刷新。
//      "同一帖多条回复"因此是一行 + count，而不是一次刷屏。
//   2. 已读：read_at IS NULL = 未读；标记已读的 SQL 里带 recipient_id 条件。
//   3. 越权：列表/未读/标记已读一律以令牌身份为收件人，**不接受**调用方传 recipient_id；
//      对别人的通知标记已读与对不存在的 id 一样回 404 not_found（不泄露存在性）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 通知类型枚举（对外稳定码，与前端四语字典的键一一对应；只增不改）。
const (
	NotificationCommentReplied = "comment.replied"
	NotificationEntityIncluded = "entity.included"
	NotificationReviewApproved = "entity.review_approved"
	NotificationReviewRejected = "entity.review_rejected"
)

// 列表分页口径：缺省 20、上限 100（越界静默收敛，与 /catalog/entities 风格一致）。
const (
	defaultNotificationLimit = 20
	maxNotificationLimit     = 100
)

// notificationTypes 的有序清单也是对外契约的一部分（NotificationTypes 用它）。
var (
	notificationTypeOrder = []string{
		NotificationCommentReplied, NotificationEntityIncluded, NotificationReviewApproved,
		NotificationReviewRejected,
	}
	notificationTypes = map[string]bool{
		NotificationCommentReplied: true,
		NotificationEntityIncluded: true,
		NotificationReviewApproved: true,
		NotificationReviewRejected: true,
	}
)

// NotificationTypes 返回全部可用类型码（供 openapi 描述与测试断言）。
func NotificationTypes() []string { return append([]string{}, notificationTypeOrder...) }

func validNotificationType(t string) bool { return notificationTypes[t] }

// Notification 是对外形状。dedupe_key 与 recipient_id 刻意不在其中：
// 前者是服务端的合并口径（不是展示字段），后者就是调用者本人。
type Notification struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	ActorID     string         `json:"actor_id"`
	ActorName   string         `json:"actor_name"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	Payload     map[string]any `json:"payload"`
	// Count 是这一行合并了多少条同键事件（>=1）。
	Count int  `json:"count"`
	Read  bool `json:"read"`
	// CreatedAt 是行创建时间；UpdatedAt 是合进行的最大事件时间，列表按它倒序（即按事件时间，不按抵达顺序）。
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NotificationInput 是服务端产生端填的内容。RecipientID 为空即丢弃（调用方已判定"没人要收"）。
type NotificationInput struct {
	RecipientID string
	Type        string
	ActorID     string
	ActorName   string
	SubjectType string
	SubjectID   string
	Payload     map[string]any
	// DedupeKey 为空时按 "type:subject_id" 生成：同类型的同一条落点合并成一行。
	DedupeKey string
	// EventID 是这条事件的稳定身份（跨服务投递传被回复的回复 id 之类），跨重试保持稳定
	// （互动分支保证）。收据键即 (RecipientID, EventID)：重复事件整体不更新聚合行。
	// 为空则每次生成新 id（本服务内的事件，不去重）。
	EventID string
	// EventTime 是事件发生时间（展示排序依据）：零值即现在。迟到的旧事件只累加计数，
	// 不把聚合行顶到收件箱顶部（见 notificationUpsert 的 GREATEST）。
	EventTime time.Time
}

func (in NotificationInput) dedupe() string {
	if k := strings.TrimSpace(in.DedupeKey); k != "" {
		return k
	}
	return in.Type + ":" + in.SubjectID
}

// notificationWriter 让同一段写入既能进调用方的事务（实体/关系写），也能直接走 DB（内部投递端点，见 notify 的同事务接线）。
type notificationWriter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// 收据 insert 与下面的聚合 upsert 是写入的全部两条语句（见 notifyTx）：先收据去重，再聚合。
// 合并时 read_at 归零（有新活动就要重新被看见）、count 累加、其余字段取最新事件。
const notificationReceiptInsert = `
INSERT INTO catalog.notification_receipts(recipient_id,event_id)
VALUES ($1,$2)
ON CONFLICT DO NOTHING`

// notificationUpsert 是聚合行的唯一写入语句：调用前收据已落定（见 notifyTx），
// 到这里的都是新事件，无条件累加计数、归零未读、刷新展示字段。
// updated_at 取事件时间的最大值：迟到的旧事件只累加计数，不把聚合行顶到顶部——
// 展示按事件时间，不按抵达顺序。新行的 updated_at 即首条事件时间。
const notificationUpsert = `
INSERT INTO catalog.notifications
  (id,recipient_id,type,actor_id,actor_name,subject_type,subject_id,payload,dedupe_key,last_event_id,count,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,now(),$11)
ON CONFLICT (recipient_id,dedupe_key) DO UPDATE SET
  type = EXCLUDED.type,
  actor_id = EXCLUDED.actor_id,
  actor_name = EXCLUDED.actor_name,
  subject_type = EXCLUDED.subject_type,
  subject_id = EXCLUDED.subject_id,
  payload = EXCLUDED.payload,
  count = catalog.notifications.count + 1,
  read_at = NULL,
  updated_at = GREATEST(catalog.notifications.updated_at, EXCLUDED.updated_at),
  last_event_id = EXCLUDED.last_event_id`

// notify 写/合并一条通知。类型是代码常量，写错了要失败得响一点（进测试眼），
// 因此非法类型返回错误而不是静默丢弃。
// 收据与聚合更新同事务：调用方传 *sql.Tx 即进调用方事务（实体/关系写），
// 传 *sql.DB 则在这里开一次事务（跨服务投递端点与导入回执）。
func notify(ctx context.Context, q notificationWriter, in NotificationInput) error {
	if tx, ok := q.(*sql.Tx); ok {
		return notifyTx(ctx, tx, in)
	}
	db, ok := q.(*sql.DB)
	if !ok {
		return fmt.Errorf("invalid_notification_writer")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := notifyTx(ctx, tx, in); err != nil {
		return err
	}
	return tx.Commit()
}

// notifyTx 先插 (recipient_id, event_id) 收据、再做聚合更新，两步同事务。
// 收据已存在即重复事件（上游重试/并发同事件/响应丢失重投）：整体不更新聚合行，
// 计数、未读与展示字段都不动。A→B→A 交错里第二个 A 是新事件（新收据），照常计数——
// 只比 last_event_id 的旧口径会把它误判成重试而丢掉。
func notifyTx(ctx context.Context, tx *sql.Tx, in NotificationInput) error {
	if strings.TrimSpace(in.RecipientID) == "" {
		return nil
	}
	if !validNotificationType(in.Type) {
		return fmt.Errorf("invalid_notification_type: %s", in.Type)
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	event := strings.TrimSpace(in.EventID)
	if event == "" {
		event = uuid.NewString()
	}
	eventTime := in.EventTime
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}
	res, err := tx.ExecContext(ctx, notificationReceiptInsert, in.RecipientID, event)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, notificationUpsert,
		uuid.NewString(), in.RecipientID, in.Type, nullable(in.ActorID), in.ActorName,
		in.SubjectType, in.SubjectID, encode(payload), in.dedupe(), event, eventTime)
	return err
}

const notificationColumns = `id::text, type, COALESCE(actor_id::text,''), actor_name, subject_type, subject_id,
       payload, count, (read_at IS NOT NULL) AS read, created_at, updated_at`

// ListNotifications 是收件箱列表：按事件时间倒序（见 notificationUpsert），返回 (条目, 总数, 未读数)。
// 未读数与列表在同一次调用里返回是有意的：列表页两个数要一致，分两次请求会出现
// "角标 3、列表全已读"的中间窗口。
func (s *Store) ListNotifications(ctx context.Context, recipientID string, limit, offset int) ([]Notification, int, int, error) {
	if limit <= 0 || limit > maxNotificationLimit {
		limit = defaultNotificationLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT "+notificationColumns+" FROM catalog.notifications WHERE recipient_id=$1 ORDER BY updated_at DESC, id DESC LIMIT $2 OFFSET $3",
		recipientID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		var n Notification
		var read bool
		var raw []byte
		if err := rows.Scan(&n.ID, &n.Type, &n.ActorID, &n.ActorName, &n.SubjectType, &n.SubjectID,
			&raw, &n.Count, &read, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, 0, 0, err
		}
		n.Read = read
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &n.Payload); err != nil {
				// payload 是本服务写进去的 JSON 对象，读不回来是服务端故障，
				// 不能折成空 payload 让调用方以为"这条通知没内容"。
				return nil, 0, 0, fmt.Errorf("invalid_notification_payload: %w", err)
			}
		}
		if n.Payload == nil {
			n.Payload = map[string]any{}
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.notifications WHERE recipient_id=$1", recipientID).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	unread, err := s.NotificationUnreadCount(ctx, recipientID)
	if err != nil {
		return nil, 0, 0, err
	}
	return items, total, unread, nil
}

// NotificationUnreadCount 是角标端点唯一要的一次聚合：走 notifications_unread 部分索引，
// 不取行、不 JOIN、不解析 payload。
func (s *Store) NotificationUnreadCount(ctx context.Context, recipientID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		"SELECT count(*) FROM catalog.notifications WHERE recipient_id=$1 AND read_at IS NULL", recipientID).Scan(&n)
	return n, err
}

// MarkNotificationRead 标记单条已读（已读时间戳保留首次值），返回标记后的未读数。
// 收件人条件进 WHERE：别人的通知与不存在的 id 都 0 行 → sql.ErrNoRows，HTTP 层统一回 404 not_found。
func (s *Store) MarkNotificationRead(ctx context.Context, recipientID, id string) (int, error) {
	if _, err := uuid.Parse(id); err != nil {
		return 0, sql.ErrNoRows
	}
	res, err := s.DB.ExecContext(ctx,
		"UPDATE catalog.notifications SET read_at=COALESCE(read_at, now()) WHERE id=$1 AND recipient_id=$2", id, recipientID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	return s.NotificationUnreadCount(ctx, recipientID)
}

// MarkAllNotificationsRead 标记全部未读，返回本次标记的条数与之后的未读数
// （后者仍回读一次：返回的 0 应当是库里的 0，不是"我假设它是 0"）。
func (s *Store) MarkAllNotificationsRead(ctx context.Context, recipientID string) (int64, int, error) {
	res, err := s.DB.ExecContext(ctx,
		"UPDATE catalog.notifications SET read_at=now() WHERE recipient_id=$1 AND read_at IS NULL", recipientID)
	if err != nil {
		return 0, 0, err
	}
	updated, _ := res.RowsAffected()
	unread, err := s.NotificationUnreadCount(ctx, recipientID)
	return updated, unread, err
}
