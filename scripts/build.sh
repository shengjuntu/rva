#!/usr/bin/env bash
set -euo pipefail
app_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$app_dir"
mkdir -p bin
go test ./...
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o bin/video-app .
