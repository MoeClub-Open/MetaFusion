package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestStorageLifecycleClientRejectsUnsafeConfigurationAndReceipts(t *testing.T) {
	for _, base := range []string{"", "ftp://storage", "http://user:secret@storage", "http://storage?token=x", "http://storage/path", "http://storage/#x"} {
		if _, err := newStorageLifecycleClient(base, "test-service-key"); err == nil {
			t.Fatalf("accepted invalid service URL %q", base)
		}
	}
	if _, err := newStorageLifecycleClient("http://storage", " "); err == nil {
		t.Fatal("empty service key accepted")
	}
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
	}))
	defer other.Close()
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/catalog/lifecycle" || r.Header.Get("Authorization") != "Bearer test-service-key" {
			t.Error("service path/credential missing")
		}
		switch mode.Load() {
		case 0:
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		case 1:
			_, _ = w.Write([]byte(`{"ok":false}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	client, err := newStorageLifecycleClient(server.URL, "test-service-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.apply(context.Background(), storageLifecycleSnapshot{}); err == nil || leaked.Load() {
		t.Fatal("redirect was acknowledged or leaked a service credential")
	}
	mode.Store(1)
	if err := client.apply(context.Background(), storageLifecycleSnapshot{}); err == nil {
		t.Fatal("negative receipt was acknowledged")
	}
	mode.Store(2)
	if err := client.apply(context.Background(), storageLifecycleSnapshot{}); err != nil {
		t.Fatal(err)
	}
}

func TestStorageLifecycleBootstrapsExistingBindingsWithoutEventsOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	source := f.save(Entity{Kind: "work", Title: "bootstrap source"})
	target := f.save(Entity{Kind: "work", Title: "bootstrap target"})
	if _, err := f.s.Lifecycle(ctx, source.ID, LifecycleEdit{ExpectedVersion: source.Version, TargetID: target.ID,
		EditNote: "merge", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.ExecContext(ctx, "DELETE FROM catalog.outbox"); err != nil {
		t.Fatal(err)
	}
	missing := uuid.NewString()
	ids := []string{source.ID, missing}
	sort.Strings(ids)
	var fail atomic.Bool
	fail.Store(true)
	var received []storageLifecycleSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bootstrap-test-key" {
			t.Error("inventory request omitted service authentication")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/internal/catalog/binding-targets" {
			after := r.URL.Query().Get("after")
			page := []string{}
			for _, id := range ids {
				if id > after {
					page = append(page, id)
				}
			}
			_ = json.NewEncoder(w).Encode(storageBindingTargets{EntityIDs: page})
			return
		}
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var state storageLifecycleSnapshot
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			t.Error(err)
		}
		received = append(received, state)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client, err := newStorageLifecycleClient(server.URL, "bootstrap-test-key")
	if err != nil {
		t.Fatal(err)
	}
	after, done, err := f.s.bootstrapStorageLifecycle(ctx, client, "")
	if err == nil || done || after != "" {
		t.Fatalf("failed bootstrap must retry its first identity: cursor=%s done=%t err=%v", after, done, err)
	}
	fail.Store(false)
	after, done, err = f.s.bootstrapStorageLifecycle(ctx, client, after)
	if err != nil || !done || after != ids[len(ids)-1] || len(received) != 2 {
		t.Fatalf("bootstrap did not converge without historical outbox: cursor=%s done=%t received=%d err=%v", after, done, len(received), err)
	}
	for _, state := range received {
		if state.EntityID == missing {
			if state.Status != "deleted" {
				t.Fatal("physically missing identity was not a deletion snapshot")
			}
		} else if state.EntityID != target.ID || !contains(state.Aliases, source.ID) {
			t.Fatalf("historical binding did not resolve to its current canonical identity: %+v", state)
		}
	}
	// Restart has no remembered cursor. It deliberately replays the inventory,
	// so recovery does not depend on old outbox events still being retained.
	before := len(received)
	after, done, err = f.s.bootstrapStorageLifecycle(ctx, client, "")
	if err != nil || !done || after != ids[len(ids)-1] || len(received) != before+2 {
		t.Fatalf("restart did not safely replay existing identities: cursor=%s done=%t received=%d err=%v", after, done, len(received), err)
	}
	for i := 0; i < before; i++ {
		original, replayed := received[i], received[before+i]
		if replayed.EntityID != original.EntityID || replayed.Status != original.Status || replayed.Version != original.Version ||
			!reflect.DeepEqual(replayed.Aliases, original.Aliases) || !reflect.DeepEqual(replayed.AliasVersions, original.AliasVersions) {
			t.Fatalf("restart changed lifecycle identity facts: original=%+v replayed=%+v", original, replayed)
		}
	}
}

func TestStorageLifecycleClientRejectsBrokenBindingInventory(t *testing.T) {
	id1 := "11111111-1111-1111-1111-111111111111"
	id2 := "22222222-2222-2222-2222-222222222222"
	for name, page := range map[string]storageBindingTargets{
		"missing array": {},
		"non UUID":      {EntityIDs: []string{"invalid"}},
		"UUID variant":  {EntityIDs: []string{"urn:uuid:" + id1}},
		"duplicate":     {EntityIDs: []string{id1, id1}},
		"out of order":  {EntityIDs: []string{id2, id1}},
		"bad cursor":    {EntityIDs: []string{id1}, NextCursor: id2},
		"empty cursor":  {EntityIDs: []string{}, NextCursor: id1},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			client, err := newStorageLifecycleClient(server.URL, "inventory-test-key")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.bindingTargets(context.Background(), ""); err == nil {
				t.Fatal("malformed inventory was accepted")
			}
		})
	}
}

func TestStorageLifecycleRetriesCurrentCanonicalFactsOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sourceAsset, targetAsset := uuid.NewString(), uuid.NewString()
	sp, tp := testPicture("https://example.test/source.png"), testPicture("https://example.test/target.png")
	sp.AssetID, tp.AssetID = sourceAsset, targetAsset
	source := f.save(Entity{Kind: "work", Title: "source", Pictures: []Picture{sp}})
	target := f.save(Entity{Kind: "work", Title: "target", Pictures: []Picture{tp}})
	if _, err := f.s.Lifecycle(ctx, source.ID, LifecycleEdit{ExpectedVersion: source.Version, TargetID: target.ID,
		EditNote: "merge", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	fail.Store(true)
	var received []storageLifecycleSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var state storageLifecycleSnapshot
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			t.Error(err)
		}
		received = append(received, state)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client, err := newStorageLifecycleClient(server.URL, "test-service-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.syncStorageLifecycle(ctx, client); err == nil {
		t.Fatal("upstream failure should leave a retryable event")
	}
	var count int
	if err := f.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.deliveries WHERE consumer=$1", StorageLifecycleConsumer).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed delivery was acknowledged: count=%d err=%v", count, err)
	}
	fail.Store(false)
	if err := f.s.syncStorageLifecycle(ctx, client); err != nil {
		t.Fatal(err)
	}
	if len(received) == 0 {
		t.Fatal("no snapshots delivered")
	}
	for _, state := range received {
		if state.EntityID != target.ID || state.Status != "published" || !contains(state.Aliases, source.ID) ||
			!contains(state.CoverAssetIDs, sourceAsset) || !contains(state.CoverAssetIDs, targetAsset) || state.AliasVersions[source.ID] <= source.Version {
			t.Fatalf("historical source event must use current canonical facts and preserve covers: %+v", state)
		}
	}
	before := len(received)
	if err := f.s.syncStorageLifecycle(ctx, client); err != nil || len(received) != before {
		t.Fatalf("acknowledged batch replayed: err=%v", err)
	}
	target, err = f.s.Unpublish(ctx, target.ID, UnpublishEdit{ExpectedVersion: target.Version, EditNote: "unpublish", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.s.currentStorageLifecycle(ctx, source.ID)
	if err != nil || state.Status != "draft" || !contains(state.CoverAssetIDs, sourceAsset) || !contains(state.CoverAssetIDs, targetAsset) {
		t.Fatalf("draft should retain both picture references: %+v err=%v", state, err)
	}
	if _, err := f.s.Lifecycle(ctx, target.ID, LifecycleEdit{ExpectedVersion: target.Version, EditNote: "delete", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	state, err = f.s.currentStorageLifecycle(ctx, source.ID)
	if err != nil || state.EntityID != target.ID || state.Status != "deleted" || !contains(state.Aliases, source.ID) {
		t.Fatalf("canonical delete must cover historical aliases: %+v err=%v", state, err)
	}
}
