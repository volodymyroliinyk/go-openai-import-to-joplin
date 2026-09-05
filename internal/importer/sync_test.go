package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeClient struct {
	folders          []Folder
	notes            []Note
	puts, folderPuts int
	failNote         bool
}

func newFake() *fakeClient                                      { return &fakeClient{folders: []Folder{{ID: "root", Title: "Knowledge"}}} }
func (f *fakeClient) Folders(context.Context) ([]Folder, error) { return f.folders, nil }
func (f *fakeClient) Notes(context.Context) ([]Note, error)     { return f.notes, nil }
func (f *fakeClient) CreateFolder(_ context.Context, v Folder) (Folder, error) {
	v.ID = fmt.Sprintf("f%d", len(f.folders))
	f.folders = append(f.folders, v)
	return v, nil
}
func (f *fakeClient) UpdateFolder(_ context.Context, v Folder) error {
	f.folderPuts++
	for i, x := range f.folders {
		if x.ID == v.ID {
			f.folders[i] = v
		}
	}
	return nil
}
func (f *fakeClient) CreateNote(_ context.Context, v Note) (Note, error) {
	if f.failNote {
		return Note{}, errors.New("note failed")
	}
	v.ID = fmt.Sprintf("n%d", len(f.notes))
	f.notes = append(f.notes, v)
	return v, nil
}
func (f *fakeClient) UpdateNote(_ context.Context, v Note) error {
	f.puts++
	for i, x := range f.notes {
		if x.ID == v.ID {
			f.notes[i] = v
		}
	}
	return nil
}
func syncTest(t *testing.T, f *fakeClient, c []Chat, p string) Result {
	t.Helper()
	r, e := Synchronize(context.Background(), f, c, "Knowledge", p)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestSyncChangeDetection(t *testing.T) {
	f := newFake()
	p := filepath.Join(t.TempDir(), "state.json")
	c := []Chat{{ID: "c", Title: "Title", Body: "Body", ProjectID: "p", ProjectName: "Project", CreatedMS: 1000, UpdatedMS: 2000}}
	r := syncTest(t, f, c, p)
	if r.Created != 1 || r.FoldersCreated != 1 {
		t.Fatal(r)
	}
	r = syncTest(t, f, c, p)
	if r.Unchanged != 1 || f.puts != 0 || len(f.folders) != 2 {
		t.Fatal(r)
	}
	f.notes[0].Body = "<!-- chatgpt-conversation-id: c --> local edit"
	r = syncTest(t, f, c, p)
	if r.Updated != 1 || f.puts != 1 {
		t.Fatal(r)
	}
	c[0].Title = "Renamed"
	c[0].ProjectName = "New project name"
	r = syncTest(t, f, c, p)
	if r.Updated != 1 || f.folderPuts != 1 {
		t.Fatal(r)
	}
	c[0].ProjectID = ""
	r = syncTest(t, f, c, p)
	if r.Updated != 1 || f.notes[0].ParentID != "root" {
		t.Fatal(r)
	}
	syncTest(t, f, nil, p)
	if len(f.notes) != 1 {
		t.Fatal("deleted absent note")
	}
}
func TestSyncMissingStaleStateAndDeletedNotes(t *testing.T) {
	f := newFake()
	p := filepath.Join(t.TempDir(), "state.json")
	c := []Chat{{ID: "c", Title: "Title", Body: "Body"}}
	syncTest(t, f, c, p)
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	r := syncTest(t, f, c, p)
	if r.Created != 0 || r.Unchanged != 1 {
		t.Fatal(r)
	}
	write(t, p, `{"notes":{"c":{"joplin_id":"wrong","updated_ms":0}},"projects":{}}`)
	r = syncTest(t, f, c, p)
	if r.Created != 0 {
		t.Fatal(r)
	}
	f.notes = nil
	r = syncTest(t, f, c, p)
	if r.Created != 1 {
		t.Fatal(r)
	}
}
func TestSyncDuplicateMarkersBeforeWrites(t *testing.T) {
	f := newFake()
	f.notes = []Note{{ID: "n1", Body: "<!-- chatgpt-conversation-id: c -->"}, {ID: "n2", Body: "<!-- chatgpt-conversation-id: other -->\n<!-- chatgpt-conversation-id: c -->"}}
	p := filepath.Join(t.TempDir(), "state.json")
	_, e := Synchronize(context.Background(), f, []Chat{{ID: "c", ProjectID: "p"}}, "Knowledge", p)
	if e == nil || len(f.folders) != 1 || f.puts != 0 {
		t.Fatal("duplicate not rejected before writes")
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("state written")
	}
}
func TestSyncPersistsFolderAfterFailure(t *testing.T) {
	f := newFake()
	f.failNote = true
	p := filepath.Join(t.TempDir(), "state.json")
	c := []Chat{{ID: "c", ProjectID: "p"}}
	if _, e := Synchronize(context.Background(), f, c, "Knowledge", p); e == nil {
		t.Fatal("expected failure")
	}
	f.failNote = false
	r := syncTest(t, f, c, p)
	if r.FoldersCreated != 0 || len(f.folders) != 2 {
		t.Fatal(r)
	}
	f.folders = f.folders[:1]
	r = syncTest(t, f, c, p)
	if r.FoldersCreated != 1 {
		t.Fatal(r)
	}
}
func TestSyncInvalidStateAndAmbiguousRoot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	write(t, p, `{"projects":[]}`)
	f := newFake()
	if _, e := Synchronize(context.Background(), f, nil, "Knowledge", p); e == nil {
		t.Fatal("accepted malformed state")
	}
	write(t, p, `{}`)
	f.folders = append(f.folders, Folder{ID: "other", Title: "Knowledge"})
	if _, e := Synchronize(context.Background(), f, nil, "Knowledge", p); e == nil {
		t.Fatal("accepted ambiguous name")
	}
	if _, e := Synchronize(context.Background(), f, nil, "root", p); e != nil {
		t.Fatal(e)
	}
}
