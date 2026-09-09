package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCodexSessionsPreservesAllRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	data := strings.Join([]string{
		`{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"abc","cwd":"/work/demo","cli_version":"1.2.3"}}`,
		`{"timestamp":"2026-01-02T03:05:05Z","type":"event_msg","payload":{"type":"user_message","message":"Build the feature","images":["image.png"]}}`,
		`{"timestamp":"2026-01-02T03:06:05Z","type":"response_item","payload":{"type":"custom_tool_call","name":"shell","arguments":"go test ./..."}}`,
		`{"timestamp":"2026-01-02T03:07:05Z","type":"world_state","payload":{"branch":"develop"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	chats, err := LoadCodexSessions(dir, nil)
	if err != nil || len(chats) != 1 {
		t.Fatalf("%v %v", chats, err)
	}
	c := chats[0]
	if c.ID != "codex-abc" || c.Title != "Build the feature" || c.ProjectName != "demo" || !strings.HasPrefix(c.ProjectID, "codex-project-") || c.CreatedMS != 1767323045000 {
		t.Fatalf("unexpected chat: %+v", c)
	}
	for _, want := range []string{"session_meta", "cli_version", "user_message", "image.png", "custom_tool_call", "go test ./...", "world_state", "develop"} {
		if !strings.Contains(c.Body, want) {
			t.Fatalf("body omitted %q: %s", want, c.Body)
		}
	}
	for _, want := range []string{"## User\n\nBuild the feature", "<summary>Tool call: shell</summary>", "<summary>Original Codex JSONL records (lossless)</summary>"} {
		if !strings.Contains(c.Body, want) {
			t.Fatalf("body omitted readable rendering %q: %s", want, c.Body)
		}
	}
	if strings.Index(c.Body, "## User") > strings.Index(c.Body, "Original Codex JSONL records") {
		t.Fatal("readable transcript must precede raw appendix")
	}
}

func TestCodexReadableMessagesAndReasoning(t *testing.T) {
	data := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"readable"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Question"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"Consider the options"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Answer"}]}}`,
	}, "\n") + "\n"
	limits := DefaultLimits()
	chat, err := parseCodexSession("session.jsonl", []byte(data), time.Unix(1, 0), limits, &exportBudget{limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## User\n\nQuestion", "## Reasoning\n\nConsider the options", "## Codex\n\nAnswer"} {
		if !strings.Contains(chat.Body, want) {
			t.Fatalf("missing %q in %s", want, chat.Body)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if !strings.Contains(chat.Body, line) {
			t.Fatalf("raw record was not preserved: %s", line)
		}
	}
}

func TestCLICodexDryRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "session.jsonl")
	write(t, p, `{"type":"session_meta","payload":{"id":"session","cwd":"/work/project"}}`+"\n")
	var out, stderr strings.Builder
	code := Run(context.Background(), []string{p, "--codex", "--dry-run"}, &out, &stderr, func(string) string { return "" })
	if code != 0 || !strings.Contains(out.String(), "1 conversations in 1 projects") || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestLoadCodexRejectsDuplicateAndMalformedSessions(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.jsonl"), `{"type":"session_meta","payload":{"id":"same"}}`+"\n")
	write(t, filepath.Join(dir, "b.jsonl"), `{"type":"session_meta","payload":{"id":"same"}}`+"\n")
	if chats, err := LoadCodexSessions(dir, nil); err == nil || chats != nil || !strings.Contains(err.Error(), "duplicate Codex session ID") {
		t.Fatalf("accepted duplicates: %v %v", chats, err)
	}
	write(t, filepath.Join(dir, "b.jsonl"), "not-json\n")
	if chats, err := LoadCodexSessions(dir, nil); err == nil || chats != nil || !strings.Contains(err.Error(), "JSONL record 1") {
		t.Fatalf("accepted malformed JSONL: %v %v", chats, err)
	}
}
