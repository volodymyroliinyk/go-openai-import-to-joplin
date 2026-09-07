# Business logic, reliability, and security audit

Audit date: 2026-09-05<br>
Scope: the Go CLI implementation in `internal/importer`, its entry point, installation scripts, and tests.<br>
Method: static analysis of the `Load → Synchronize → Joplin API` flow, including note identity, repeat imports, project notebooks, partial failures, and untrusted local input.

## Summary

The implementation is compact and has useful safety properties: atomic state replacement, rejection of duplicate marker IDs across notes, blocked HTTP redirects, token redaction in errors, and an offline dry-run. The original audit identified high-priority risks involving marker injection, unsafe state mappings, concurrent runs, full-database scans, and repeated state rewrites. All findings below were addressed on 2026-09-05; their original risks are retained as historical context.

## Priority scale

- **P0** — a direct risk of uncontrolled data corruption; fix before broad use.
- **P1** — a high probability of correctness, confidentiality, or availability failures with real data.
- **P2** — a material reliability, compatibility, or diagnostics issue.
- **P3** — hardening, maintainability, or UX work that does not block the basic workflow.

## Findings

### BL-01 — Conversation content could forge an identity marker (P0) — ✅ Complete

**Status:** completed in commit `3be5751`. Only an exact marker on the first line establishes identity; marker-like content below it does not affect the index. Regression tests cover injected and multiple markers.

**Original risk:** arbitrary user or assistant text containing `<!-- chatgpt-conversation-id: victim -->` could associate multiple IDs with one note and cause the wrong note to be overwritten.

**Acceptance criterion:** content after the service marker cannot affect identity, and one Joplin note cannot represent multiple conversation IDs.

### BL-02 — State could claim an unrelated notebook (P0) — ✅ Complete

**Status:** completed in commit `a824c78`. Cached folder IDs, parents, and saved titles are validated before writes. Existing unrelated notebooks are never renamed or moved; a changed project name creates a new managed notebook.

**Original risk:** corrupt or edited state could point `joplin_id` at the root or an unrelated folder, allowing the importer to rename or move it.

**Acceptance criterion:** replacing a project mapping in state cannot mutate an unrelated folder.

### BL-03 — Concurrent imports were not locked (P1) — ✅ Complete

**Status:** completed in commit `e6cd24b`. An exclusive state-path lock spans state reads, Joplin access, and saves. A competing invocation fails immediately. Tests cover cross-process locking and lock release after errors and cancellation; manual crash recovery is documented.

**Original risk:** concurrent processes could observe the same snapshots, create duplicate notes or folders, and overwrite each other's state.

**Acceptance criterion:** two runs sharing a state path cannot perform concurrent Joplin writes.

### BL-04 — Loading every Joplin note was the primary bottleneck (P1) — ✅ Complete

**Status:** global paginated basic search for `chatgpt-conversation-id:` now discovers candidates. Only candidates are fetched and their first lines are validated locally. Search errors stop the import before writes. HTTP tests cover pagination, failures, and a database with 10,000 unrelated notes.

**Original risk:** importing a small export required downloading every note body, causing runtime, memory use, and API traffic to scale with the whole Joplin database.

**Acceptance criterion:** normal repeat imports scale with matching candidates while preserving global marker discovery.

### BL-05 — State was fully encoded and synchronized after every chat (P1) — ✅ Complete

**Status:** the note cache was removed. Each newly created project folder receives an atomic checkpoint before note writes; the main state is compacted once after a successful pass, then checkpoints are removed. Replay preserves mappings after failures or process exit.

**Original risk:** repeated full rewrites produced quadratic I/O and left difficult crash windows. The unavoidable gap between a successful Joplin folder POST and its local checkpoint is documented for manual recovery.

**Acceptance criterion:** full state rewrites do not grow linearly with chat count, and a checkpointed folder is reused after retry.

### BL-06 — Untrusted exports had no resource limits (P1) — ✅ Complete

**Status:** limits cover aggregate JSON, individual files, ZIP indexes and entries, JSON depth, values and strings, conversations, messages, parts, IDs, titles, and rendered Markdown. ZIP metadata is checked before payload reads; actual decompressed bytes are guarded. `--limit NAME=VALUE` provides explicit overrides.

**Original risk:** malformed or adversarial files could exhaust memory, disk, or CPU before reaching Joplin.

**Acceptance criterion:** budget violations return controlled errors before Joplin or state access.

### BL-07 — Tokens could be sent over plain HTTP to remote hosts (P1) — ✅ Complete

**Status:** plain HTTP is allowed by default only for `localhost`, `127.0.0.0/8`, and `::1`. Remote endpoints require HTTPS unless the user explicitly supplies `--allow-insecure-http`, which emits a redacted warning. The policy is checked before locking state or making a request.

**Original risk:** a mistyped remote HTTP URL could expose the Web Clipper token in transit.

