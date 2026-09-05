package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var marker = regexp.MustCompile(`^<!-- chatgpt-conversation-id: ([^\s<>]+) -->$`)

type projectState struct {
	ID    string `json:"joplin_id"`
	Title string `json:"title"`
}
type noteState struct {
	ID        string `json:"joplin_id"`
	UpdatedMS int64  `json:"updated_ms"`
}
type state struct {
	Notes    map[string]noteState    `json:"notes"`
	Projects map[string]projectState `json:"projects"`
}
type Result struct{ Created, Updated, Unchanged, FoldersCreated int }

func loadState(path string) (state, error) {
	s := state{}
	b, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return s, e
	}
	if e == nil {
		if e = json.Unmarshal(b, &s); e != nil {
			return s, fmt.Errorf("invalid state file: %w", e)
		}
	}
	if s.Notes == nil {
		s.Notes = map[string]noteState{}
	}
	if s.Projects == nil {
		s.Projects = map[string]projectState{}
	}
	return s, nil
}
func saveState(path string, s state) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if e = enc.Encode(s); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func Synchronize(ctx context.Context, c Client, chats []Chat, notebook, statePath string) (Result, error) {
	r := Result{}
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
	notes, e := c.Notes(ctx)
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
			title := first(chat.ProjectName, "ChatGPT project "+chat.ProjectID)
			f, ok := byID[p.ID]
			if !ok {
				f, e = c.CreateFolder(ctx, Folder{Title: title, ParentID: root})
				if e != nil {
					return r, e
				}
				if f.ID == "" {
					return r, fmt.Errorf("Joplin returned folder without ID")
				}
				r.FoldersCreated++
			} else if f.Title != title || f.ParentID != root {
				f.Title = title
				f.ParentID = root
				if e = c.UpdateFolder(ctx, f); e != nil {
					return r, e
				}
			}
			byID[f.ID] = f
			s.Projects[chat.ProjectID] = projectState{f.ID, chat.ProjectName}
			if e = saveState(statePath, s); e != nil {
				return r, e
			}
			parent = f.ID
		}
		body := chat.Body
		if line, _, _ := strings.Cut(body, "\n"); line != "<!-- chatgpt-conversation-id: "+chat.ID+" -->" {
			body = "<!-- chatgpt-conversation-id: " + chat.ID + " -->\n\n" + body
		}
		n := Note{Title: chat.Title, Body: body, ParentID: parent, Source: "chatgpt-import-to-joplin", SourceURL: "https://chatgpt.com/c/" + chat.ID, CreatedMS: chat.CreatedMS, UpdatedMS: chat.UpdatedMS}
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
		s.Notes[chat.ID] = noteState{n.ID, chat.UpdatedMS}
		if e = saveState(statePath, s); e != nil {
			return r, e
		}
	}
	return r, nil
}
