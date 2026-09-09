package importer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LoadCodexSessions imports local Codex transcript JSONL files. Notes begin with
// a readable transcript and retain every JSONL record verbatim in a collapsed
// appendix so unknown and future event types are never silently discarded.
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
	var rawRecords []string
	lastSpeaker, lastText := "", ""
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
		renderCodexEvent(&body, event, payload, &lastSpeaker, &lastText)
		rawRecords = append(rawRecords, string(line))
	}
	if err := reader.Err(); err != nil {
		return Chat{}, fmt.Errorf("%s: read JSONL: %w", path, err)
	}
	if record == 0 {
		return Chat{}, fmt.Errorf("%s: empty Codex session", path)
	}
	body.WriteString("---\n\n<details>\n<summary>Original Codex JSONL records (lossless)</summary>\n\n```jsonl\n")
	for _, line := range rawRecords {
		body.WriteString(line)
		body.WriteByte('\n')
	}
	body.WriteString("```\n\n</details>\n")
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

func renderCodexEvent(body *strings.Builder, event, payload map[string]any, lastSpeaker, lastText *string) {
	eventType, subtype := str(event["type"]), str(payload["type"])
	writeMessage := func(speaker, heading, text string) {
		text = strings.TrimSpace(text)
		if text == "" || (*lastSpeaker == speaker && *lastText == text) {
			return
		}
		fmt.Fprintf(body, "## %s\n\n%s\n\n", heading, text)
		*lastSpeaker, *lastText = speaker, text
	}

	switch eventType {
	case "event_msg":
		switch subtype {
		case "user_message":
			writeMessage("user", "User", str(payload["message"]))
		case "agent_message":
			writeMessage("assistant", "Codex", first(payload["message"], payload["text"]))
		case "agent_reasoning":
			writeMessage("reasoning", "Reasoning", first(payload["text"], payload["message"]))
		case "token_count":
			writeCodexDetails(body, "Token usage", payload)
		case "agent_reasoning_section_break":
			body.WriteString("---\n\n")
		default:
			writeCodexDetails(body, "Event: "+first(subtype, eventType), payload)
		}
	case "response_item":
		switch subtype {
		case "message":
			role, heading := str(payload["role"]), "Codex"
			if role == "user" {
				heading = "User"
			}
			writeMessage(role, heading, codexContentText(payload["content"]))
		case "reasoning":
			writeMessage("reasoning", "Reasoning", first(codexContentText(payload["summary"]), codexContentText(payload["content"])))
		case "function_call", "custom_tool_call", "web_search_call":
			writeCodexDetails(body, "Tool call: "+first(payload["name"], subtype), payload)
		case "function_call_output", "custom_tool_call_output":
			writeCodexDetails(body, "Tool output", payload)
		default:
			writeCodexDetails(body, "Response item: "+first(subtype, "unknown"), payload)
		}
	case "session_meta":
		writeCodexDetails(body, "Session metadata", payload)
	case "turn_context":
		writeCodexDetails(body, "Turn context", payload)
	default:
		writeCodexDetails(body, "Event: "+first(eventType, "unknown"), payload)
	}
}

func codexContentText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			if text := strings.TrimSpace(codexContentText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	case map[string]any:
		return first(value["text"], value["message"], value["output"])
	default:
		return ""
	}
}

func writeCodexDetails(body *strings.Builder, summary string, payload map[string]any) {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintf(body, "<details>\n<summary>%s</summary>\n\n", summary)
	for _, line := range strings.Split(string(encoded), "\n") {
		fmt.Fprintf(body, "    %s\n", line)
	}
	body.WriteString("\n</details>\n\n")
}

func oneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > max {
		value = string(runes[:max]) + "…"
	}
	return value
}
