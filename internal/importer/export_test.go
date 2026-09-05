package importer

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `[{"id":"c1","title":"Тест","project_id":"p1","create_time":10,"update_time":20,"current_node":"a2","mapping":{"a1":{"message":{"author":{"role":"user"},"content":{"parts":["Question"]},"create_time":10}},"a2":{"parent":"a1","message":{"author":{"role":"assistant"},"content":{"parts":["Answer",{"asset":"image"}]},"create_time":11}},"unused":{"parent":"a1","message":{"content":{"parts":["Old branch"]}}}}}]`

func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func makeZIP(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "export.zip")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for n, s := range files {
		w, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(s)); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestLoadFormats(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "conversations.json"), fixture)
	projects := `[{"id":"p1","name":"Project One"}]`
	write(t, filepath.Join(dir, "projects.json"), projects)
	z := makeZIP(t, map[string]string{"export/conversations.json": fixture, "export/projects.json": projects, "export/image.png": "binary"})
	for _, p := range []string{dir, filepath.Join(dir, "conversations.json"), z} {
		chats, e := Load(p)
		if e != nil {
			t.Fatal(e)
		}
		if len(chats) != 1 {
			t.Fatal(chats)
		}
		c := chats[0]
		if c.ProjectName != "Project One" || c.UpdatedMS != 20000 || !strings.Contains(c.Body, "Question") || !strings.Contains(c.Body, "Answer") || strings.Contains(c.Body, "Old branch") || !strings.Contains(c.Body, "```json") || !strings.Contains(c.Body, "1970-01-01T00:00:10+00:00") {
			t.Fatalf("unexpected chat: %+v", c)
		}
	}
}
func TestLoadSpecificFileAndShards(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "conversations.json"), `[{"id":"one"}]`)
	write(t, filepath.Join(dir, "conversations-2.json"), `{"conversations":[{"conversation_id":"two","project":{"id":"p","title":"Project"}}]}`)
	c, e := Load(dir)
	if e != nil || len(c) != 2 {
		t.Fatalf("%v %v", c, e)
	}
	c, e = Load(filepath.Join(dir, "conversations.json"))
	if e != nil || len(c) != 1 || c[0].ID != "one" {
		t.Fatalf("%v %v", c, e)
	}
}
func TestLoadRejectsBadInputs(t *testing.T) {
	for _, data := range []string{`{`, `null`, `[1]`, `[] {}`, `[{"id":"bad -->"}]`} {
		p := filepath.Join(t.TempDir(), "conversations.json")
		write(t, p, data)
		if _, e := Load(p); e == nil {
			t.Errorf("accepted %s", data)
		}
	}
	for _, files := range []map[string]string{{"../conversations.json": fixture}, {"a/conversations.json": fixture, "b/conversations.json": fixture}, {"image.png": "x"}} {
		if _, e := Load(makeZIP(t, files)); e == nil {
			t.Fatal("accepted invalid ZIP")
		}
	}
	if _, e := Load(filepath.Join(t.TempDir(), "missing.json")); e == nil {
		t.Fatal("accepted missing source")
	}
}
func TestLoadCycleAndFallback(t *testing.T) {
	for _, current := range []string{"a", "missing"} {
		p := filepath.Join(t.TempDir(), "conversations.json")
		write(t, p, `[{"id":"c","current_node":"`+current+`","mapping":{"a":{"parent":"b","message":{"create_time":2,"content":{"parts":["Second"]}}},"b":{"parent":"a","message":{"create_time":1,"content":{"parts":["First"]}}}}}]`)
		c, e := Load(p)
		if e != nil || len(c) != 1 {
			t.Fatalf("%v", e)
		}
		if strings.Index(c[0].Body, "First") > strings.Index(c[0].Body, "Second") {
			t.Fatal(c[0].Body)
		}
	}
}
