#!/bin/sh
set -eu

mkdir -p dist/helpers
for arch in amd64 arm64; do
	GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "dist/helpers/aact-vault-handoff-linux-$arch" ./cmd/aact-vault-handoff
done
