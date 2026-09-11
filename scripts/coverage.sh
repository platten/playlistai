#!/usr/bin/env bash
# Honest application coverage gate; reports remain available after failure.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
mkdir -p coverage
failed=0
go test -count=1 -coverpkg=./... -covermode=atomic -coverprofile=coverage/backend.out ./... || failed=1
go tool cover -html=coverage/backend.out -o coverage/backend.html || failed=1
go run ./cmd/coveragecheck -profile coverage/backend.out || failed=1
(cd frontend && pnpm run test:coverage) || failed=1
exit "$failed"
