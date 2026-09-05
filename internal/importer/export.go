package importer

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Chat struct {
	ID, Title, ProjectID, ProjectName string
	CreatedMS, UpdatedMS              int64
	Body                              string
}
type object = map[string]any

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func obj(v any) object { m, _ := v.(map[string]any); return m }
func first(v ...any) string {
	for _, x := range v {
		if s := str(x); s != "" {
			return s
		}
	}
	return ""
}
func millis(v any) int64 {
	f, e := strconv.ParseFloat(str(v), 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 9e15 {
		return 0
	}
	return int64(f * 1000)
}
func decode(r io.Reader) (any, error) {
	var v any
	d := json.NewDecoder(r)
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON document")
	}
	return v, nil
}
func entries(v any, key string) ([]any, error) {
	if m, ok := v.(map[string]any); ok {
		v = m[key]
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON array of %s", key)
	}
	return a, nil
}

// Load reads JSON directly from ZIP entries; binary attachments are never extracted.
func Load(source string) ([]Chat, error) {
	info, e := os.Stat(source)
	if e != nil {
		return nil, e
	}
	readers := map[string]func() (io.ReadCloser, error){}
	if info.IsDir() || strings.EqualFold(filepath.Ext(source), ".json") {
		root := source
		if !info.IsDir() {
			root = filepath.Dir(source)
		}
		files, e := os.ReadDir(root)
		if e != nil {
			return nil, e
		}
		for _, f := range files {
			name := f.Name()
			full := filepath.Join(root, name)
			if !f.IsDir() && (isConversations(name) || isProjects(name)) {
				readers[name] = func() (io.ReadCloser, error) { return os.Open(full) }
			}
		}
		if !info.IsDir() {
			readers = map[string]func() (io.ReadCloser, error){}
			readers["conversations.json"] = func() (io.ReadCloser, error) { return os.Open(source) }
			for _, n := range []string{"projects.json", "chatgpt_projects.json"} {
				full := filepath.Join(root, n)
				if _, e := os.Stat(full); e == nil {
					readers[n] = func() (io.ReadCloser, error) { return os.Open(full) }
				}
			}
		}
	} else {
		z, e := zip.OpenReader(source)
		if e != nil {
			return nil, fmt.Errorf("not a directory, JSON, or ZIP export: %w", e)
		}
		defer z.Close()
		roots := map[string]bool{}
		for _, f := range z.File {
			n := f.Name
			if strings.Contains(n, "\\") || path.IsAbs(n) || n == ".." || strings.HasPrefix(path.Clean(n), "../") {
				return nil, fmt.Errorf("unsafe path in ZIP export")
			}
			if !f.FileInfo().IsDir() && isConversations(path.Base(n)) {
				roots[path.Dir(n)] = true
			}
		}
		if len(roots) > 1 {
			return nil, fmt.Errorf("ZIP contains multiple export directories; select an unpacked directory")
		}
		for _, f := range z.File {
			if roots[path.Dir(f.Name)] && !f.FileInfo().IsDir() {
				n := path.Base(f.Name)
				if isConversations(n) || isProjects(n) {
					if _, ok := readers[n]; ok {
						return nil, fmt.Errorf("duplicate ZIP entry: %s", n)
					}
					readers[n] = f.Open
				}
			}
		}
	}
	read := func(n string) (any, error) {
		r, e := readers[n]()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		v, e := decode(r)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", n, e)
		}
		return v, nil
	}
	projects := map[string]string{}
	for _, n := range []string{"projects.json", "chatgpt_projects.json"} {
		if readers[n] == nil {
			continue
		}
		v, e := read(n)
		if e != nil {
			return nil, e
		}
		var a []any
		switch x := v.(type) {
		case []any:
			a = x
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				a = append(a, x[k])
			}
		default:
			return nil, fmt.Errorf("%s: expected array or object", n)
		}
		for _, p := range a {
			m := obj(p)
			if m == nil {
				return nil, fmt.Errorf("invalid project entry")
			}
			id := first(m["id"], m["project_id"])
			if id != "" {
				projects[id] = first(m["name"], m["title"], id)
			}
		}
	}
	names := []string{}
	for n := range readers {
		if isConversations(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no conversations*.json found")
	}
	chats := map[string]Chat{}
	order := []string{}
	for _, n := range names {
		v, e := read(n)
		if e != nil {
			return nil, e
		}
		a, e := entries(v, "conversations")
		if e != nil {
			return nil, e
		}
		for _, v := range a {
			m := obj(v)
			if m == nil {
				return nil, fmt.Errorf("invalid conversation entry")
			}
			id := first(m["id"], m["conversation_id"])
			if id == "" {
				continue
			}
			if strings.ContainsAny(id, " \t\r\n<>") {
				return nil, fmt.Errorf("invalid conversation ID")
			}
			p := obj(m["project"])
			pid := first(m["project_id"], m["project_uuid"], p["id"])
			body, e := render(m, id)
			if e != nil {
				return nil, e
			}
			if _, ok := chats[id]; !ok {
				order = append(order, id)
			}
			chats[id] = Chat{id, first(m["title"], "Untitled ChatGPT conversation"), pid, first(p["name"], p["title"], projects[pid]), millis(m["create_time"]), millis(m["update_time"]), body}
		}
	}
	result := make([]Chat, 0, len(order))
	for _, id := range order {
		result = append(result, chats[id])
	}
	return result, nil
}
func isConversations(n string) bool {
	return strings.HasPrefix(n, "conversations") && strings.HasSuffix(n, ".json")
}
func isProjects(n string) bool { return n == "projects.json" || n == "chatgpt_projects.json" }
func render(c object, id string) (string, error) {
	mapping := obj(c["mapping"])
	messages := []object{}
	seen := map[string]bool{}
	for current := str(c["current_node"]); current != "" && !seen[current]; {
		seen[current] = true
		n := obj(mapping[current])
		if n == nil {
			break
		}
		if m := obj(n["message"]); m != nil {
			messages = append(messages, m)
		}
		current = str(n["parent"])
	}
	if len(messages) > 0 {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	} else {
		keys := []string{}
		for k := range mapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if m := obj(obj(mapping[k])["message"]); m != nil {
				messages = append(messages, m)
			}
		}
		sort.SliceStable(messages, func(i, j int) bool { return millis(messages[i]["create_time"]) < millis(messages[j]["create_time"]) })
	}
	chunks := []string{}
	for _, m := range messages {
		parts, _ := obj(m["content"])["parts"].([]any)
		texts := []string{}
		for _, p := range parts {
			switch x := p.(type) {
			case string:
				if x != "" {
					texts = append(texts, x)
				}
			case map[string]any:
				b, e := json.MarshalIndent(x, "", "  ")
				if e != nil {
					return "", e
				}
				texts = append(texts, "```json\n"+string(b)+"\n```")
			}
		}
		text := strings.TrimSpace(strings.Join(texts, "\n\n"))
		if text == "" {
			continue
		}
		role := first(obj(m["author"])["role"], "unknown")
		label := map[string]string{"user": "You", "assistant": "ChatGPT", "system": "System", "tool": "Tool"}[role]
		if label == "" {
			label = strings.ToUpper(role[:1]) + role[1:]
		}
		stamp := ""
		if ms := millis(m["create_time"]); ms != 0 {
			t := time.UnixMilli(ms).UTC()
			layout := "2006-01-02T15:04:05"
			if t.Nanosecond() != 0 {
				layout += ".000000"
			}
			stamp = " · " + t.Format(layout) + "+00:00"
		}
		chunks = append(chunks, "## "+label+stamp+"\n\n"+text)
	}
	return "<!-- chatgpt-conversation-id: " + id + " -->\n\n" + strings.Join(chunks, "\n\n---\n\n"), nil
}
