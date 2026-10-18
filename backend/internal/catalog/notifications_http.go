package catalog

// 站内通知的 HTTP 面：读取端（列表 / 未读数 / 标记已读）与跨服务投递端（internal）。
//
// 越权口径：四个读/写端点**一律以令牌身份为收件人**，请求体与查询串里没有 recipient_id
// 可传——参数里根本没有这个字段，而不是"传了也会被忽略"。标记已读的 UPDATE 把
// recipient_id 写进 WHERE，别人的通知与不存在的 id 都 0 行 → 统一 404 not_found
//（不区分"不存在"与"不是你的"，避免用 403/404 的差异探测他人通知的存在性）。

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// InternalTokenHeader 是跨服务投递端点的共享密钥头。密钥由编排注入
// （INTERNAL_API_TOKEN，见 deploy/docker-compose.yml 与 scripts/check_env_matrix.py），
// 未配置时端点整体关闭（503 internal_api_disabled）——**默认关闭**是有意的：
// 忘记配置的后果是"评论回复不产生通知"（可见、可排查），而不是"任何登录用户都能给任何人塞通知"。
const InternalTokenHeader = "X-Internal-Token"

// notificationPaging 解析列表分页：limit 缺省 20、上限 100（越界收敛，不 400——
// 与 /catalog/entities 的静默收敛风格一致；列表页拿到的总数与页宽由响应自己描述）。
func notificationPaging(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	offset, _ := strconv.Atoi(strings.TrimSpace(c.Query("offset")))
	if limit <= 0 || limit > maxNotificationLimit {
		limit = defaultNotificationLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h HTTP) registerNotifications(api *gin.RouterGroup) {
	s := h.Store
	// 未读数是角标端点：只做一次部分索引聚合，供顶栏每 60 秒级轮询。
	// 与其余业务读写共用外层已验证主体的预算，不重复扣数或覆盖额度响应头。
	api.GET("/notifications/unread-count", required(""), func(c *gin.Context) {
		unread, err := s.NotificationUnreadCount(c.Request.Context(), user(c).ID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(200, gin.H{"unread": unread})
	})
	api.GET("/notifications", required(""), func(c *gin.Context) {
		limit, offset := notificationPaging(c)
		items, total, unread, err := s.ListNotifications(c.Request.Context(), user(c).ID, NotificationQuery{Limit: limit, Offset: offset, Category: c.Query("category"), Read: c.Query("read"), From: c.Query("from"), To: c.Query("to")})
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(200, gin.H{"items": items, "total": total, "unread": unread})
	})
	// 全部标记已读。不取"截止时间"参数：收件箱只有自己的行，口径就是"当前所有未读"。
	api.POST("/notifications/read-all", required(""), func(c *gin.Context) {
		updated, unread, err := s.MarkAllNotificationsRead(c.Request.Context(), user(c).ID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(200, gin.H{"ok": true, "updated": updated, "unread": unread})
	})
	api.POST("/notifications/:id/read", required(""), func(c *gin.Context) {
		unread, err := s.MarkNotificationRead(c.Request.Context(), user(c).ID, c.Param("id"))
		if err != nil {
			// sql.ErrNoRows（不是自己的/不存在）由 respond 统一成 404 not_found。
			respond(c, nil, err)
			return
		}
		c.JSON(200, gin.H{"ok": true, "unread": unread})
	})
	// 投递端点允许匿名进 handler：凭据是 X-Internal-Token（handler 内校验），
	// 作者来自投递体 actor 快照，调用方以 X-Internal-Token 认证。
	api.POST("/notifications/internal", h.deliverInternal)
}

// notificationDelivery 是跨服务投递的请求体（互动服务 → 目录）。
// 只有服务端能构造的字段（type 白名单、payload）在这里开放，收件人由投递方声明——
// 这正是需要共享密钥的原因：用户令牌只证明"谁做的"，证明不了"这条通知是服务生成的"。
type notificationDelivery struct {
	RecipientID string         `json:"recipient_id"`
	Type        string         `json:"type"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	Payload     map[string]any `json:"payload"`
	DedupeKey   string         `json:"dedupe_key"`
	// EventID 是投递方给的稳定事件身份，用于让上游重试变成幂等（见 NotificationInput.EventID）。
	EventID   string `json:"event_id"`
	EventTime string `json:"event_time"` // 事件发生时间（RFC3339，可选）：展示按它排序，缺省即现在。
	// ActorID/ActorName 是产生端确认的作者快照。
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
}

// deliverInternal 是"评论被回复"等跨服务事件的唯一入口。
// X-Internal-Token 证明调用方是受信任服务；作者只认投递体里的 actor 快照。
func (h HTTP) deliverInternal(c *gin.Context) {
	if strings.TrimSpace(h.InternalToken) == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "internal_api_disabled"})
		return
	}
	given := c.GetHeader(InternalTokenHeader)
	// 定长比较：避免用响应时间逐字节试探密钥。
	if subtle.ConstantTimeCompare([]byte(given), []byte(h.InternalToken)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_token"})
		return
	}
	var in notificationDelivery
	if !body(c, &in) {
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(in.RecipientID)); err != nil {
		c.JSON(400, gin.H{"error": "invalid_recipient_id"})
		return
	}
	if !validNotificationType(in.Type) {
		c.JSON(400, gin.H{"error": "invalid_notification_type"})
		return
	}
	if len(in.SubjectID) > 200 || len(in.DedupeKey) > 300 || len(in.SubjectType) > 64 || len(in.EventID) > 200 || len(in.EventTime) > 64 || len(in.ActorID) > 64 || len(in.ActorName) > 200 {
		c.JSON(400, gin.H{"error": "invalid_payload"})
		return
	}
	var eventTime time.Time
	if s := strings.TrimSpace(in.EventTime); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid_event_time"})
			return
		}
		eventTime = t
	}
	actorID := strings.TrimSpace(in.ActorID)
	actorName := in.ActorName
	if _, err := uuid.Parse(actorID); err != nil {
		c.JSON(400, gin.H{"error": "invalid_actor"})
		return
	}
	err := notify(c.Request.Context(), h.Store.DB, NotificationInput{
		RecipientID: strings.TrimSpace(in.RecipientID),
		Type:        in.Type,
		ActorID:     actorID,
		ActorName:   actorName,
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		Payload:     in.Payload,
		DedupeKey:   in.DedupeKey,
		EventID:     in.EventID,
		EventTime:   eventTime,
	})
	if err != nil {
		respond(c, nil, err)
		return
	}
	unread, err := h.Store.NotificationUnreadCount(c.Request.Context(), strings.TrimSpace(in.RecipientID))
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(200, gin.H{"ok": true, "unread": unread})
}
