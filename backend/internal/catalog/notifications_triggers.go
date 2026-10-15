package catalog

// 收录通知与事实写入同事务；审核通知由 commit_review.go 发给提交者。

import (
	"context"
	"database/sql"
	"fmt"
)

// entityNotificationPayload 是实体类通知的最小展示载荷：id / kind / title。
// 刻意不带 attributes 或整份实体——通知是"带你去看看"的入口，不是实体快照。
func entityNotificationPayload(e Entity) map[string]any {
	return map[string]any{
		"entity_id":    e.ID,
		"entity_kind":  e.Kind,
		"entity_title": e.Title,
		"version":      e.Version,
	}
}

// notifySaveOutcome only observes changes to release subjects.
func notifySaveOutcome(ctx context.Context, tx *sql.Tx, before *Entity, after Entity, actor User) error {
	if before == nil {
		// 新建只可能带出"收录"事件（新建 release 声明了 subjects），审核结果需要一次跃迁。
		return notifyIncludedSubjects(ctx, tx, nil, after, actor)
	}
	return notifyIncludedSubjects(ctx, tx, before, after, actor)
}

// notifyIncludedSubjects 处理"发行声明收录某作品"这类收录：本次新增而旧版本没有的 subject 各自通知其 owner。
// 旧版本里的既有 subject 不重复通知（不然每次改个封面都会把收录事件重播一遍）。
func notifyIncludedSubjects(ctx context.Context, tx *sql.Tx, before *Entity, after Entity, actor User) error {
	if after.Kind != "release" || len(after.Subjects) == 0 {
		return nil
	}
	seen := map[string]bool{}
	if before != nil {
		for _, s := range before.Subjects {
			seen[s.WorkID] = true
		}
	}
	for _, s := range after.Subjects {
		if s.WorkID == "" || seen[s.WorkID] {
			continue
		}
		seen[s.WorkID] = true
		work, err := get(ctx, tx, s.WorkID)
		if err != nil {
			// 悬挂引用（subject 指向的 work 不存在）在 impact/体检里是警告级别，不该让本次写入失败；
			// 但也不是静默：无法解析收件人就没有通知，写入照常。
			continue
		}
		if work.CreatedBy == "" || work.CreatedBy == actor.ID {
			continue
		}
		payload := entityNotificationPayload(work)
		payload["container_id"] = after.ID
		payload["container_kind"] = after.Kind
		payload["container_title"] = after.Title
		payload["role"] = s.Role
		if err := notify(ctx, tx, NotificationInput{
			RecipientID: work.CreatedBy, Type: NotificationEntityIncluded,
			ActorID: actor.ID, ActorName: actor.Username,
			SubjectType: "entity", SubjectID: work.ID, Payload: payload,
			DedupeKey: fmt.Sprintf("%s:%s:%s", NotificationEntityIncluded, work.ID, after.ID),
		}); err != nil {
			return err
		}
	}
	return nil
}

// notifyIncludedRelation 处理 includes 关系（集合/作品包含另一作品或集合）里的"被收录"方向：
// 收件人是 target 的 owner，容器是 source。只在新建关系时通知——改属性不是新的收录事件。
func notifyIncludedRelation(ctx context.Context, tx *sql.Tx, r Relation, src, tgt Entity, actor User) error {
	if r.Type != "includes" {
		return nil
	}
	if tgt.CreatedBy == "" || tgt.CreatedBy == actor.ID {
		return nil
	}
	payload := entityNotificationPayload(tgt)
	payload["container_id"] = src.ID
	payload["container_kind"] = src.Kind
	payload["container_title"] = src.Title
	payload["role"] = "includes"
	return notify(ctx, tx, NotificationInput{
		RecipientID: tgt.CreatedBy, Type: NotificationEntityIncluded,
		ActorID: actor.ID, ActorName: actor.Username,
		SubjectType: "entity", SubjectID: tgt.ID, Payload: payload,
		DedupeKey: fmt.Sprintf("%s:%s:%s", NotificationEntityIncluded, tgt.ID, src.ID),
	})
}
