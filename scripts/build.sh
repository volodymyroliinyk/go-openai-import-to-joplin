#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_dir"
command -v go >/dev/null || { echo "Install Go 1.27.1 or newer from https://go.dev/dl/" >&2; exit 1; }
mkdir -p dist
CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -o dist/go-openai-import-to-joplin ./cmd/go-openai-import-to-joplin
