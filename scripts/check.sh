#!/bin/sh
# Runs the Definition-of-done checks of CLAUDE.md. Stops at the first failure.
set -eu
cd "$(dirname "$0")/.."

# On macOS the temporary folder is under /var, a link to /private/var; tests
# compare paths that the program resolves, so give them the resolved one.
if [ "$(uname -s)" = Darwin ]; then
	TMPDIR=$(cd "${TMPDIR:-/tmp}" && pwd -P)/
	export TMPDIR
fi

step() { printf '\n== %s\n' "$*"; }

step gofmt
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "not formatted:"; echo "$unformatted"; exit 1
fi

step go vet
go vet ./...

step "go vet (linux)"
GOOS=linux GOARCH=amd64 go vet ./...

step golangci-lint
golangci-lint run

step "go test -race -tags debug_mode"
go test -race -tags debug_mode ./...

step "go test"
go test ./...

if [ -d rdp ]; then
	step "the library on linux"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./rdp/...
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./rdp/...
fi

printf '\nall checks passed\n'
