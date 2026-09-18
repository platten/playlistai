package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestParseByteSizeAndWorkers(t *testing.T) {
	for input, want := range map[string]int64{"2GiB": 2 << 30, "512MiB": 512 << 20, "100MB": 100_000_000, "4096": 4096} {
		got, err := parseByteSize(input)
		if err != nil || got != want {
			t.Fatalf("parseByteSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	if workers, err := parseWorkers("auto"); err != nil || workers != 0 {
		t.Fatalf("auto workers = %d, %v", workers, err)
	}
	if workers, err := parseWorkers("3"); err != nil || workers != 3 {
		t.Fatalf("explicit workers = %d, %v", workers, err)
	}
	for _, invalid := range []string{"", "0", "-1", "many"} {
		if _, err := parseWorkers(invalid); err == nil {
			t.Fatalf("parseWorkers(%q) succeeded", invalid)
		}
	}
}

func TestExecuteRequiresOutputAndTwoInputs(t *testing.T) {
	for _, args := range [][]string{{"one", "two"}, {"--out", "merged.paipack", "one"}} {
		var stdout, stderr bytes.Buffer
		code, err := execute(context.Background(), args, &stdout, &stderr)
		if code != 2 || err == nil || !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("execute(%v) = code %d, err %v, stderr %q", args, code, err, stderr.String())
		}
	}
}
