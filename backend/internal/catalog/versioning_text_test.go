package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionTextThreeWay(t *testing.T) {
	for _, test := range []struct {
		name, base, current, proposed, want string
		ok                                  bool
	}{
		{"paragraphs", "one\ntwo\nthree\n", "ONE\ntwo\nthree\n", "one\ntwo\nTHREE\n", "ONE\ntwo\nTHREE\n", true},
		{"adjacent", "a\nb\nc", "A\nb\nc", "a\nB\nc", "A\nB\nc", true},
		{"same edit", "a\nb\n", "a\nB\n", "a\nB\n", "a\nB\n", true},
		{"overlap", "a\nb", "a\nB", "a\nC", "", false},
		{"competing inserts", "a\nb", "a\nx\nb", "a\ny\nb", "", false},
		{"disjoint inserts", "a\nb\nc\n", "x\na\nb\nc\n", "a\nb\nc\ny\n", "x\na\nb\nc\ny\n", true},
		{"delete and edit", "a\nb\nc\n", "b\nc\n", "a\nb\nC\n", "b\nC\n", true},
		{"delete modified", "a\nb\n", "a\n", "a\nB\n", "", false},
		{"crlf", "a\r\nb\r\nc\r\n", "A\r\nb\r\nc\r\n", "a\r\nb\r\nC\r\n", "A\r\nb\r\nC\r\n", true},
		{"no trailing newline", "a\nb\nc", "A\nb\nc", "a\nb\nC", "A\nb\nC", true},
		{"empty base conflict", "", "first", "second", "", false},
		{"unchanged proposal", "a", "A", "a", "A", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mergeVersionText(test.base, test.current, test.proposed)
			if ok != test.ok || ok && got != test.want {
				t.Fatalf("got %q,%v want %q,%v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestVersionLineDiffRoundTrip(t *testing.T) {
	values := []string{"", "a", "a\n", "a\na\nb\n", "a\nb\na", "\r\n", "日本語\n本文\n", strings.Repeat("same\n", 30)}
	for _, a := range values {
		for _, b := range values {
			base := versionLines(a)
			edits, ok := versionLineEdits(base, versionLines(b))
			if !ok {
				t.Fatal("small input exceeded budget")
			}
			position := 0
			var result strings.Builder
			for _, edit := range edits {
				if edit.start < position || edit.end < edit.start || edit.end > len(base) {
					t.Fatalf("invalid edit: %+v", edit)
				}
				result.WriteString(strings.Join(base[position:edit.start], ""))
				result.WriteString(strings.Join(edit.lines, ""))
				position = edit.end
			}
			result.WriteString(strings.Join(base[position:], ""))
			if result.String() != b {
				t.Fatalf("%q -> %q produced %q", a, b, result.String())
			}
		}
	}
}

func TestVersionMergedTextRetainsContribution(t *testing.T) {
	raw := func(body string) json.RawMessage {
		return json.RawMessage(encode(map[string]any{"translations": map[string]any{"en": map[string]any{"body": body}}}))
	}
	if !versionTextContribution(raw("a\nb\nc\n"), raw("a\nB\nc\n"), raw("A\nb\nc\n"), raw("A\nB\nc\n"), []string{"translations", "en", "body"}) {
		t.Fatal("a surviving disjoint edit lost its contributor")
	}
	if versionTextContribution(raw("a\nb\n"), raw("a\nB\n"), raw("a\nb\n"), raw("a\nb\n"), []string{"translations", "en", "body"}) {
		t.Fatal("an undone edit invented a contribution")
	}
}
