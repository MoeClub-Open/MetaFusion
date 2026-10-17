package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestRelationshipReadMigrationPreservesCustomLabels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	config, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credit := config.Document.Relations["credit_for"]
	credit.Names["zh-CN"] = "参与制作"
	credit.ReverseNames["zh-CN"] = "署名人员"
	credit.ReverseNames["en-US"] = "Custom contribution history"
	config.Document.Relations["credit_for"] = credit
	originalETag := uuid.NewString()
	if _, err = f.s.DB.Exec(`UPDATE catalog.definition_config SET document=$1,etag=$2 WHERE singleton`, encode(config.Document), originalETag); err != nil {
		t.Fatal(err)
	}
	script, err := migrations.FS.ReadFile("000033_relationship_reads.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.DB.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	after, err := definitions(ctx, f.s.DB)
	if err != nil {
		t.Fatal(err)
	}
	actual := after.Document.Relations["credit_for"]
	if actual.Names["zh-CN"] != "参与主体" || actual.ReverseNames["zh-CN"] != "参与记录" || actual.ReverseNames["en-US"] != "Custom contribution history" || after.ETag == originalETag {
		t.Fatalf("labels/customization/etag: %+v", after)
	}
	if _, err = f.s.DB.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	repeated, err := definitions(ctx, f.s.DB)
	if err != nil || repeated.ETag != after.ETag {
		t.Fatalf("repeat migration changed definitions: %v", err)
	}
}
