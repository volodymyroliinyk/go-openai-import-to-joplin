# ChatGPT export source comparison

## Scope

This report compares two official ChatGPT exports from the same account:

- a Privacy Portal request generated on 2026-09-05;
- a **Settings → Data Controls → Export** archive generated on 2026-09-07.

The comparison inspected archive manifests, JSON structure, identifiers, and
content hashes. It did not copy conversation text, account data, or attachment
contents into the repository. Conclusions about the source are limited to these
two snapshots; the snapshots were generated two days apart.

## Results

| Property | Privacy Portal | Settings export |
| --- | ---: | ---: |
| `conversations-*.json` shards | 3 | 3 |
| Conversations | 278 | 284 |
| Binary `file_*.dat` entries | 138 | 139 |
| `projects.json` or `chatgpt_projects.json` | absent | absent |
| Conversations with `project_id`, `project_uuid`, or `project` | 0 | 0 |
| Successfully parsed by the current CLI | 278 | 284 |

The Settings snapshot contains all 278 conversation IDs in the Privacy
snapshot plus 6 additional IDs. This is consistent with the later export date,
so it does not establish that Settings exports are intrinsically more complete.

For the 278 shared conversations, a normalized comparison of title, current
node, mapping parent links, author roles, and message content found 278 exact
matches. The raw conversation objects are not byte-identical: the Privacy
snapshot has fields such as `async_status`, `context_scopes`,
`conversation_origin`, `disabled_tool_ids`, `gizmo_type`, and
`moderation_results`, while the Settings snapshot has fields such as
`is_read_only`, `is_study_mode`, `pinned_time`, and `plugin_ids`. These are
service metadata that the importer does not use to construct Joplin notes.
Both archives also contain the same importer-relevant core files and ancillary
account files; the Settings archive additionally contains `ads.json`.

## Decision

Keep one ChatGPT import mode for both official download paths. Separate
subcommands would select between two variants of the same conversation schema,
add user-facing complexity, and provide no additional fidelity. The existing
ZIP discovery already accepts the numbered shards used by both archives and
ignores unrelated account and binary files.

Do not give either source unconditional priority. Neither sample contains
project membership or project names, the Privacy sample contains more service
metadata, and the Settings sample is newer. Prefer the newest available export
in normal use. Importing an older snapshot after a newer one does not delete
notes that are absent from the older snapshot, but—as with every re-import—an
older differing version of a shared conversation can overwrite that note.

No parser or CLI change is required for the observed formats. A dry run of each
archive completed successfully and reported zero projects.
