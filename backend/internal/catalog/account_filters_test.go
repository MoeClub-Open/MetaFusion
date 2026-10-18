package catalog

import (
	"context"
	"testing"
	"time"
)

func TestCalendarRangeUsesExclusiveUTCEnd(t *testing.T) {
	start, end, err := calendarRange("2026-10-18", "2026-10-18")
	if err != nil || start.Format(time.RFC3339) != "2026-10-18T00:00:00Z" || end.Format(time.RFC3339) != "2026-10-19T00:00:00Z" {
		t.Fatalf("range: %v %v %v", start, end, err)
	}
	for _, dates := range [][2]string{{"2026-02-30", ""}, {"2026-10-19", "2026-10-18"}, {"invalid", "2026-01-01"}} {
		if _, _, err := calendarRange(dates[0], dates[1]); err == nil {
			t.Fatalf("accepted invalid dates: %v", dates)
		}
	}
}

func TestPostgresNotificationCategoriesDatesAndReadState(t *testing.T) {
	f := newNotifyFixture(t)
	ctx := context.Background()
	for i, kind := range []string{NotificationCommentReplied, NotificationEntityIncluded, NotificationReviewApproved, NotificationReviewRejected} {
		date := time.Date(2026, 10, 18+i/2, 0, 0, 0, 0, time.UTC)
		if err := notify(ctx, f.s.DB, NotificationInput{RecipientID: f.owner.ID, Type: kind, ActorID: f.owner.ID, SubjectID: kind, EventTime: date}); err != nil {
			t.Fatal(err)
		}
	}
	query := NotificationQuery{Limit: 1, Category: "review", From: "2026-10-19", To: "2026-10-19", Read: "unread"}
	items, total, unread, err := f.s.ListNotifications(ctx, f.owner.ID, query)
	if err != nil || len(items) != 1 || total != 2 || unread != 4 {
		t.Fatalf("filter/page/count: %v %d %d %d", err, len(items), total, unread)
	}
	if _, err := f.s.MarkNotificationRead(ctx, f.owner.ID, items[0].ID); err != nil {
		t.Fatal(err)
	}
	query.Read = "read"
	items, total, unread, err = f.s.ListNotifications(ctx, f.owner.ID, query)
	if err != nil || len(items) != 1 || total != 1 || unread != 3 {
		t.Fatalf("read filter: %v %d %d %d", err, len(items), total, unread)
	}
	query.From, query.To = "2026-10-18", "2026-10-18"
	if items, total, _, err = f.s.ListNotifications(ctx, f.owner.ID, query); err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("date boundary: %v %d", err, total)
	}
	if items, total, _, err = f.s.ListNotifications(ctx, f.reviewer.ID, NotificationQuery{}); err != nil || total != 0 || len(items) != 0 {
		t.Fatal("another inbox leaked")
	}
}
