// Package testutil provisions isolated databases for integration tests.
package testutil

import (
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func Database(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MF_V2_TEST_DSN")
	if dsn == "" {
		if os.Getenv("MF_REQUIRE_INTEGRATION_TESTS") == "1" {
			t.Fatal("MF_REQUIRE_INTEGRATION_TESTS=1 requires MF_V2_TEST_DSN for isolated PostgreSQL integration tests")
		}
		t.Skip("MF_V2_TEST_DSN must identify an isolated PostgreSQL test server")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "mf_v2_test") {
		t.Fatal("MF_V2_TEST_DSN must be a postgres URL with a mf_v2_test database name")
	}
	u.Path = "/postgres"
	control, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal("cannot open test PostgreSQL connection")
	}
	name := "mf_v2_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = control.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name)); err != nil {
		control.Close()
		t.Fatalf("create isolated test database: %v", err)
	}
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal("cannot connect to newly created test database")
	}
	t.Cleanup(func() {
		db.Close()
		// Only this invocation's generated database is ever removed.
		if _, err := control.Exec("DROP DATABASE " + pq.QuoteIdentifier(name)); err != nil {
			t.Errorf("cleanup isolated database: %v", err)
		}
		control.Close()
	})
	return db
}
