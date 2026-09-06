# ChatGPT import to Joplin

Local CLI importer of a downloaded ChatGPT export into Joplin. Each invocation imports once and exits. Each chat becomes a Markdown note; project chats go into child notebooks, and other chats go into the selected root notebook. Re-running updates existing notes without duplicating them.

## Requirements

- [Go 1.27.1](https://go.dev/dl/) or newer to build; the compiled binary needs no Go or Python installation.
- Joplin Desktop running with Web Clipper enabled in **Tools → Options → Web Clipper**.
- A Web Clipper token and an existing destination notebook.
- A manually downloaded ChatGPT export: ZIP, unpacked directory, or `conversations.json`.

See [download instructions](docs/how-to-download-your-data-way-2.md). The CLI reads local files and uses the Joplin token. It does not authenticate with ChatGPT or download chats. Download a fresh export and run the command again to import newer conversations.

Keep exports, tokens, and personal `config.env` files out of Git.

## Quick start

```bash
./scripts/build.sh
./dist/chatgpt-import-to-joplin ~/Downloads/chatgpt-export.zip --dry-run
JOPLIN_TOKEN='...' JOPLIN_NOTEBOOK='ChatGPT Knowledge Base' \
./dist/chatgpt-import-to-joplin ~/Downloads/chatgpt-export.zip
```

The notebook name must be unique; use its ID when names are ambiguous. `--dry-run` parses the export without contacting Joplin or writing state.

## Install and update

```bash
./scripts/install.sh
"${XDG_DATA_HOME:-$HOME/.local/share}/chatgpt-import-to-joplin/bin/chatgpt-import-to-joplin" --help
./scripts/update.sh
```

The installer builds a standalone binary under `${XDG_DATA_HOME:-$HOME/.local/share}/chatgpt-import-to-joplin/bin`. Add that directory to your `PATH` or use the full path. Update rebuilds the current checkout. Neither script runs an import or downloads build dependencies.

When migrating from Python, run `./scripts/update.sh` and replace the old `venv/bin` path in your commands or `PATH` with `bin`. Existing configuration and state remain compatible. The old virtual environment is left in place; it is no longer used by these scripts.

## Optional configuration

The CLI accepts options and environment variables. It does not automatically load configuration files. The installer creates a shell configuration example under `${XDG_CONFIG_HOME:-$HOME/.config}/chatgpt-import-to-joplin` and preserves an existing `config.env`.

```bash
nano "${XDG_CONFIG_HOME:-$HOME/.config}/chatgpt-import-to-joplin/config.env"
source "${XDG_CONFIG_HOME:-$HOME/.config}/chatgpt-import-to-joplin/config.env"
"${XDG_DATA_HOME:-$HOME/.local/share}/chatgpt-import-to-joplin/bin/chatgpt-import-to-joplin" ~/Downloads/chatgpt-export.zip
```

Use [config/example.env](config/example.env) syntax: `export NAME='value'`. When upgrading, adapt preserved configuration to this format using `config.env.example`. Always pass the export path as the positional argument.

| Option | Default / environment variable |
| --- | --- |
| `--joplin-token` | `JOPLIN_TOKEN`; required for import |
| `--notebook` | `JOPLIN_NOTEBOOK`; destination ID or exact name, required for import |
| `--joplin-url` | `JOPLIN_URL`, otherwise `http://127.0.0.1:41184` |
| `--allow-insecure-http` | Explicitly allow HTTP to a non-loopback Joplin host |
| `--state` | `${XDG_STATE_HOME:-$HOME/.local/state}/chatgpt-import-to-joplin/state.json` |
| `--limit NAME=VALUE` | Override one resource budget; repeatable |
| `--dry-run` | Parse and report without importing |

Prefer the environment variable for the token to keep it out of command-line arguments. Summaries go to stdout; errors go to stderr. Exit codes: `0` success, `1` import error, `2` invalid arguments.

HTTPS is required when `JOPLIN_URL` points outside the local machine. Plain HTTP is accepted by default only for `localhost`, `127.0.0.0/8`, and `::1`. If a trusted private deployment cannot provide HTTPS, `--allow-insecure-http` enables the connection for that invocation and prints a warning because the token will cross the network without transport encryption.

## Import behavior and limitations

Only the exact first line `<!-- chatgpt-conversation-id: ID -->` identifies an imported note. Marker-like conversation text on later lines does not affect identity. Malformed first-line markers stop import. Duplicate conversation IDs across notes stop the import and report the conflicting note IDs. Discovery uses paginated Joplin basic search (`/search`, query `/"chatgpt-conversation-id:"`) across all notebooks, including moved and legacy imported notes. Only matching candidates are transferred; their first-line markers are validated locally. State is not trusted for note identity. Search errors stop import before writes, without falling back to downloading every note. Joplin still performs the text search internally, so server-side search time can grow with database size. See the [Data API](https://joplinapp.org/help/api/references/rest_api/#searching) and [basic search documentation](https://joplinapp.org/help/apps/search/).

The state file caches project notebook mappings. Cached folders must remain under the selected root with their saved title; unsafe mappings stop import before writes. Conflicting names for one project ID also stop the whole import before writes; otherwise each project produces at most one notebook creation per run. The importer never renames or moves existing notebooks. A project rename creates a new notebook and moves its imported notes there, leaving the old notebook intact. Retain it between imports to reuse project notebooks. Losing state does not duplicate chat notes, but can create new project notebooks. Deleted notes are recreated on the next import. Existing notes are updated only when their title, body, destination, source metadata, or supplied timestamps differ; identical notes are counted as unchanged. Local edits are overwritten from the export. Notes absent from a newer export are not deleted.

State stores only project mappings; legacy `notes` entries are ignored and removed on the next successful import. Each newly created project notebook is checkpointed atomically in `STATE_PATH.projects/` before importing its notes. Checkpoints are replayed on retry, including after note failures or failed final saves. The main state file is rewritten once after a successful pass, then checkpoint files are removed. Keep the checkpoint directory with the state file when recovering or moving an interrupted import. As with any separate API and filesystem writes, a crash after Joplin creates a folder but before its checkpoint is saved can leave an untracked folder; recover its mapping before retrying. Checkpoint write errors report the created folder ID and stop before note writes.

Joplin has no transaction spanning an import. If a request fails after earlier writes succeeded, the CLI prints `Failed after` counters and leaves those changes in place; fix the reported problem and rerun the same export and state path to resume safely.

Imports sharing one state path are serialized by an exclusive `STATE_PATH.lock` directory acquired before reading state or contacting Joplin. A competing invocation fails immediately. Normal completion, errors, and handled cancellation release the lock. After a crash or forced kill, confirm that no importer is active before removing the leftover lock directory with `rmdir`. State and project checkpoints are bound to the normalized Joplin endpoint and resolved root notebook ID; a mismatch stops before writes with an instruction to use another state path. Legacy state containing project mappings but no destination binding must be explicitly migrated after verifying its origin. Different state paths do not coordinate.

The parser supports `conversations.json`, numbered `conversations*.json`, the active message branch, and optional `projects.json`/`project_id` metadata. A missing node, cycle, or absent `current_node` in a non-empty message mapping rejects the whole export instead of importing a partial or mixed branch. Without project IDs, chats go into the root notebook. Projects without names use `ChatGPT project ID`. A JSON file argument imports that file only, with optional sibling project metadata. ZIP archives containing multiple export directories are rejected as ambiguous. Repeated, semantically identical conversation entries are deduplicated. If the same conversation ID has differing raw data anywhere in the export, the entire run is rejected with both file locations instead of choosing one by shard order or timestamp. Attachments remain structured snippets in Markdown; binary assets are not imported.

The whole export is validated before the importer opens the Joplin client or reads or writes state. A malformed export or exceeded resource budget rejects the run without skipping chats. `--dry-run` performs the same complete validation without credentials, network access, or state writes. The default budgets are:

| Limit name | Default | Scope |
| --- | ---: | --- |
| `json-bytes` | 1 GiB | Combined uncompressed JSON read |
| `file-bytes` | 256 MiB | Each JSON file or ZIP entry |
| `string-bytes` | 16 MiB | One encoded JSON string |
| `render-bytes` | 512 MiB | Combined rendered Markdown |
| `zip-index-bytes` | 64 MiB | ZIP central-directory reads |
| `entries` | 100,000 | Directory or ZIP entries |
| `json-values` | 2,000,000 | JSON values across input files |
| `depth` | 256 | JSON nesting depth |
| `conversations` | 100,000 | Conversations across input files |
| `messages` | 100,000 | Message nodes in one conversation |
| `parts` | 100,000 | Content parts in one message |
| `id-bytes` | 4 KiB | One conversation or project ID |
| `title-bytes` | 64 KiB | One conversation or project title |

Override a budget only when a trusted, valid export needs it, for example `--limit json-bytes=2GiB --limit conversations=150000`. Byte limits accept integer bytes or the exact `KiB`, `MiB`, and `GiB` suffixes. Errors name the exceeded budget and instruct how to retry the whole export.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/chatgpt-import-to-joplin --help
for file in scripts/*.sh config/example.env; do bash -n "$file" || break; done
./scripts/build.sh # standalone binary in dist/
git diff --check
```

AI contributors: read [AGENTS.md](AGENTS.md) for the code map, invariants, and focused checks. Current scope: [task.md](task.md).
