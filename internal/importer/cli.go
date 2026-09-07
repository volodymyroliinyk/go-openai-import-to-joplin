package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usage = `Usage: chatgpt-import-to-joplin SOURCE [options]

Import a ChatGPT ZIP, directory, or conversations.json into Joplin.
  --joplin-token TOKEN  Web Clipper token (JOPLIN_TOKEN)
  --notebook NAME_OR_ID Destination notebook (JOPLIN_NOTEBOOK)
  --joplin-url URL      API URL (JOPLIN_URL; default http://127.0.0.1:41184)
  --allow-insecure-http Allow token-bearing HTTP requests to a non-loopback host
  --state PATH          State file (default under XDG_STATE_HOME or ~/.local/state)
  --limit NAME=VALUE    Override a resource budget; repeatable (byte units: KiB/MiB/GiB)
  --dry-run             Validate the whole export without network or state writes
  -h, --help            Show help
`

// Run accepts options before or after the required source, matching the original CLI.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	token, notebook, base := getenv("JOPLIN_TOKEN"), getenv("JOPLIN_NOTEBOOK"), getenv("JOPLIN_URL")
	if base == "" {
		base = "http://127.0.0.1:41184"
	}
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	stateRoot := getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		stateRoot = filepath.Join(home, ".local", "state")
	}
	statePath := filepath.Join(stateRoot, "chatgpt-import-to-joplin", "state.json")
	source := ""
	dry := false
	allowInsecureHTTP := false
	limits := DefaultLimits()
	positional := false
	invalid := func(s string) int { fmt.Fprintln(stderr, "error:", s); fmt.Fprint(stderr, usage); return 2 }
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positional && arg == "--" {
			positional = true
			continue
		}
		if !positional && (arg == "--help" || arg == "-h") {
			fmt.Fprint(stdout, usage)
			return 0
		}
		if !positional && arg == "--dry-run" {
			dry = true
			continue
		}
		if !positional && arg == "--allow-insecure-http" {
			allowInsecureHTTP = true
			continue
		}
		if !positional && strings.HasPrefix(arg, "-") {
			name, value, hasValue := strings.Cut(arg, "=")
			var target *string
			var limitOption string
			switch name {
			case "--joplin-token":
				target = &token
			case "--notebook":
				target = &notebook
			case "--joplin-url":
				target = &base
			case "--limit":
				target = &limitOption
			case "--state":
				target = &statePath
			default:
				return invalid("unknown option")
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return invalid(name + " requires a value")
				}
				i++
				value = args[i]
			}
			*target = value
			if name == "--limit" {
				if e := limits.set(limitOption); e != nil {
					return invalid(e.Error())
				}
			}
			continue
		}
		if source != "" {
			return invalid("exactly one source path is required")
		}
		source = arg
	}
	if source == "" {
		return invalid("source path is required")
	}
	if !dry && (token == "" || notebook == "") {
		return invalid("--joplin-token and --notebook are required (or set their environment variables)")
	}
	if statePath == "" {
		return invalid("--state must not be empty")
	}
	fail := func(e error) int {
		message := e.Error()
		if token != "" {
			message = strings.ReplaceAll(message, token, "[REDACTED]")
		}
		fmt.Fprintln(stderr, "error:", message)
		return 1
	}
	chats, e := LoadWithLimits(source, limits)
	if e != nil {
		return fail(fmt.Errorf("export validation failed; no Joplin or state changes: %w", e))
	}
	projects := map[string]bool{}
	for _, c := range chats {
		if c.ProjectID != "" {
			projects[c.ProjectID] = true
		}
	}
	if len(chats) > 0 && len(projects) == 0 {
		fmt.Fprintln(stderr, "warning: export contains no project metadata; all conversations will be imported into the destination notebook without project sub-notebooks")
	}
	if dry {
		fmt.Fprintf(stdout, "Parsed %d conversations in %d projects\n", len(chats), len(projects))
		return 0
	}
	c, e := newClient(token, base, allowInsecureHTTP)
	if e != nil {
		return fail(e)
	}
	if c.base.Scheme == "http" && !loopbackHost(c.base.Hostname()) {
		fmt.Fprintln(stderr, "warning: sending the Joplin token over insecure HTTP to a non-loopback host")
	}
	r, e := Synchronize(ctx, c, chats, notebook, statePath)
	if e != nil {
		var partial *PartialError
		if errors.As(e, &partial) {
			p := partial.Result
			fmt.Fprintf(stderr, "Failed after: %d created, %d updated, %d unchanged, %d project notebooks created. Existing changes were not rolled back; fix the reported error and rerun the same import to resume safely.\n", p.Created, p.Updated, p.Unchanged, p.FoldersCreated)
		}
		return fail(e)
	}
	fmt.Fprintf(stdout, "Done: %d created, %d updated, %d unchanged, %d project notebooks created\n", r.Created, r.Updated, r.Unchanged, r.FoldersCreated)
	return 0
}
