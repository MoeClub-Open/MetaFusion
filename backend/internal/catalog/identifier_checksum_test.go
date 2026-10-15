package catalog

import (
	"encoding/json"
	"testing"
)

func TestISBNAndGTINChecksums(t *testing.T) {
	for _, value := range []string{"0306406152", "080442957X", "9780306406157", "9791090636071"} {
		if !validISBN(value) {
			t.Errorf("valid ISBN rejected: %s", value)
		}
	}
	for _, value := range []string{"0306406153", "X80442957X", "0804429570", "9780306406152", "4006381333931", "030640615"} {
		if validISBN(value) {
			t.Errorf("invalid ISBN accepted: %s", value)
		}
	}
	for _, value := range []string{"96385074", "036000291452", "4006381333931", "10012345000017"} {
		if !validGTIN(value) {
			t.Errorf("valid GTIN rejected: %s", value)
		}
		bad := value[:len(value)-1] + string('0'+(value[len(value)-1]-'0'+1)%10)
		if validGTIN(bad) {
			t.Errorf("invalid GTIN accepted: %s", bad)
		}
	}
	for _, value := range []string{"400638133393X", "", "400638133393", "0000000"} {
		if validGTIN(value) {
			t.Errorf("invalid GTIN shape accepted: %s", value)
		}
	}
}

func TestExternalSourceISBNConversionAndRawPreservation(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"0-306-40615-2", "9780306406157"},
		{"0 8044 2957 x", "9780804429573"},
		{"978-0-306-40615-7", "9780306406157"},
	} {
		got, ok := externalSourceISBNBarcode(tc.raw)
		if !ok || got != tc.want {
			t.Fatalf("ISBN %q became %q, want %q", tc.raw, got, tc.want)
		}
	}
	for _, raw := range []string{"9780306406152", "0306406153", "ISBN 9780306406157", "https://example.org/9780306406157"} {
		if got, ok := externalSourceISBNBarcode(raw); ok {
			t.Errorf("invalid ISBN %q became %q", raw, got)
		}
		encoded, _ := json.Marshal(raw)
		subject := bangumiSubject{Infobox: []bangumiInfoItem{{Key: "ISBN", Value: encoded}}}
		if _, ok := subject.infoboxValues()["barcode"]; ok {
			t.Fatalf("invalid infobox ISBN %q produced a barcode", raw)
		}
		if got := subject.infoboxEntries(); len(got) != 1 || got[0]["value"] != raw {
			t.Fatalf("invalid raw ISBN must remain traceable: %#v", got)
		}
	}
	encoded, _ := json.Marshal("0-306-40615-2")
	subject := bangumiSubject{Infobox: []bangumiInfoItem{{Key: "ISBN", Value: encoded}}}
	if got := subject.infoboxValues()["barcode"]; got != "9780306406157" {
		t.Fatalf("valid infobox ISBN-10 must recompute EAN check digit: %v", got)
	}
}
