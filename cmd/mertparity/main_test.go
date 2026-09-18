package main

import (
	"testing"
)

func TestWorkerFlagMatchesManagedMERTWorkerProtocol(t *testing.T) {
	if bundle, ok := mertWorkerInvocation([]string{"--mert-worker", "/prepared/bundle"}); !ok || bundle != "/prepared/bundle" {
		t.Fatalf("managed worker invocation=%q,%t", bundle, ok)
	}
	if bundle, ok := mertWorkerInvocation([]string{"--bundle", "/prepared/bundle"}); ok || bundle != "" {
		t.Fatalf("legacy worker invocation=%q,%t", bundle, ok)
	}
}
