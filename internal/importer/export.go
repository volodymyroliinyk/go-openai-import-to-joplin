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
	"reflect"
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
		if e != nil {
			return nil, e
		}
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
func Load(source string) ([]Chat, error) { return LoadWithLimits(source, nil) }

// LoadWithLimits returns no partial chats on any error. The caller must finish
// loading the whole export successfully before creating a Joplin client.
func LoadWithLimits(source string, overrides Limits) ([]Chat, error) {
	limits := DefaultLimits()
	for name, value := range overrides {
		if _, ok := limits[name]; !ok || value <= 0 {
			return nil, fmt.Errorf("invalid export limit: %s", name)
		}
		limits[name] = value
	}
	budget := &exportBudget{limits: limits}

	info, e := os.Stat(source)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file or directory")
	}
	readers := map[string]func() (io.ReadCloser, error){}
	labels := map[string]string{}
	if info.IsDir() || strings.EqualFold(filepath.Ext(source), ".json") {
		root := source
		if !info.IsDir() {
			root = filepath.Dir(source)
		}
		if info.IsDir() {
			dir, e := os.Open(root)
			if e != nil {
				return nil, e
			}
			defer dir.Close()
			var count uint64
			for {
				files, e := dir.ReadDir(128)
				if e != nil && e != io.EOF {
					return nil, e
				}
				for _, f := range files {
					count++
					if e := limits.check("entries", count); e != nil {
						return nil, fmt.Errorf("%s: %w", source, e)
					}
					name := f.Name()
					full := filepath.Join(root, name)
					if !f.IsDir() && (isConversations(name) || isProjects(name)) {
						readers[name] = func() (io.ReadCloser, error) { return openExportJSON(full, limits) }
					}
				}
				if e == io.EOF {
					break
				}
			}
		}
		if !info.IsDir() {
			readers = map[string]func() (io.ReadCloser, error){}
			readers["conversations.json"] = func() (io.ReadCloser, error) { return openExportJSON(source, limits) }
			labels["conversations.json"] = filepath.Base(source)
			for _, n := range []string{"projects.json", "chatgpt_projects.json"} {
				full := filepath.Join(root, n)
				if _, e := os.Stat(full); e == nil {
					readers[n] = func() (io.ReadCloser, error) { return openExportJSON(full, limits) }
				}
			}
		}
	} else {
		file, e := os.Open(source)
		if e != nil {
			return nil, e
		}
		defer file.Close()
		stat, e := file.Stat()
		if e != nil {
			return nil, e
		}
		indexReader := &guardedZIPIndex{r: file, limit: limits["zip-index-bytes"], active: true}
		z, e := zip.NewReader(indexReader, stat.Size())
		if indexReader.err != nil {
			return nil, fmt.Errorf("%s: %w", source, indexReader.err)
		}
		if e != nil {
			return nil, fmt.Errorf("not a directory, JSON, or ZIP export: %w", e)
		}
		indexReader.active = false
		if e := limits.check("entries", uint64(len(z.File))); e != nil {
			return nil, fmt.Errorf("%s: %w", source, e)
		}
		var declaredJSON uint64
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
					if e := limits.check("file-bytes", f.UncompressedSize64); e != nil {
						return nil, fmt.Errorf("%s: %w", n, e)
					}
					if f.UncompressedSize64 > uint64(limits["json-bytes"])-declaredJSON {
						return nil, fmt.Errorf("%s: %w", n, limitError("json-bytes", limits["json-bytes"], declaredJSON+f.UncompressedSize64))
					}
					declaredJSON += f.UncompressedSize64
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
		guard := &guardedJSON{r: r, budget: budget}
		v, e := decode(guard)
		if guard.err != nil {
			e = guard.err
		}
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
				title := first(m["name"], m["title"], id)
				if e := checkNames(limits, id, title); e != nil {
					return nil, fmt.Errorf("%s: %w", n, e)
				}
				projects[id] = title
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
	type conversationOrigin struct {
		entry    object
		location string
	}
	origins := map[string]conversationOrigin{}
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
		for position, v := range a {
			if budget.conversations == limits["conversations"] {
				return nil, fmt.Errorf("%s: %w", n, limitError("conversations", limits["conversations"], uint64(budget.conversations)+1))
			}
			budget.conversations++
			m := obj(v)
			if m == nil {
				return nil, fmt.Errorf("invalid conversation entry")
			}
			id := first(m["id"], m["conversation_id"])
			if id == "" {
				return nil, fmt.Errorf("%s: conversation %d has no id or conversation_id; whole export rejected", n, position+1)
			}
			if strings.ContainsAny(id, " \t\r\n<>") {
				return nil, fmt.Errorf("invalid conversation ID")
			}
			label := n
			if labels[n] != "" {
				label = labels[n]
			}
			location := fmt.Sprintf("%s conversation %d", label, position+1)
			if previous, ok := origins[id]; ok {
				if reflect.DeepEqual(previous.entry, m) {
					continue
				}
				return nil, fmt.Errorf("conflicting duplicate conversation ID %q in %s and %s; whole export rejected", id, previous.location, location)
			}
			origins[id] = conversationOrigin{entry: m, location: location}
			p := obj(m["project"])
			pid := first(m["project_id"], m["project_uuid"], p["id"])
			title := first(m["title"], "Untitled ChatGPT conversation")
			projectName := first(p["name"], p["title"], projects[pid])
			if e := checkNames(limits, id, title); e != nil {
				return nil, fmt.Errorf("%s: conversation %d: %w", n, position+1, e)
			}
			if e := checkNames(limits, pid, projectName); e != nil {
				return nil, fmt.Errorf("%s: conversation %d: %w", n, position+1, e)
			}
			mapping := obj(m["mapping"])
			if e := limits.check("messages", uint64(len(mapping))); e != nil {
				return nil, fmt.Errorf("%s: conversation %d: %w", n, position+1, e)
			}
			for _, node := range mapping {
				parts, _ := obj(obj(obj(node)["message"])["content"])["parts"].([]any)
				if e := limits.check("parts", uint64(len(parts))); e != nil {
					return nil, fmt.Errorf("%s: conversation %d: %w", n, position+1, e)
				}
			}
			body, e := renderLimited(m, id, limits["render-bytes"]-budget.rendered)
			if e != nil {
				if failure, ok := e.(*exportLimitError); ok && failure.name == "render-bytes" {
					// Nested rendering buffers use remaining budgets; report the
					// configured whole-export budget in the actionable error.
					e = limitError("render-bytes", limits["render-bytes"], uint64(limits["render-bytes"])+1)
				}
				return nil, fmt.Errorf("%s: conversation %d: %w", n, position+1, e)
			}
			budget.rendered += int64(len(body))
			order = append(order, id)
			chats[id] = Chat{id, title, pid, projectName, millis(m["create_time"]), millis(m["update_time"]), body}
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
	return renderLimited(c, id, DefaultLimits()["render-bytes"])
}

func renderLimited(c object, id string, maxBytes int64) (string, error) {
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
	body := limitedText{limit: maxBytes}
	if e := body.add("<!-- chatgpt-conversation-id: " + id + " -->\n\n"); e != nil {
		return "", e
	}
	messageCount := 0
	for _, m := range messages {
		parts, _ := obj(m["content"])["parts"].([]any)
		texts := limitedText{limit: maxBytes}
		partCount := 0
		for _, p := range parts {
			var text string
			switch x := p.(type) {
			case string:
				if x == "" {
					continue
				}
				text = x
			case map[string]any:
				b, e := indentedPart(x, maxBytes-int64(texts.Len()))
				if e != nil {
					return "", e
				}
				text = "```json\n" + b + "\n```"
			default:
				continue
			}
			if partCount > 0 {
				if e := texts.add("\n\n"); e != nil {
					return "", e
				}
			}
			if e := texts.add(text); e != nil {
				return "", e
			}
			partCount++
		}
		text := strings.TrimSpace(texts.String())
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
		if messageCount > 0 {
			if e := body.add("\n\n---\n\n"); e != nil {
				return "", e
			}
		}
		if e := body.add("## " + label + stamp + "\n\n"); e != nil {
			return "", e
		}
		if e := body.add(text); e != nil {
			return "", e
		}
		messageCount++
	}
	return body.String(), nil
}
