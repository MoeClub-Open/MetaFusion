package catalog

import "testing"

func fuzzyClause(clauses []any) map[string]any {
	for _, c := range clauses {
		m, _ := c.(map[string]any)["multi_match"].(map[string]any)
		if m != nil && m["fuzziness"] != nil {
			return m
		}
	}
	return nil
}

func TestSearchMatchClausesFuzzy(t *testing.T) {
	m := fuzzyClause(searchMatchClauses("kuninaruu"))
	if m == nil {
		t.Fatal("expected a fuzzy clause for a 9-letter query")
	}
	if m["fuzziness"] != "AUTO" || m["prefix_length"] != 1 {
		t.Fatalf("unexpected fuzzy settings: %#v", m)
	}
	if b, _ := m["boost"].(float64); b <= 0 || b >= 1 {
		t.Fatalf("fuzzy clause must be down-weighted, got boost %v", m["boost"])
	}
}

func TestSearchMatchClausesShortQueryNotFuzzy(t *testing.T) {
	if fuzzyClause(searchMatchClauses("abc")) != nil {
		t.Fatal("queries shorter than searchFuzzyMinRunes must not use fuzzy matching")
	}
	if got := len(searchMatchClauses("abc")); got != 2 {
		t.Fatalf("want 2 clauses for a short query, got %d", got)
	}
}
