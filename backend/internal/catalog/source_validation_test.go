package catalog

import (
	"testing"

	"github.com/google/uuid"
)

func TestOptionalSourcesValidateOnlyProvidedMetadata(t *testing.T) {
	cases := []struct {
		name    string
		sources []Source
		wantErr bool
	}{
		{name: "absent"},
		{name: "empty", sources: []Source{}},
		{name: "self", sources: []Source{{Kind: "self", Citation: "Author's observation"}}},
		{name: "instance URL", sources: []Source{{Kind: "url", Citation: "Related entry", URL: "https://findverse.cc/catalog/item"}}},
		{name: "publication", sources: []Source{{Kind: "publication", Citation: "Book, page 3"}}},
		{name: "empty supplied item", sources: []Source{{}}, wantErr: true},
		{name: "unknown kind", sources: []Source{{Kind: "unknown", Citation: "Reference"}}, wantErr: true},
		{name: "empty citation", sources: []Source{{Kind: "publication"}}, wantErr: true},
		{name: "URL missing", sources: []Source{{Kind: "url", Citation: "Page"}}, wantErr: true},
		{name: "unsafe URL", sources: []Source{{Kind: "url", Citation: "Page", URL: "javascript:alert(1)"}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSources(tc.sources)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSources = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

func TestPicturesAndInclusionsDoNotRequireSources(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	work := Entity{Kind: "work", Title: "Work", Pictures: PicturesJSON{{URL: "https://images.example/cover.jpg"}}}
	if err := d.validateEntityContent(work, ref, false); err != nil {
		t.Fatalf("picture without source: %v", err)
	}
	work.Pictures[0].Source = Source{Kind: "url", Citation: "Malformed URL", URL: "javascript:alert(1)"}
	if err := d.validateEntityContent(work, ref, false); err == nil {
		t.Fatal("provided malformed picture source accepted")
	}
	track := Entity{ID: uuid.NewString(), Kind: "track", State: "active", Title: "Track", MediumID: uuid.NewString(), Contents: []Inclusion{{ExpressionID: uuid.NewString(), Position: 1}}}
	if err := d.validateEntity(track, ref, false); err != nil {
		t.Fatalf("inclusion without sources: %v", err)
	}
	track.Contents[0].Sources = []Source{{Kind: "url", Citation: "Malformed URL", URL: "javascript:alert(1)"}}
	if err := d.validateEntity(track, ref, false); err == nil {
		t.Fatal("provided malformed inclusion source accepted")
	}
}
