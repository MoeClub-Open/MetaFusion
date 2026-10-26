package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCredentialsCheckerLookupAndMatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/credentials-status/", func(w http.ResponseWriter, r *http.Request) {
		uid := r.URL.Path[len("/api/auth/credentials-status/"):]
		_ = json.NewEncoder(w).Encode(CredentialsStatus{UserID: uid, TokenVersion: 3, Banned: false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewCredentialsChecker(srv.URL)
	st, err := c.Lookup(context.Background(), "u1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !st.Match(3) {
		t.Fatalf("expected match for tv=3")
	}
	if st.Match(2) {
		t.Fatalf("stale tv must not match")
	}
	banned := CredentialsStatus{TokenVersion: 3, Banned: true}
	if banned.Match(3) {
		t.Fatalf("banned must not match")
	}
}

func TestCredentialsCheckerCache(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/credentials-status/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(CredentialsStatus{UserID: "u1", TokenVersion: 1})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewCredentialsChecker(srv.URL)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	if _, err := c.Lookup(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lookup(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call within TTL, got %d", calls)
	}
	now = now.Add(credentialsCacheTTL + time.Millisecond)
	if _, err := c.Lookup(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected refresh after TTL, got %d", calls)
	}
}

func TestClaimsToUserCarriesTokenVersion(t *testing.T) {
	u := ClaimsToUser(&Claims{Subject: "u1", Username: "a", TokenUse: TokenUseSession, TokenVersion: 7})
	if u == nil || u.TokenVersion != 7 {
		t.Fatalf("TokenVersion not carried: %+v", u)
	}
}
