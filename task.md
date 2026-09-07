# Project task

A local CLI application that imports a manually downloaded ChatGPT archive or local Codex session transcripts into a Joplin knowledge base. Each invocation performs one import and exits.

## Inputs

1. A path to a ChatGPT ZIP archive, unpacked directory, `conversations.json`, Codex session JSONL file, or Codex sessions directory.
2. A Joplin Web Clipper API token through `JOPLIN_TOKEN` or `--joplin-token`.
3. Separate destination notebook IDs or exact names through `JOPLIN_CHATGPT_NOTEBOOK` and `JOPLIN_CODEX_NOTEBOOK`, with `--notebook` as an invocation override and `JOPLIN_NOTEBOOK` as a compatibility fallback.
4. Optional Joplin URL, state path, and `--dry-run` mode.
5. `--codex` when the source contains local Codex JSONL sessions.

## Behavior

- Adds new chats and projects with their nested chats, and updates existing chats when their content differs, using stable conversation IDs.
- Places project chats in child notebooks and all other chats in the destination notebook.
- Stores ChatGPT and Codex project-to-notebook mappings in separate local state files.
- Does not delete notes that are absent from a later export.
- Reports a summary or error through stdout/stderr and the exit code.
- Preserves complete Codex JSONL records, including messages, reasoning, tool activity, usage, state, and unknown event types.

## Project composition

- Implementation language: the latest stable Go release.
- CLI, export parser, Joplin client, and import logic.
- Build, installation, and update scripts.
- Automated tests, an example shell configuration, user documentation, and AI contributor guidance.

The code must be safe, tested, extensible, performant, and reliable, following established Go engineering practices.
