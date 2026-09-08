#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_dir"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/release.sh changelog VERSION
  ./scripts/release.sh package VERSION
  ./scripts/release.sh publish VERSION

VERSION must be a stable semantic version such as 1.2.3 (without a leading v).

  changelog  Move the curated Unreleased entries to a dated VERSION section.
  package    Build the Linux amd64 binary and Debian amd64 package locally.
  publish    Verify main, run all checks, tag the commit, and create a GitHub release.
EOF
}

die() {
  printf 'release: %s\n' "$*" >&2
  exit 1
}

validate_version() {
  version="${1:-}"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "VERSION must have the form 1.2.3 (without a leading v)"
}

require_command() {
  command -v "$1" >/dev/null || die "required command not found: $1"
}

prepare_changelog() {
  local release_date temporary
  release_date="${RELEASE_DATE:-$(date -u +%F)}"
  [[ "$release_date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "RELEASE_DATE must have the form YYYY-MM-DD"
  ! grep -Fq "## [$version]" CHANGELOG.md || die "CHANGELOG.md already contains version $version"
  awk '
    /^## \[Unreleased\]$/ { unreleased=1; next }
    unreleased && /^## \[/ { exit }
    unreleased && /^- / { entries=1 }
    END { exit(entries ? 0 : 1) }
  ' CHANGELOG.md || die "the Unreleased section has no entries"

  temporary="$(mktemp "$project_dir/.CHANGELOG.md.XXXXXX")"
  trap 'rm -f "$temporary"' EXIT
  awk -v heading="## [$version] - $release_date" '
    /^## \[Unreleased\]$/ {
      print
      print ""
      print heading
      next
    }
    { print }
  ' CHANGELOG.md >"$temporary"
  mv "$temporary" CHANGELOG.md
  trap - EXIT
  printf 'Prepared CHANGELOG.md for %s. Review and commit it before publishing.\n' "$version"
}

package_release() {
  local output_dir binary_name deb_name staging
  require_command go
  require_command dpkg-deb

  output_dir="$project_dir/dist/release/v$version"
  binary_name="openai-import-to-joplin_${version}_linux_amd64"
  deb_name="openai-import-to-joplin_${version}_amd64.deb"
  mkdir -p "$output_dir"

  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local \
    go build -trimpath -buildvcs=true -ldflags='-s -w' \
    -o "$output_dir/$binary_name" ./cmd/openai-import-to-joplin

  staging="$(mktemp -d "${TMPDIR:-/tmp}/openai-import-to-joplin-deb.XXXXXX")"
  trap 'rm -rf "$staging"' EXIT
  install -Dm755 "$output_dir/$binary_name" "$staging/usr/bin/openai-import-to-joplin"
  mkdir -p "$staging/DEBIAN"
  sed "s/@VERSION@/$version/" packaging/debian/control >"$staging/DEBIAN/control"
  dpkg-deb --root-owner-group --build "$staging" "$output_dir/$deb_name" >/dev/null
  rm -rf "$staging"
  trap - EXIT

  printf '%s\n%s\n' "$output_dir/$binary_name" "$output_dir/$deb_name"
}

release_notes() {
  awk -v version="$version" '
    $0 ~ "^## \\[" version "\\] - " { found=1; next }
    found && /^## \[/ { exit }
    found { print }
  ' CHANGELOG.md
}

publish_release() {
  local branch remote output_dir binary deb notes
  require_command git
  require_command gh

  branch="$(git branch --show-current)"
  [[ "$branch" == main ]] || die "publishing is allowed only from main (current branch: ${branch:-detached HEAD})"
  [[ -z "$(git status --porcelain=v1)" ]] || die "working tree must be clean"
  grep -Eq "^## \[$version\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md || \
    die "prepare and commit CHANGELOG.md first: ./scripts/release.sh changelog $version"

  remote="${RELEASE_REMOTE:-origin}"
  git remote get-url "$remote" >/dev/null 2>&1 || die "Git remote '$remote' does not exist"
  git fetch "$remote" main
  [[ "$(git rev-parse HEAD)" == "$(git rev-parse "$remote/main")" ]] || \
    die "local main must exactly match $remote/main"
  ! git rev-parse -q --verify "refs/tags/v$version" >/dev/null || die "tag v$version already exists locally"
  [[ -z "$(git ls-remote --tags "$remote" "refs/tags/v$version")" ]] || die "tag v$version already exists on $remote"
  gh auth status >/dev/null

  go test ./...
  go test -race ./...
  go vet ./...
  go run ./cmd/openai-import-to-joplin --help >/dev/null
  for file in scripts/*.sh config/example.env; do bash -n "$file" || exit; done
  git diff --check

  package_release
  output_dir="$project_dir/dist/release/v$version"
  binary="$output_dir/openai-import-to-joplin_${version}_linux_amd64"
  deb="$output_dir/openai-import-to-joplin_${version}_amd64.deb"
  notes="$(release_notes)"
  [[ -n "${notes//[[:space:]]/}" ]] || die "the $version changelog section is empty"

  git tag -a "v$version" -m "Release v$version"
  git push "$remote" "refs/tags/v$version"
  gh release create "v$version" "$binary" "$deb" \
    --verify-tag --title "v$version" --notes "$notes"
}

command_name="${1:-}"
case "$command_name" in
  changelog|package|publish)
    validate_version "${2:-}"
    [[ $# -eq 2 ]] || die "unexpected arguments"
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

case "$command_name" in
  changelog) prepare_changelog ;;
  package) package_release ;;
  publish) publish_release ;;
esac
