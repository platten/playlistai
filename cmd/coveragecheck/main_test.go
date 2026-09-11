package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverage(t *testing.T) {
	for _, tc := range []struct {
		input          string
		covered, total uint64
		invalid        bool
	}{
		{"mode: set\na:1.1,2.1 19 1\na:3.1,4.1 1 0\n", 19, 20, false},
		{"mode: atomic\na:1.1,2.1 2 50\n", 2, 2, false},
		{"mode: count\na:1.1,2.1 2 0\n", 0, 2, false},
		{"", 0, 0, true}, {"mode: x", 0, 0, true}, {"mode: set\n", 0, 0, true},
		{"mode: set\nbad\n", 0, 0, true},
		{"mode: set\na 1 1\na 1 0\n", 1, 1, false},
		{"mode: atomic\na 2 0\nb 1 0\na 2 8\na 2 5\n", 2, 3, false},
		{"mode: atomic\na 1 1\na 2 1\n", 0, 0, true},
		{"mode: set\na -1 1\n", 0, 0, true},
		{"mode: set\na 1 nope\n", 0, 0, true},
		{"mode: set\na 18446744073709551615 0\nb 1 0\n", 0, 0, true},
		{"mode: set\n" + strings.Repeat("a", 70000), 0, 0, true},
	} {
		covered, total, err := coverage(strings.NewReader(tc.input))
		if (err != nil) != tc.invalid || covered != tc.covered || total != tc.total {
			t.Fatalf("coverage: got %d/%d, %v; expected %d/%d invalid=%v", covered, total, err, tc.covered, tc.total, tc.invalid)
		}
	}
}

func TestThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(path, []byte("mode: set\na 18999 1\nb 1001 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args    []string
		invalid bool
	}{
		{[]string{"-profile", path}, true}, // 94.995 must not round up to a pass.
		{[]string{"-profile", path, "-minimum", "94"}, false},
		{[]string{"-profile", path, "-minimum", "0"}, false},
		{[]string{"-minimum", "NaN"}, true}, {[]string{"-minimum", "+Inf"}, true},
		{[]string{"-minimum", "101"}, true}, {[]string{"-minimum", "-1"}, true},
		{[]string{"-bad"}, true}, {[]string{"unexpected"}, true},
		{[]string{"-profile", path + ".missing"}, true},
	} {
		if err := run(tc.args, &bytes.Buffer{}); (err != nil) != tc.invalid {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
	if err := os.WriteFile(path, []byte("mode: set\na 19 1\nb 1 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-profile", path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-profile", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid profile passed")
	}
}
