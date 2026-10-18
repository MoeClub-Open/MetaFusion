package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// deliverEvent 是带稳定事件身份与可选事件时间的跨服务投递（A04 验收口径）。
func (f *notifyFixture) deliverEvent(t *testing.T, recipientID, entityID, eventID, excerpt, eventTime string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"dedupe_key\":\"comment.replied:entity:%s\",\"event_id\":\"%s\",\"event_time\":\"%s\",\"actor_id\":\"%s\",\"actor_name\":\"%s\",\"payload\":{\"entity_id\":\"%s\",\"excerpt\":\"%s\"}}",
		recipientID, entityID, entityID, eventID, eventTime, f.owner.ID, f.owner.Username, entityID, excerpt)
	return f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal", body,
		map[string]string{InternalTokenHeader: testInternalToken})
}

func receiptCount(t *testing.T, f *notifyFixture, recipientID string) int {
	t.Helper()
	var n int
	if err := f.s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog.notification_receipts WHERE recipient_id=$1", recipientID).Scan(&n); err != nil {
		t.Fatalf("收据表应存在且可查（先跑 mf-migrate up）：%v", err)
	}
	return n
}

// A/B/A：交错的新事件照常计数，重投的旧事件整体不更新（计数/展示字段都不动）。
// 旧口径只比 last_event_id，第二个 A 会被误判成重试而丢掉。
func TestNotificationRedeliveryABAOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "ABA 用例作品"})
	for _, ce := range []struct{ id, excerpt string }{
		{"aaaaaaaa-0000-0000-0000-0000000000a1", "A"},
		{"aaaaaaaa-0000-0000-0000-0000000000b1", "B"},
		{"aaaaaaaa-0000-0000-0000-0000000000a2", "A2"},
	} {
		if w := f.deliverEvent(t, f.owner.ID, work.ID, ce.id, ce.excerpt, ""); w.Code != 200 {
			t.Fatalf("投递 %s 应 200: %d %s", ce.id, w.Code, w.Body.String())
		}
	}
	items := f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("A/B/A2 应合并成一行 count=3: %+v", items)
	}
	if got, _ := items[0].Payload["excerpt"].(string); got != "A2" {
		t.Fatalf("聚合行应展示最新事件，实际 %q", got)
	}
	if n := receiptCount(t, f, f.owner.ID); n != 3 {
		t.Fatalf("收据应有 3 条，实际 %d", n)
	}
	// 响应丢失后重投 A：计数、展示字段、收据数全都不动。
	if w := f.deliverEvent(t, f.owner.ID, work.ID, "aaaaaaaa-0000-0000-0000-0000000000a1", "A", ""); w.Code != 200 {
		t.Fatalf("重投应 200: %d %s", w.Code, w.Body.String())
	}
	items = f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("重投旧事件不得累加计数：%+v", items)
	}
	if got, _ := items[0].Payload["excerpt"].(string); got != "A2" {
		t.Fatalf("重投旧事件不得刷新展示字段，实际 %q", got)
	}
	if n := receiptCount(t, f, f.owner.ID); n != 3 {
		t.Fatalf("重投不得新增收据，实际 %d", n)
	}
	if unread, err := f.s.NotificationUnreadCount(ctx, f.owner.ID); err != nil || unread != 1 {
		t.Fatalf("未读数应仍是 1: %v %d", err, unread)
	}
}

// 并发同事件：10 路并发投同一个 event_id，只计一次。
func TestNotificationConcurrentDuplicateEventOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "并发用例作品"})
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := f.deliverEvent(t, f.owner.ID, work.ID, "cccccccc-0000-0000-0000-000000000001", "并发", "")
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("并发投递 %d 应 200，实际 %d", i, c)
		}
	}
	items := f.notifs(f.owner)
	if len(items) != 1 || items[0].Count != 1 {
		t.Fatalf("并发同事件只应计一次：%+v", items)
	}
}

// 不同收件人同一事件：收据键含收件人，各自成行互不干扰。
func TestNotificationSameEventDifferentRecipientsOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	work := f.saveAs(f.owner, Entity{Kind: "work", Title: "多收件人用例作品"})
	eventID := "dddddddd-0000-0000-0000-000000000001"
	if w := f.deliverEvent(t, f.owner.ID, work.ID, eventID, "给主人", ""); w.Code != 200 {
		t.Fatalf("投递给主人应 200: %d %s", w.Code, w.Body.String())
	}
	if w := f.deliverEvent(t, f.reviewer.ID, work.ID, eventID, "给审稿人", ""); w.Code != 200 {
		t.Fatalf("同一事件投给另一收件人应 200: %d %s", w.Code, w.Body.String())
	}
	ownerItems := f.notifs(f.owner)
	reviewerItems := f.notifs(f.reviewer)
	if len(ownerItems) != 1 || ownerItems[0].Count != 1 {
		t.Fatalf("主人应有 1 条：%+v", ownerItems)
	}
	if len(reviewerItems) != 1 || reviewerItems[0].Count != 1 {
		t.Fatalf("审稿人应有 1 条：%+v", reviewerItems)
	}
}

// 展示按事件时间不按抵达顺序：后抵达的旧事件不把聚合行顶到前面。
func TestNotificationEventTimeOrderingOnPostgres(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	newWork := f.saveAs(f.owner, Entity{Kind: "work", Title: "新事件作品"})
	oldWork := f.saveAs(f.owner, Entity{Kind: "work", Title: "旧事件作品"})
	if w := f.deliverEvent(t, f.owner.ID, newWork.ID, "eeeeeeee-0000-0000-0000-000000000001", "新", ""); w.Code != 200 {
		t.Fatalf("新事件投递应 200: %d %s", w.Code, w.Body.String())
	}
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if w := f.deliverEvent(t, f.owner.ID, oldWork.ID, "eeeeeeee-0000-0000-0000-000000000002", "旧", past); w.Code != 200 {
		t.Fatalf("旧事件投递应 200: %d %s", w.Code, w.Body.String())
	}
	items, total, _, err := f.s.ListNotifications(ctx, f.owner.ID, NotificationQuery{Limit: 20, Offset: 0})
	if err != nil || total != 2 {
		t.Fatalf("应有 2 行：%v total=%d", err, total)
	}
	if items[0].SubjectID != newWork.ID || items[1].SubjectID != oldWork.ID {
		t.Fatalf("应按事件时间排序（新在前）：%+v", items)
	}
	if w := f.do(f.engine(&f.owner, testInternalToken), http.MethodPost, "/api/notifications/internal",
		fmt.Sprintf("{\"recipient_id\":\"%s\",\"type\":\"comment.replied\",\"subject_type\":\"entity\",\"subject_id\":\"%s\",\"event_time\":\"not-a-time\"}", f.owner.ID, newWork.ID),
		map[string]string{InternalTokenHeader: testInternalToken}); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_event_time") {
		t.Fatalf("非法 event_time 应 400 invalid_event_time：%d %s", w.Code, w.Body.String())
	}
}
