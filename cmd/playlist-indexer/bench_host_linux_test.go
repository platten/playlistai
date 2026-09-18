//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestParseBenchmarkARCSnapshot(t *testing.T) {
	raw := "13 1 0x01 86 720 512\nname type data\nsize 4 12345\nhits 4 678\nmisses 4 9\nbad 4 nope\n"
	got := parseBenchmarkARCSnapshot(strings.NewReader(raw))
	if got != (benchmarkARCSnapshot{SizeBytes: 12345, Hits: 678, Misses: 9}) {
		t.Fatalf("ARC snapshot=%+v", got)
	}
}

func TestFilesystemTypeName(t *testing.T) {
	if got := filesystemTypeName(0x2fc12fc1); got != "zfs" {
		t.Fatalf("ZFS type=%q", got)
	}
	if got := filesystemTypeName(123); got != "0x7b" {
		t.Fatalf("unknown type=%q", got)
	}
}
