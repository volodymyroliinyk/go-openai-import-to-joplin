# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project intends to use [Semantic Versioning](https://semver.org/spec/v2.0.0.html) for tagged releases.

## [Unreleased]

### Added

- Add a guarded GitHub release script that builds a Linux amd64 binary and a Debian amd64 package, maintains dated changelog sections, and publishes exactly those two assets.
- Require Conventional Commits and automatically include release commit subjects in the changelog.
- Import local Codex JSONL sessions with complete records, grouped into Joplin child notebooks by working directory.
- Use separate destination notebook settings and default state files for ChatGPT and Codex imports.
- Support `JOPLIN_CHATGPT_NOTEBOOK` and `JOPLIN_CODEX_NOTEBOOK`, while retaining `JOPLIN_NOTEBOOK` as a compatibility fallback.
- Add an MIT license.
- Warn when a ChatGPT export contains no project metadata.

### Changed

- Rename the project, CLI, installation paths, Go module, and release packages to `go-openai-import-to-joplin`.
- Translate repository documentation into English.
- Document Ubuntu as the currently tested platform; other Linux distributions, macOS, and Windows remain unverified.

### Security

- Reject insecure token-bearing HTTP connections to non-loopback Joplin hosts unless explicitly allowed.
- Validate export and state schemas, conflicting conversation data, duplicate identity markers, and unsafe project mappings before writes.
- Preserve tokens and private conversation content outside logs and committed configuration.