**Acceptance criterion:** a remote plain-HTTP endpoint cannot receive the token without explicit user consent.

### BL-08 — Duplicate conversation IDs silently overwrote each other (P2) — ✅ Complete

**Status:** semantically equivalent raw JSON entries are deduplicated regardless of field order. Conflicting entries reject the whole export before writes and report both source locations. Tests cover object and array containers, directories, ZIP shards, lexical shard ordering, and direct JSON input.

**Original risk:** map assignment made the selected conversation depend on shard order or an untrusted timestamp.

**Acceptance criterion:** conflicting duplicates never disappear silently or depend on incidental lexical ordering.

### BL-09 — A corrupt active branch could be imported partially (P2) — ✅ Complete

**Status:** the active branch must terminate at a root node with an empty parent. Missing or invalid nodes, cycles, and absent `current_node` values in non-empty mappings reject the full export before writes. The unsafe fallback that mixed branches was removed.

**Original risk:** a successful run could overwrite a complete note with a truncated or mixed conversation.

**Acceptance criterion:** no existing note is replaced with a partial branch without an explicit policy and diagnostic.

### BL-10 — Partial success was not reported and preflight was incomplete (P2) — ✅ Complete

**Status:** export validity, marker identity, input IDs, project consistency, and cached folder ownership are checked before the first mutation. Failures after a successful mutation return a typed partial result; the CLI prints counters, states that changes were not rolled back, and recommends a marker-based retry.

**Original risk:** users could interpret a failed import as having made no changes even though earlier Joplin writes remained.

**Acceptance criterion:** every post-write failure clearly reports partial completion and safe recovery steps.

### BL-11 — Conflicting project metadata caused repeated PUTs and unstable names (P2) — ✅ Complete

**Status:** chats are aggregated by project ID before writes and assigned one canonical name, including the documented fallback. Conflicting names reject the full import; identical names create at most one folder. State records the actual canonical title.

**Original risk:** one project could be renamed repeatedly, with the final name determined by sort order.

**Acceptance criterion:** one project ID produces at most one folder mutation per run and has a deterministic name.

### BL-12 — State was not bound to a Joplin profile and destination (P2) — ✅ Complete

**Status:** state and project checkpoints are bound to a normalized Joplin endpoint and resolved root notebook ID. A mismatch stops before marker search or mutations and instructs the user to choose the correct state path. Legacy mappings without a binding require explicit migration.

**Original risk:** folder mappings from one profile or root could accidentally apply to another destination.

**Acceptance criterion:** state from one destination cannot be silently applied to another.

### BL-13 — State lacked explicit schema-version validation (P3) — ✅ Complete

**Status:** state and checkpoints use schema version 1. The loader rejects unsupported versions, incomplete destination bindings, and empty project or folder IDs. Version 0 is migrated explicitly; legacy note entries are removed during compaction. Unknown fields remain ignored for backward compatibility.

**Original risk:** future or partially invalid state could be misinterpreted silently.

### BL-14 — Export errors lacked actionable source coordinates (P3) — ✅ Complete

**Status:** project and conversation errors include filenames and available conversation, node, and part coordinates without printing private content. Empty conversation IDs and unsupported scalar parts reject the entire export; object parts are preserved as JSON.

**Original risk:** users could not locate invalid records in large exports, while some invalid content was silently skipped.

### BL-15 — Critical recovery and security paths lacked end-to-end tests (P3) — ✅ Complete

**Status:** regression and integration tests cover marker injection, secondary IDs in content, duplicate markers, unsafe folder mappings, cross-process locks, path aliases, process exit after checkpoints, replay after compaction failure, ambiguous note writes, resource limits, remote HTTP policy, and partial-result reporting. Tests use fake clients, subprocesses, and local `httptest` servers—never live Joplin accounts or private exports.

## Recommended architecture

Joplin remains authoritative for note identity through exact markers. State contains only versioned, destination-bound ownership mappings for project folders. A future state-assisted fast path may use note IDs for targeted GET requests, but each result must verify its marker and fall back to reconciliation on mismatch. State must never become the sole source of note identity.

## Properties to preserve

- Dry-run requires no credentials, network, or state writes.
- Notes absent from an export are not deleted.
- Local edits are overwritten only when the desired imported note differs.
- Duplicate markers stop the import before mutations.
- State uses temporary files, `fsync`, and atomic rename.
- Redirects are blocked, response bodies are excluded from errors, and tokens are redacted.
- ZIP entries are read in place, and path traversal names are rejected.
- The standard-library-only, CGO-disabled, one-shot CLI design keeps the attack surface small.

## Audit limitations

- No live Joplin profile or private ChatGPT export was used.
- No large-database performance benchmark was run; complexity estimates follow from control and data flow.
- Joplin API behavior outside the implemented contract was not tested.
- This was a snapshot audit of a worktree that already contained unrelated staged or unstaged changes.
