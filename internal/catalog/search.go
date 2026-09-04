package catalog

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalizeSearch folds a string to the catalog's canonical search form. It is a
// byte-for-byte port of python/catalogfmt.py:normalize_search — keep the two in
// step:
//
//	NFKD → drop Unicode category Mn → lowercase → each run of non-[a-z0-9]
//	becomes a single space → trim.
//
// Non-Latin scripts are stripped rather than transliterated.
func normalizeSearch(s string) string {
	d := norm.NFKD.String(s)

	var b strings.Builder
	b.Grow(len(d))
	pendingSpace := false
	wrote := false

	for _, r := range d {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingSpace && wrote {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			wrote = true
			pendingSpace = false
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

// tokenize splits a user query into search tokens (normalized, whitespace-split).
func tokenize(query string) []string {
	return strings.Fields(normalizeSearch(query))
}
