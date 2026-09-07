# Project task

A local CLI application that imports a manually downloaded ChatGPT archive into a Joplin knowledge base. The user obtains the archive manually and runs the command when needed. Each invocation performs one import and exits.

## Inputs

1. A path to a ZIP archive, unpacked directory, or `conversations.json`.
2. A Joplin Web Clipper API token through `JOPLIN_TOKEN` or `--joplin-token`.
3. The ID or exact name of the destination notebook through `JOPLIN_NOTEBOOK` or `--notebook`.
4. Optional Joplin URL, state path, and `--dry-run` mode.

## Behavior

- Adds new chats and projects with their nested chats, and updates existing chats when their content differs, using stable conversation IDs.
- Places project chats in child notebooks and all other chats in the destination notebook.
- Stores project-to-notebook mappings in a local state file.
- Does not delete notes that are absent from a later export.
- Reports a summary or error through stdout/stderr and the exit code.

## Project composition

- Implementation language: the latest stable Go release.
- CLI, export parser, Joplin client, and import logic.
- Build, installation, and update scripts.
- Automated tests, an example shell configuration, user documentation, and AI contributor guidance.

The code must be safe, tested, extensible, performant, and reliable, following established Go engineering practices.
