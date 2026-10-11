package catalog

import (
	"testing"
)

func TestSourceEligibility(t *testing.T) {
	policy, err := newSourcePolicy("https://FindVerse.CC.:8443", "mirror.example, archive.example.")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		source Source
		want   string
	}{
		{"self without URL", Source{Kind: "self", Citation: "internal assertion"}, "source_not_eligible"},
		{"self with external URL", Source{Kind: "self", Citation: "assertion", URL: "https://example.org/item"}, "source_not_eligible"},
		{"instance", Source{Kind: "url", Citation: "item", URL: "https://findverse.cc/catalog/123"}, "source_not_eligible"},
		{"case trailing dot port", Source{Kind: "url", Citation: "item", URL: "HTTP://FINDVERSE.CC.:8080/catalog/123"}, "source_not_eligible"},
		{"subdomain", Source{Kind: "url", Citation: "item", URL: "https://wiki.findverse.cc/item"}, "source_not_eligible"},
		{"publication instance URL", Source{Kind: "publication", Citation: "book p. 3", URL: "https://findverse.cc/item"}, "source_not_eligible"},
		{"extra blocked host", Source{Kind: "url", Citation: "item", URL: "https://sub.archive.example/item"}, "source_not_eligible"},
		{"evil suffix", Source{Kind: "url", Citation: "item", URL: "https://findverse.cc.example.org/item"}, ""},
		{"hostname suffix without dot", Source{Kind: "url", Citation: "item", URL: "https://notfindverse.cc/item"}, ""},
		{"external community", Source{Kind: "url", Citation: "community item", URL: "https://bgm.tv/subject/7"}, ""},
		{"publication without URL", Source{Kind: "publication", Citation: "ISBN 9780306406157, p. 3"}, ""},
		{"publication external URL", Source{Kind: "publication", Citation: "book p. 3", URL: "https://example.org/book"}, ""},
		{"invalid URL", Source{Kind: "url", Citation: "item", URL: "javascript:alert(1)"}, "invalid_source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSourcesWithPolicy("edit", []Source{tc.source}, policy)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if err := validateSourcesWithPolicy("", []Source{{Kind: "self", Citation: "x"}}, policy); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("empty edit note: %v", err)
	}
	if err := validateSourcesWithPolicy("edit", []Source{{Kind: "url", Citation: "external", URL: "https://example.org"}, {Kind: "self", Citation: "internal"}}, policy); err == nil || err.Error() != "source_not_eligible" {
		t.Fatalf("mixed evidence containing self accepted: %v", err)
	}
}

func TestSourcePolicyConfiguration(t *testing.T) {
	policy, err := newSourcePolicy("https://catalog.example", "mirror.example")
	if err != nil {
		t.Fatal(err)
	}
	if policy.eligible(Source{Kind: "url", URL: "https://catalog.example/item"}) {
		t.Fatal("self-hosted instance accepted as evidence")
	}
	if !policy.eligible(Source{Kind: "url", URL: "https://findverse.cc/item"}) {
		t.Fatal("configured public host must replace the default instance host")
	}
	defaults, err := newSourcePolicy("", "")
	if err != nil || defaults.eligible(Source{Kind: "url", URL: "https://findverse.cc/item"}) {
		t.Fatalf("default instance policy: %v", err)
	}
	for _, publicURL := range []string{"not-a-url", "https://", "https://user:pass@example.org", "https://example..org"} {
		if _, err := newSourcePolicy(publicURL, ""); err == nil {
			t.Fatalf("invalid public URL accepted: %q", publicURL)
		}
	}
	for _, hosts := range []string{"good.example,bad host", "https://example.org", "example.org/path", "example.org?query=x", "example..org"} {
		if _, err := newSourcePolicy("https://catalog.example", hosts); err == nil {
			t.Fatalf("invalid blocked hosts accepted: %q", hosts)
		}
	}
	ip, err := newSourcePolicy("http://[::1]:8080", "127.0.0.1")
	if err != nil || ip.eligible(Source{Kind: "url", URL: "http://[::1]:1234/item"}) || ip.eligible(Source{Kind: "url", URL: "http://127.0.0.1/item"}) {
		t.Fatalf("IP instance hosts not blocked: %v", err)
	}
}

func TestChangedEntitySourcesPreserveHistoricalEvidence(t *testing.T) {
	legacy := Source{Kind: "self", Citation: "historical assertion"}
	picture := Picture{URL: "https://images.example/old.jpg", Source: legacy}
	inclusion := Inclusion{ExpressionID: "expression", Position: 1, Sources: []Source{legacy}}
	previous := Entity{Pictures: PicturesJSON{picture}, Contents: []Inclusion{inclusion}}
	if err := validateChangedEntitySources(previous, previous); err != nil {
		t.Fatalf("unchanged historical sources rejected: %v", err)
	}
	if err := validateSourceShapes("historical", []Source{legacy}); err != nil {
		t.Fatalf("historical shape rejected: %v", err)
	}
	if err := validateChangedEntitySources(previous, Entity{}); err == nil || err.Error() != "source_not_eligible" {
		t.Fatalf("new self sources accepted: %v", err)
	}
	changedPicture := picture
	changedPicture.URL = "https://images.example/new.jpg"
	if err := validateChangedEntitySources(Entity{Pictures: PicturesJSON{changedPicture}}, previous); err == nil || err.Error() != "source_not_eligible" {
		t.Fatalf("changed picture reusing self accepted: %v", err)
	}
	changedInclusion := inclusion
	changedInclusion.ExpressionID = "other-expression"
	if err := validateChangedEntitySources(Entity{Contents: []Inclusion{changedInclusion}}, previous); err == nil || err.Error() != "source_not_eligible" {
		t.Fatalf("changed inclusion reusing self accepted: %v", err)
	}
	changedPicture.Source = Source{Kind: "url", Citation: "original cover", URL: "https://example.org/item"}
	changedInclusion.Sources = []Source{{Kind: "url", Citation: "tracklist", URL: "https://example.org/tracklist"}}
	if err := validateChangedEntitySources(Entity{Pictures: PicturesJSON{changedPicture}, Contents: []Inclusion{changedInclusion}}, previous); err != nil {
		t.Fatalf("new external evidence rejected: %v", err)
	}
	if err := validateChangedEntitySources(Entity{Contents: []Inclusion{{ExpressionID: "expression"}}}, Entity{}); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("new inclusion without evidence accepted: %v", err)
	}
}

func TestChangedEntitySourcesPreserveEmptyHistoricalEvidenceThroughJSON(t *testing.T) {
	previous := Entity{Contents: []Inclusion{{
		ExpressionID: "expression", Locator: Locator{}, Attributes: map[string]any{}, Sources: []Source{},
	}}}
	next := Entity{Contents: []Inclusion{{ExpressionID: "expression"}}}
	if err := validateChangedEntitySources(next, previous); err != nil {
		t.Fatalf("JSON omission of unchanged empty historical sources must survive: %v", err)
	}
	next.Contents[0].Position = 1
	if err := validateChangedEntitySources(next, previous); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("modified fact without evidence accepted: %v", err)
	}
	legacy := Source{Kind: "self", Citation: "historical assertion"}
	old := Picture{URL: "https://images.example/old.jpg", Caption: Names{}, Source: legacy}
	current := old
	current.Caption = nil
	if err := validateChangedEntitySources(Entity{Pictures: PicturesJSON{current}}, Entity{Pictures: PicturesJSON{old}}); err != nil {
		t.Fatalf("empty caption allocation must not change legacy picture: %v", err)
	}
}
