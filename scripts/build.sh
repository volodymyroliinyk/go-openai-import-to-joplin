#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_dir"

version="${1:-0.0.0-dev}"
[[ $# -le 1 ]] || { echo "Usage: ./scripts/build.sh [VERSION]" >&2; exit 2; }
if [[ "$version" != "0.0.0-dev" && ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "build: VERSION must have the form 1.2.3 (without a leading v)" >&2
  exit 2
fi

command -v go >/dev/null || { echo "Install Go 1.27.1 or newer from https://go.dev/dl/" >&2; exit 1; }
command -v dpkg-deb >/dev/null || { echo "Install dpkg-deb to build the Debian package" >&2; exit 1; }

output_dir="${BUILD_OUTPUT_DIR:-$project_dir/dist/build/v$version}"
binary_name="go-openai-import-to-joplin_${version}_linux_amd64"
deb_name="go-openai-import-to-joplin_${version}_amd64.deb"
mkdir -p "$output_dir"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local \
  go build -trimpath -buildvcs=true -ldflags='-s -w' \
  -o "$output_dir/$binary_name" ./cmd/go-openai-import-to-joplin

staging="$(mktemp -d "${TMPDIR:-/tmp}/go-openai-import-to-joplin-deb.XXXXXX")"
trap 'rm -rf "$staging"' EXIT
install -Dm755 "$output_dir/$binary_name" "$staging/usr/bin/go-openai-import-to-joplin"
mkdir -p "$staging/DEBIAN"
sed "s/@VERSION@/$version/" packaging/debian/control >"$staging/DEBIAN/control"
dpkg-deb --root-owner-group --build "$staging" "$output_dir/$deb_name" >/dev/null
rm -rf "$staging"
trap - EXIT

printf '%s\n%s\n' "$output_dir/$binary_name" "$output_dir/$deb_name"
