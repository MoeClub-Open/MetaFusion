package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// compactBookIdentifier removes only ISBN's accepted separators. Arbitrary
// text is never stripped down to digits and mistaken for a product identifier.
func compactBookIdentifier(raw string) (string, bool) {
	var compact strings.Builder
	for _, ch := range strings.TrimSpace(raw) {
		switch {
		case ch >= '0' && ch <= '9':
			compact.WriteRune(ch)
		case ch == 'X' || ch == 'x':
			compact.WriteByte('X')
		case ch == '-' || ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
		default:
			return "", false
		}
	}
	return compact.String(), true
}

func validISBN(compact string) bool {
	if len(compact) == 13 {
		return (strings.HasPrefix(compact, "978") || strings.HasPrefix(compact, "979")) && validGTIN(compact)
	}
	if len(compact) != 10 {
		return false
	}
	sum := 0
	for i, ch := range compact {
		value := int(ch - '0')
		if i == 9 && ch == 'X' {
			value = 10
		} else if ch < '0' || ch > '9' {
			return false
		}
		sum += value * (10 - i)
	}
	return sum%11 == 0
}

// validGTIN supports EAN-8, UPC-A, EAN-13/JAN and GTIN-14. It never changes a
// source's invalid check digit to make that assertion look valid.
func validGTIN(compact string) bool {
	if !slices.Contains([]int{8, 12, 13, 14}, len(compact)) {
		return false
	}
	for _, ch := range compact {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return compact[len(compact)-1] == gtinCheckDigit(compact[:len(compact)-1])
}

func gtinCheckDigit(body string) byte {
	sum, weight := 0, 3
	for i := len(body) - 1; i >= 0; i-- {
		sum += int(body[i]-'0') * weight
		weight = 4 - weight
	}
	return byte('0' + (10-sum%10)%10)
}

// importerISBNBarcode converts a validated ISBN-10 to Bookland EAN-13 by
// dropping its check digit, prefixing 978, then computing a new check digit.
// A validated ISBN-13 is returned without separators.
func importerISBNBarcode(raw string) (string, bool) {
	compact, ok := compactBookIdentifier(raw)
	if !ok || !validISBN(compact) {
		return "", false
	}
	if len(compact) == 13 {
		return compact, true
	}
	body := "978" + compact[:9]
	return body + string(gtinCheckDigit(body)), true
}

func importerBarcodeError(raw string) error {
	compact, ok := compactBookIdentifier(raw)
	if !ok {
		// barcode is a dynamic text field, and a non-GTIN proprietary barcode
		// may legitimately contain other characters. Do not guess its scheme.
		return nil
	}
	if len(compact) == 10 {
		if !validISBN(compact) {
			return fmt.Errorf("invalid_isbn")
		}
		return nil
	}
	if slices.Contains([]int{8, 12, 13, 14}, len(compact)) && !validGTIN(compact) {
		return fmt.Errorf("invalid_barcode")
	}
	return nil
}
