# AI contributor guide

## Scope

Go 1.27.1+ CLI: manually downloaded ChatGPT export or local Codex session JSONL → local Joplin Web Clipper API. Each invocation imports once and exits. Keep this project CLI-only: no background service, scheduler, web UI, or OpenAI authentication. Use only the Go standard library; build a standalone binary with CGO disabled.

## Read only what the task needs

- `cmd/openai-import-to-joplin/main.go`: signal-aware CLI entry point.
- `internal/importer/cli.go`: arguments, environment defaults, dry-run, exit codes.
- `internal/importer/export.go`: ZIP/directory/JSON, branches, project metadata, Markdown.
- `internal/importer/codex.go`: local Codex JSONL discovery, project grouping, and lossless record rendering.
- `internal/importer/joplin.go`: HTTP client, pagination, global basic marker search, note/folder operations.
- `internal/importer/sync.go`: authoritative marker index, change detection, synchronization.
- `internal/importer/state.go`: project-only state, atomic per-project checkpoints, replay and final compaction.
- `internal/importer/lock.go`: exclusive state-path lock spanning state reads, Joplin access, and saves; abandoned locks require manual recovery.
- `internal/importer/*_test.go`: parser, fake-client sync, local HTTP and CLI tests.
- `go.mod`: minimum stable Go toolchain; no third-party dependencies.
- `scripts/{build,install,update}.sh`: packaging and per-user CLI installation.
- `config/example.env`: optional shell exports; CLI does not load it automatically.
- `README.md`: user commands and behavior; `CHANGELOG.md`: notable user-facing changes; `task.md`: scope in Ukrainian.
- `docs/how-to-download-your-data-way-2.md`: manual export help, unrelated to importer internals.

Start with `git status --short` and targeted `rg`/file reads. Preserve staged and unstaged work. Avoid reading private exports/config, build outputs, virtual environments, or the entire repository when a focused read suffices. Ordinary local refactoring needs no external documentation lookup. Keep responses concise: changes, verification, actual limitations.

## Invariants

- `<!-- chatgpt-conversation-id: ID -->` identifies imported notes; Joplin is authoritative for note identity. Duplicate markers across notes stop import. Discover candidates with global basic search; do not filter by source or destination, or trust state IDs in place of markers.
- Source-specific state files cache project notebook IDs separately for ChatGPT and Codex. Checkpoint newly created folders before note writes; compact once on success and only then clear checkpoints. Retain state and pending `STATE_PATH.projects/` checkpoints to reuse folders. Missing state must not duplicate notes, but can recreate project notebooks.
- Re-import updates differing notes, overwriting local edits; identical notes must not issue PUT requests. Notes absent from the export are not deleted.
- Source is a required positional path. Dry-run needs no credentials and must avoid network and state writes.
- Codex mode reads only explicitly supplied JSONL files/directories, skips symlinks, and never reads Codex credentials. Preserve every valid JSONL record in the note; do not silently filter new event types.
- Preserve ZIP, directory, JSON, and optional project metadata support. Do not assume every export includes projects. Binary attachments are not imported.
- Never log tokens or commit user exports/configuration. Use fake clients for verification, not live Joplin accounts.
- Installer preserves existing config; install/update do not run imports. Shell configuration values must be quoted and exported.

## Verification

Run from the repository root without installation or network:

```bash
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/openai-import-to-joplin --help
for file in scripts/*.sh config/example.env; do bash -n "$file" || break; done
git diff --check
```

Use `go test ./internal/importer -run TestSync -v` (or `TestLoad`) for focused tests. Add tests for substantive behavior changes. Packaging checks: `./scripts/build.sh`; test install/update with temporary XDG directories, never the user's actual configuration. No network or installation is needed once Go is installed. Build scripts set `GOTOOLCHAIN=local` to avoid implicit toolchain downloads.

Update README when commands/configuration change, and this guide when architecture/checks change. Use Conventional Commits for every commit: `type(scope)!: description`, with the scope optional. Allowed types are `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`, and `revert`; use `!` for a breaking change. Keep the description concise, imperative, and in English. Merge commits are exempt. Every commit containing a notable user-facing change must also update the appropriate section under `CHANGELOG.md` in the same commit. Keep entries concise, written in English, and grouped according to Keep a Changelog. Documentation-only, test-only, formatting, and internal maintenance commits may omit a curated changelog entry when they do not affect users. `scripts/release.sh changelog VERSION` adds the Conventional Commit subjects since the previous release under `Included commits` and moves `Unreleased` into a dated semantic-version section; after the first release tag, it rejects non-compliant commit subjects. Avoid new dependencies or unrelated refactors for small tasks.
