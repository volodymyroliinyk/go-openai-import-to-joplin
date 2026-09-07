package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestLoadLimitsRejectWholeExport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value int64
	}{
		{"file-bytes", 10}, {"json-bytes", 10}, {"string-bytes", 8}, {"json-values", 5},
		{"depth", 3}, {"messages", 2}, {"parts", 1}, {"id-bytes", 1}, {"title-bytes", 4}, {"render-bytes", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "conversations.json")
			write(t, p, fixture)
			chats, err := LoadWithLimits(p, Limits{tc.name: tc.value})
			if err == nil || chats != nil || !strings.Contains(err.Error(), "--limit "+tc.name+"=VALUE") {
				t.Fatal(chats, err)
			}
			chats, err = Load(p)
			if err != nil || len(chats) != 1 {
				t.Fatal(chats, err)
			}
		})
	}
}

func TestLoadByteAndConversationBoundaries(t *testing.T) {
	dir := t.TempDir()
	first := `[{"id":"first"}]`
	last := `[{"id":"last"}]`
	write(t, filepath.Join(dir, "conversations-1.json"), first)
	write(t, filepath.Join(dir, "conversations-2.json"), last)
	write(t, filepath.Join(dir, "projects.json"), `[]`)
	size := int64(len(first) + len(last) + 2)
	for _, limits := range []Limits{{"json-bytes": size, "conversations": 2}, {"file-bytes": int64(len(first))}} {
		chats, err := LoadWithLimits(dir, limits)
		if err != nil || len(chats) != 2 {
			t.Fatal(chats, err)
		}
	}
	for _, limits := range []Limits{{"json-bytes": size - 1}, {"conversations": 1}} {
		chats, err := LoadWithLimits(dir, limits)
		if err == nil || chats != nil {
			t.Fatal("partial result", chats, err)
		}
	}
}

func TestLoadZIPResourceLimits(t *testing.T) {
	p := makeZIP(t, map[string]string{"conversations.json": fixture, "projects.json": "[]", "attachment.bin": strings.Repeat("x", 1<<20)})
	chats, err := LoadWithLimits(p, Limits{"file-bytes": int64(len(fixture)), "json-bytes": int64(len(fixture) + 2)})
	if err != nil || len(chats) != 1 {
		t.Fatal("attachment consumed JSON budget", chats, err)
	}
	for _, limits := range []Limits{{"file-bytes": int64(len(fixture) - 1)}, {"json-bytes": int64(len(fixture) + 1)}, {"entries": 2}, {"zip-index-bytes": 1}} {
		chats, err = LoadWithLimits(p, limits)
		if err == nil || chats != nil {
			t.Fatal(chats, err)
		}
	}
}

func TestLoadMissingIDRejectsPreviouslyParsedChats(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conversations.json")
	write(t, p, `[{"id":"valid"},{"title":"missing ID"}]`)
	chats, err := Load(p)
	if err == nil || chats != nil || !strings.Contains(err.Error(), "conversation 2 has no id") {
		t.Fatal(chats, err)
	}
}

func TestGuardedJSONBoundariesAcrossReads(t *testing.T) {
	data := `{"x":["[\"{}]",1,true,null]}`
	if !json.Valid([]byte(data)) {
		t.Fatal("invalid test data")
	}
	for _, oneByte := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			pass, fail int64
		}{
			{"file-bytes", int64(len(data)), int64(len(data) - 1)},
			{"json-bytes", int64(len(data)), int64(len(data) - 1)},
			{"depth", 2, 1}, {"json-values", 7, 6}, {"string-bytes", 6, 5},
		} {
			t.Run(fmt.Sprintf("%s/oneByte=%v", tc.name, oneByte), func(t *testing.T) {
				for _, value := range []int64{tc.pass, tc.fail} {
					limits := DefaultLimits()
					limits[tc.name] = value
					var r io.Reader = strings.NewReader(data)
					if oneByte {
						r = iotest.OneByteReader(r)
					}
					guard := &guardedJSON{r: r, budget: &exportBudget{limits: limits}}
					_, err := decode(guard)
					if value == tc.pass && err != nil {
						t.Fatal(err)
					}
					if value == tc.fail && (err == nil || !strings.Contains(err.Error(), tc.name)) {
						t.Fatal("limit not enforced on actual reads", err)
					}
				}
			})
		}
	}
}

func TestLoadLimitOnTrailingWhitespace(t *testing.T) {
	data := `[]` + strings.Repeat(" ", 100)
	limits := DefaultLimits()
	limits["file-bytes"] = int64(len(data) - 1)
	guard := &guardedJSON{r: iotest.OneByteReader(strings.NewReader(data)), budget: &exportBudget{limits: limits}}
	if _, err := decode(guard); err == nil || !strings.Contains(err.Error(), "file-bytes") {
		t.Fatal(err)
	}
}

func TestIndentedPartPreservesJSONAndBoundsAllocation(t *testing.T) {
	for _, data := range []string{`{}`, `{"a":[],"b":{},"c":[1,{"text":"Test 😀 \\\" [{}]"}],"d":true,"e":null}`} {
		var v map[string]any
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			t.Fatal(err)
		}
		expected, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got, err := indentedPart(v, int64(len(expected)))
		if err != nil || got != string(expected) {
			t.Fatalf("%q != %q: %v", got, expected, err)
		}
		if _, err = indentedPart(v, int64(len(expected)-1)); err == nil {
			t.Fatal("render limit ignored")
		}
	}
}

func TestLoadDirectoryEntryLimit(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "conversations.json"), fixture)
	write(t, filepath.Join(dir, "other"), "ignored")
	if chats, err := LoadWithLimits(dir, Limits{"entries": 1}); err == nil || chats != nil {
		t.Fatal(chats, err)
	}
}
