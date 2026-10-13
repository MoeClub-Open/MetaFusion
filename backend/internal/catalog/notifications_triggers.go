package catalog

// 通知的**产生端**：目录服务侧的三类事件（审核结果、收录、导入完成）在此收口。
// 每个产生点都在**已经持锁/已开事务**的写路径里调用，通知行与业务行同事务提交：
// 要么"条目已发布且通知已入箱"，要么两者都没有——不存在"发布了但通知丢了"的半成品。
//
// 接收者解析规则（唯一实现，不允许各处自己猜）：
//   - 审核结果 → 实体 owner（catalog.entities.created_by）。提案创建的新实体 owner 就是提交者，
//     因此"提案被通过/驳回"落在这条规则上；编辑他人实体时被通知的是条目 owner。
//   - 收录 → 被收录实体（release.subjects 的 work、includes 关系的 target）的 owner。
//   - 导入完成 → 发起导入的人自己（系统回执，不是"别人对你做了什么"）。
// 所有规则都跳过"收件人 = 操作者"：自己改自己的东西不该收到通知。

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

// notifySaveOutcome 在 Save 事务内按"状态跃迁 + 收录变化"产生通知。before 为 nil 表示新建。
func notifySaveOutcome(ctx context.Context, tx *sql.Tx, before *Entity, after Entity, actor User) error {
	if before == nil {
		// 新建只可能带出"收录"事件（新建 release 声明了 subjects），审核结果需要一次跃迁。
		return notifyIncludedSubjects(ctx, tx, nil, after, actor)
	}
	owner := after.CreatedBy
	if owner != "" && owner != actor.ID {
		switch {
		case before.Status != "published" && after.Status == "published":
			// 审核通过：从 draft/pending_review 变成公开可见，且不是 owner 自己做的。
			payload := entityNotificationPayload(after)
			payload["from_status"] = before.Status
			if err := notify(ctx, tx, NotificationInput{
				RecipientID: owner, Type: NotificationReviewApproved,
				ActorID: actor.ID, ActorName: actor.Username,
				SubjectType: "entity", SubjectID: after.ID, Payload: payload,
				DedupeKey: fmt.Sprintf("%s:%s:v%d", NotificationReviewApproved, after.ID, after.Version),
			}); err != nil {
				return err
			}
		case before.Status == "pending_review" && after.Status == "draft":
			// 审核驳回：待审被退回草稿（发布中条目的降级走 Unpublish，不在这里）。
			payload := entityNotificationPayload(after)
			payload["from_status"] = before.Status
			if err := notify(ctx, tx, NotificationInput{
				RecipientID: owner, Type: NotificationReviewRejected,
				ActorID: actor.ID, ActorName: actor.Username,
				SubjectType: "entity", SubjectID: after.ID, Payload: payload,
				DedupeKey: fmt.Sprintf("%s:%s:v%d", NotificationReviewRejected, after.ID, after.Version),
			}); err != nil {
				return err
			}
		}
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


