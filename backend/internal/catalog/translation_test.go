package catalog

import "testing"

func TestNonofficialTitleCannotReplaceOriginalLanguage(t *testing.T) {
	d := Defaults()
	for _, tc := range []struct {
		name, original, locale string
		nonofficial, invalid   bool
	}{
		{"original", "ja", "ja", true, true},
		{"regional alias", "ja", "ja-JP", true, true},
		{"regional original", "en-US", "en", true, true},
		{"translated", "ja", "zh-CN", true, false},
		{"different script", "zh-CN", "zh-TW", true, false},
		{"official original", "ja", "ja-JP", false, false},
		{"unspecified original", "", "zh-CN", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Entity{Kind: "work", Title: "Original", OriginalLanguage: tc.original,
				Translations: map[string]Translation{tc.locale: {Title: "Title", Nonofficial: tc.nonofficial}}}
			err := d.validateEntityContent(e, allowAllRef, false)
			if tc.invalid {
				if err == nil || err.Error() != "invalid_translation" {
					t.Fatalf("validation = %v, want invalid_translation", err)
				}
			} else if err != nil {
				t.Fatalf("valid translation rejected: %v", err)
			}
		})
	}
}
