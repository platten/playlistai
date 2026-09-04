package catalog

import (
	"reflect"
	"testing"
)

func TestNormalizeSearch(t *testing.T) {
	t.Parallel()
	// Expected values mirror python/catalogfmt.py:normalize_search.
	cases := []struct{ in, want string }{
		{"Justice", "justice"},
		{"Justice - Genesis", "justice genesis"},
		{"Sigur Rós", "sigur ros"},
		{"Björk", "bjork"},
		{"Jóga", "joga"},
		{"D.A.N.C.E. - Radio Edit", "d a n c e radio edit"},
		{"  leading / and  trailing  ", "leading and trailing"},
		{"Beyoncé & Jay‑Z", "beyonce jay z"},
		{"Motörhead", "motorhead"},
		{"AC/DC", "ac dc"},
		{"Émigré", "emigre"},
		{"İstanbul", "istanbul"},
		{"", ""},
		{"---", ""},
		{"90's Mix vol. 2", "90 s mix vol 2"},
	}
	for _, tc := range cases {
		if got := normalizeSearch(tc.in); got != tc.want {
			t.Errorf("normalizeSearch(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTokenize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"daft punk", []string{"daft", "punk"}},
		{"  Sigur Rós  ", []string{"sigur", "ros"}},
		{"%%%", nil},
		{"", nil},
		{"one", []string{"one"}},
	}
	for _, tc := range cases {
		got := tokenize(tc.in)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
