package importer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LoadCodexSessions imports local Codex transcript JSONL files. Every JSONL
// record is retained verbatim in the rendered note so unknown and future event
// types are preserved rather than silently discarded.
func LoadCodexSessions(source string, overrides Limits) ([]Chat, error) {
	limits := DefaultLimits()
	for name, value := range overrides {
		if _, ok := limits[name]; !ok || value <= 0 {
			return nil, fmt.Errorf("invalid export limit: %s", name)
		}
		limits[name] = value
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	var paths []string
	if info.IsDir() {
		var entries uint64
		err = filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			entries++
			if err := limits.check("entries", entries); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".jsonl") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else if info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(source), ".jsonl") {
		paths = []string{source}
	} else {
		return nil, fmt.Errorf("Codex source must be a JSONL file or directory containing JSONL sessions")
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Codex session JSONL files found")
	}
	if err := limits.check("conversations", uint64(len(paths))); err != nil {
		return nil, err
	}
	budget := &exportBudget{limits: limits}
	var totalRendered uint64
	seen := map[string]string{}
	chats := make([]Chat, 0, len(paths))
	for _, path := range paths {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if err := limits.check("file-bytes", uint64(stat.Size())); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		chat, err := parseCodexSession(path, data, stat.ModTime(), limits, budget)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[chat.ID]; ok {
			return nil, fmt.Errorf("duplicate Codex session ID %q in %s and %s", chat.ID, previous, path)
		}
		seen[chat.ID] = path
		totalRendered += uint64(len(chat.Body))
		if err := limits.check("render-bytes", totalRendered); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

func parseCodexSession(path string, data []byte, modified time.Time, limits Limits, budget *exportBudget) (Chat, error) {
	reader := bufio.NewScanner(bytes.NewReader(data))
	reader.Buffer(make([]byte, 64<<10), int(limits["file-bytes"]))
	var id, cwd, title string
	var created time.Time
	var body strings.Builder
	record := 0
	for reader.Scan() {
		line := append([]byte(nil), reader.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		record++
		guard := &guardedJSON{r: bytes.NewReader(line), budget: budget}
		value, err := decode(guard)
		if guard.err != nil {
			err = guard.err
		}
		if err != nil {
			return Chat{}, fmt.Errorf("%s: JSONL record %d: %w", path, record, err)
		}
		event := obj(value)
		if event == nil {
			return Chat{}, fmt.Errorf("%s: JSONL record %d must be a JSON object", path, record)
		}
		payload := obj(event["payload"])
		if str(event["type"]) == "session_meta" {
			id = first(payload["session_id"], payload["id"], id)
			cwd = first(payload["cwd"], cwd)
		}
		if title == "" && str(event["type"]) == "event_msg" && str(payload["type"]) == "user_message" {
			title = oneLine(str(payload["message"]), 120)
		}
		if created.IsZero() {
			created, _ = time.Parse(time.RFC3339Nano, first(event["timestamp"], payload["timestamp"]))
		}
		kind := first(event["type"], "record")
		if subtype := str(payload["type"]); subtype != "" {
			kind += " / " + subtype
		}
		fmt.Fprintf(&body, "## %s\n\n", kind)
		for _, row := range strings.Split(string(line), "\n") {
			body.WriteString("    ")
			body.WriteString(row)
			body.WriteByte('\n')
		}
		body.WriteByte('\n')
	}
	if err := reader.Err(); err != nil {
		return Chat{}, fmt.Errorf("%s: read JSONL: %w", path, err)
	}
	if record == 0 {
		return Chat{}, fmt.Errorf("%s: empty Codex session", path)
	}
	if id == "" {
		return Chat{}, fmt.Errorf("%s: Codex session has no session_meta ID", path)
	}
	id = "codex-" + id
	if strings.ContainsAny(id, " \t\r\n<>") {
		return Chat{}, fmt.Errorf("%s: invalid Codex session ID", path)
	}
	if title == "" {
		title = "Codex session " + oneLine(strings.TrimPrefix(id, "codex-"), 16)
	}
	projectID, projectName := "", ""
	if cwd != "" {
		hash := sha256.Sum256([]byte(filepath.Clean(cwd)))
		projectID = "codex-project-" + hex.EncodeToString(hash[:])
		projectName = filepath.Base(filepath.Clean(cwd))
		if projectName == "." || projectName == string(filepath.Separator) {
			projectName = cwd
		}
	}
	if err := checkNames(limits, id, title); err != nil {
		return Chat{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkNames(limits, projectID, projectName); err != nil {
		return Chat{}, fmt.Errorf("%s: %w", path, err)
	}
	createdMS := created.UnixMilli()
	if created.IsZero() {
		createdMS = modified.UnixMilli()
	}
	return Chat{ID: id, Title: title, ProjectID: projectID, ProjectName: projectName, CreatedMS: createdMS, UpdatedMS: modified.UnixMilli(), Body: body.String()}, nil
}

func oneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > max {
		value = string(runes[:max]) + "…"
	}
	return value
}
