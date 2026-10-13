package catalog

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOutboundThrottleWaits(t *testing.T) {
	lim := newOutboundLimiter(100, 1)
	ctx := context.Background()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("throttle waited too long: %v", time.Since(start))
	}
}

func TestQueryExternalSourceLookupRecordsFetchMeta(t *testing.T) {
	stubBangumi(t)
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)
	f := newFixture(t)
	out, err := f.s.QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "bangumi", Op: "detail", URLOrID: "https://bgm.tv/subject/7", EntityType: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Op != "detail" || out.Source != "bangumi" || out.Preview == nil || out.Preview.ExternalID == "" {
		t.Fatalf("bad response: %+v", out)
	}
	if out.Fetch == nil || out.Fetch.URL == "" || !strings.HasPrefix(out.Fetch.ContentHash, "sha256:") {
		t.Fatalf("missing fetch meta: %+v", out.Fetch)
	}
	if out.Raw == nil {
		t.Fatal("expected raw payload")
	}
	if len(out.Fetches) == 0 {
		t.Fatal("expected fetches list")
	}
}

func TestQueryExternalSourceRejectsBadOp(t *testing.T) {
	_, err := (&Store{}).QueryExternalSource(context.Background(), ExternalSourceQueryRequest{Op: "merge"})
	if err == nil || err.Error() != "invalid_op" {
		t.Fatalf("got %v", err)
	}
}

func TestQueryExternalSourceSearchNotSupportedForDLsite(t *testing.T) {
	_, err := (&Store{}).QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "dlsite", Op: "search", Q: "test",
	})
	if err == nil || err.Error() != "search_not_supported" {
		t.Fatalf("got %v", err)
	}
}
