package importer

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const noteSource = "go-openai-import-to-joplin"

var marker = regexp.MustCompile(`^<!-- chatgpt-conversation-id: ([^\s<>]+) -->$`)

type Result struct{ Created, Updated, Unchanged, FoldersCreated int }

type PartialError struct {
	Result Result
	Err    error
}

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

func (r Result) mutations() int { return r.Created + r.Updated + r.FoldersCreated }

func Synchronize(ctx context.Context, c Client, chats []Chat, notebook, statePath string) (r Result, err error) {
	defer func() {
		if err != nil && r.mutations() != 0 {
			err = &PartialError{Result: r, Err: err}
		}
	}()
	if e := ctx.Err(); e != nil {
		return r, e
	}
	statePath, unlock, e := lockState(statePath)
	if e != nil {
		return r, e
	}
	defer unlock()
	s, e := loadState(statePath)
	if e != nil {
		return r, e
	}
	folders, e := c.Folders(ctx)
	if e != nil {
		return r, e
	}
	byID := map[string]Folder{}
	matches := []string{}
	for _, f := range folders {
		byID[f.ID] = f
		if f.Title == notebook {
			matches = append(matches, f.ID)
		}
	}
	root := notebook
	if _, ok := byID[root]; !ok {
		if len(matches) == 0 {
			return r, fmt.Errorf("Joplin notebook not found: %q", notebook)
		}
		if len(matches) > 1 {
			return r, fmt.Errorf("notebook name is ambiguous: %q; use its ID", notebook)
		}
		root = matches[0]
	}
	destination := destinationState{Endpoint: c.DestinationID(), RootID: root}
	if !destination.valid() {
		return r, fmt.Errorf("cannot identify Joplin destination")
	}
	if s.Destination == nil {
		if len(s.Projects) != 0 {
			return r, fmt.Errorf("state has project mappings but no destination binding; use a different state path or explicitly migrate the state after verifying its Joplin endpoint and root notebook")
		}
		s.Destination = &destination
	} else if *s.Destination != destination {
		return r, fmt.Errorf("state destination mismatch: this state belongs to endpoint %q and root %q; use the matching destination or a different state path", s.Destination.Endpoint, s.Destination.RootID)
	}
	notes, e := c.MarkerNotes(ctx)
	if e != nil {
		return r, e
	}
	index := map[string]Note{}
	for _, n := range notes {
		line, _, _ := strings.Cut(n.Body, "\n")
		m := marker.FindStringSubmatch(line)
		if m == nil && strings.Contains(line, "chatgpt-conversation-id:") {
			return r, fmt.Errorf("invalid ChatGPT marker in note %s", n.ID)
		}
		if m != nil {
			id := m[1]
			if old, ok := index[id]; ok && old.ID != n.ID {
				return r, fmt.Errorf("duplicate ChatGPT ID markers: %s in notes %s, %s", id, old.ID, n.ID)
			}
			index[id] = n
		}
	}
	for _, chat := range chats {
		if !marker.MatchString("<!-- chatgpt-conversation-id: " + chat.ID + " -->") {
			return r, fmt.Errorf("invalid conversation ID: %q", chat.ID)
		}
	}
	projectNames := map[string]string{}
	for _, chat := range chats {
		if chat.ProjectID == "" {
			continue
		}
		title := first(chat.ProjectName, "ChatGPT project "+chat.ProjectID)
		if previous, ok := projectNames[chat.ProjectID]; ok && previous != title {
			return r, fmt.Errorf("conflicting names for project %q: %q and %q; whole import rejected", chat.ProjectID, previous, title)
		}
		projectNames[chat.ProjectID] = title
	}
	// Validate every cached folder before any writes. State is only a cache,
	// not authority to rename or move an existing Joplin folder.
	for project, p := range s.Projects {
		if f, ok := byID[p.ID]; ok {
			if f.ID == root || f.ParentID != root || f.Title != first(p.Title, "ChatGPT project "+project) {
				return r, fmt.Errorf("unsafe project notebook mapping for %q: folder %s; restore the state mapping or remove it to create a new notebook", project, f.ID)
			}
		}
	}
	ordered := append([]Chat(nil), chats...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.ProjectName != b.ProjectName {
			return a.ProjectName < b.ProjectName
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
	for _, chat := range ordered {
		if e = ctx.Err(); e != nil {
			return r, e
		}
		parent := root
		if chat.ProjectID != "" {
			p := s.Projects[chat.ProjectID]
			title := projectNames[chat.ProjectID]
			f, ok := byID[p.ID]
			if !ok || f.Title != title {
				f, e = c.CreateFolder(ctx, Folder{Title: title, ParentID: root})
				if e != nil {
					return r, e
				}
				r.FoldersCreated++
				if f.ID == "" {
					return r, fmt.Errorf("Joplin returned folder without ID")
				}
				p = projectState{f.ID, title}
				if e = checkpointProject(statePath, chat.ProjectID, p, destination); e != nil {
					return r, fmt.Errorf("checkpoint project %q folder %s: %w; preserve this folder ID and repair the state mapping before retrying", chat.ProjectID, f.ID, e)
				}
				s.Projects[chat.ProjectID] = p
			}
			byID[f.ID] = f
			parent = f.ID
		}
		body := chat.Body
		if line, _, _ := strings.Cut(body, "\n"); line != "<!-- chatgpt-conversation-id: "+chat.ID+" -->" {
			body = "<!-- chatgpt-conversation-id: " + chat.ID + " -->\n\n" + body
		}
		sourceURL := "https://chatgpt.com/c/" + chat.ID
		if strings.HasPrefix(chat.ID, "codex-") {
			sourceURL = ""
		}
		n := Note{Title: chat.Title, Body: body, ParentID: parent, Source: noteSource, SourceURL: sourceURL, CreatedMS: chat.CreatedMS, UpdatedMS: chat.UpdatedMS}
		if old, ok := index[chat.ID]; ok {
			n.ID = old.ID
			comparison := n
			if comparison.CreatedMS == 0 {
				comparison.CreatedMS = old.CreatedMS
			}
			if comparison.UpdatedMS == 0 {
				comparison.UpdatedMS = old.UpdatedMS
			}
			if comparison == old {
				r.Unchanged++
			} else {
				if e = c.UpdateNote(ctx, n); e != nil {
					return r, e
				}
				r.Updated++
			}
		} else {
			created, e := c.CreateNote(ctx, n)
			if e != nil {
				return r, e
			}
			if created.ID == "" {
				return r, fmt.Errorf("Joplin returned note without ID")
			}
			n.ID = created.ID
			r.Created++
		}
		index[chat.ID] = n
	}
	// Keep checkpoints if compaction fails; replay is idempotent after a crash.
	if e = saveState(statePath, s); e != nil {
		return r, e
	}
	return r, clearProjectCheckpoints(statePath)
}
