package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeClient struct {
	folders          []Folder
	notes            []Note
	puts, folderPuts int
	failNote         bool
	failAfterNotes   int
}

func newFake() *fakeClient                                        { return &fakeClient{folders: []Folder{{ID: "root", Title: "Knowledge"}}} }
func (f *fakeClient) DestinationID() string                       { return "fake://profile" }
func (f *fakeClient) Folders(context.Context) ([]Folder, error)   { return f.folders, nil }
func (f *fakeClient) MarkerNotes(context.Context) ([]Note, error) { return f.notes, nil }
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
	if f.failNote || f.failAfterNotes > 0 && len(f.notes) >= f.failAfterNotes {
		return Note{}, errors.New("note failed")
	}
	v.ID = fmt.Sprintf("n%d", len(f.notes))
	f.notes = append(f.notes, v)
	return v, nil
}

func TestSyncReportsPartialResult(t *testing.T) {
	f := newFake()
	f.failAfterNotes = 1
	r, err := Synchronize(context.Background(), f, []Chat{{ID: "one"}, {ID: "two"}}, "Knowledge", filepath.Join(t.TempDir(), "state.json"))
	var partial *PartialError
	if !errors.As(err, &partial) || r.Created != 1 || partial.Result != r || len(f.notes) != 1 || !strings.Contains(err.Error(), "note failed") {
		t.Fatalf("result=%+v partial=%+v notes=%d err=%v", r, partial, len(f.notes), err)
	}
}

type ambiguousWriteClient struct {
	*fakeClient
	failCreate, failUpdate bool
}

func (c *ambiguousWriteClient) CreateNote(ctx context.Context, note Note) (Note, error) {
	created, err := c.fakeClient.CreateNote(ctx, note)
	if err == nil && c.failCreate {
		c.failCreate = false
		return Note{}, errors.New("connection lost after create")
	}
	return created, err
}

func (c *ambiguousWriteClient) UpdateNote(ctx context.Context, note Note) error {
	err := c.fakeClient.UpdateNote(ctx, note)
	if err == nil && c.failUpdate {
		c.failUpdate = false
		return errors.New("connection lost after update")
	}
	return err
}

func TestSyncRetryAfterAmbiguousNoteWrites(t *testing.T) {
	t.Run("create response lost", func(t *testing.T) {
		f := newFake()
		client := &ambiguousWriteClient{fakeClient: f, failCreate: true}
		path := filepath.Join(t.TempDir(), "state.json")
		chat := []Chat{{ID: "c", Title: "Created", Body: "content"}}

		if result, err := Synchronize(context.Background(), client, chat, "Knowledge", path); err == nil || result != (Result{}) || len(f.notes) != 1 {
			t.Fatalf("result=%+v notes=%v err=%v", result, f.notes, err)
		}
		result := syncTest(t, f, chat, path)
		if result.Unchanged != 1 || result.Created != 0 || len(f.notes) != 1 {
			t.Fatalf("retry duplicated note: result=%+v notes=%v", result, f.notes)
		}
	})

	t.Run("update response lost", func(t *testing.T) {
		f := newFake()
		f.notes = []Note{{ID: "n", Title: "Old", Body: "<!-- chatgpt-conversation-id: c -->\n\nold", ParentID: "root", Source: "chatgpt-import-to-joplin", SourceURL: "https://chatgpt.com/c/c"}}
		client := &ambiguousWriteClient{fakeClient: f, failUpdate: true}
		path := filepath.Join(t.TempDir(), "state.json")
		chat := []Chat{{ID: "c", Title: "Updated", Body: "new"}}

		if result, err := Synchronize(context.Background(), client, chat, "Knowledge", path); err == nil || result != (Result{}) || f.notes[0].Title != "Updated" {
			t.Fatalf("result=%+v note=%+v err=%v", result, f.notes[0], err)
		}
		result := syncTest(t, f, chat, path)
		if result.Unchanged != 1 || result.Updated != 0 || f.puts != 1 {
			t.Fatalf("retry rewrote note: result=%+v puts=%d", result, f.puts)
		}
	})
}

func TestSyncRejectsConflictingProjectNamesBeforeWrites(t *testing.T) {
	f := newFake()
	p := filepath.Join(t.TempDir(), "state.json")
	chats := []Chat{
		{ID: "one", ProjectID: "project", ProjectName: "Alpha"},
		{ID: "two", ProjectID: "project", ProjectName: "Beta"},
	}
	r, err := Synchronize(context.Background(), f, chats, "Knowledge", p)
	if err == nil || !strings.Contains(err.Error(), `conflicting names for project "project"`) || r != (Result{}) || len(f.folders) != 1 || len(f.notes) != 0 {
		t.Fatalf("result=%+v folders=%v notes=%v err=%v", r, f.folders, f.notes, err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatal("preflight conflict wrote state", statErr)
	}
}

func TestSyncStoresCanonicalFallbackProjectName(t *testing.T) {
	f := newFake()
	p := filepath.Join(t.TempDir(), "state.json")
	r := syncTest(t, f, []Chat{{ID: "one", ProjectID: "project"}, {ID: "two", ProjectID: "project"}}, p)
	if r.FoldersCreated != 1 || len(f.folders) != 2 || f.folders[1].Title != "ChatGPT project project" {
		t.Fatal(r, f.folders)
	}
	s, err := loadState(p)
	if err != nil || s.Projects["project"].Title != "ChatGPT project project" {
		t.Fatal(s, err)
	}
}

func TestSyncRejectsStateDestinationMismatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		destination *destinationState
	}{
		{"endpoint", &destinationState{Endpoint: "fake://other-profile", RootID: "root"}},
		{"root", &destinationState{Endpoint: "fake://profile", RootID: "other-root"}},
		{"unbound mappings", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			p := filepath.Join(t.TempDir(), "state.json")
			if err := saveState(p, state{Destination: tc.destination, Projects: map[string]projectState{"p": {ID: "missing", Title: "Project"}}}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(p)
			r, err := Synchronize(context.Background(), f, []Chat{{ID: "c", ProjectID: "p", ProjectName: "Project"}}, "Knowledge", p)
			after, _ := os.ReadFile(p)
			if err == nil || r != (Result{}) || len(f.folders) != 1 || len(f.notes) != 0 || string(before) != string(after) || (!strings.Contains(err.Error(), "destination") && !strings.Contains(err.Error(), "no destination binding")) {
				t.Fatalf("result=%+v folders=%v notes=%v err=%v", r, f.folders, f.notes, err)
			}
		})
	}
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
	f.notes[0].Body = "<!-- chatgpt-conversation-id: c -->\nlocal edit"
	r = syncTest(t, f, c, p)
	if r.Updated != 1 || f.puts != 1 {
		t.Fatal(r)
	}
	c[0].Title = "Renamed"
	c[0].ProjectName = "New project name"
	r = syncTest(t, f, c, p)
	if r.Updated != 1 || f.folderPuts != 0 || r.FoldersCreated != 1 {
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
	f.notes = []Note{{ID: "n1", Body: "<!-- chatgpt-conversation-id: c -->"}, {ID: "n2", Body: "<!-- chatgpt-conversation-id: c -->\n<!-- chatgpt-conversation-id: other -->"}}
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

func TestSyncMarkerContentCannotClaimIdentity(t *testing.T) {
	f := newFake()
	p := filepath.Join(t.TempDir(), "state.json")
	chats := []Chat{{ID: "real", Body: "## You\n\n<!-- chatgpt-conversation-id: victim -->\n## ChatGPT\n\n<!-- chatgpt-conversation-id: another -->"}, {ID: "victim", Body: "original"}}
	syncTest(t, f, chats, p)
	r := syncTest(t, f, chats, p)
	if r.Unchanged != 2 || len(f.notes) != 2 || f.puts != 0 {
		t.Fatal(r, f.notes)
	}
}
func TestSyncRejectsMalformedFirstMarker(t *testing.T) {
	for _, body := range []string{"<!-- chatgpt-conversation-id: c --> trailing", "<!--chatgpt-conversation-id:c-->"} {
		f := newFake()
		f.notes = []Note{{ID: "n", Body: body}}
		_, err := Synchronize(context.Background(), f, []Chat{{ID: "c"}}, "Knowledge", filepath.Join(t.TempDir(), "state.json"))
		if err == nil || len(f.notes) != 1 || f.puts != 0 {
			t.Fatal("accepted malformed marker")
		}
	}
}

func TestSyncRejectsUnsafeProjectMappingsBeforeWrites(t *testing.T) {
	for _, target := range []Folder{
		{ID: "root", Title: "Knowledge"},
		{ID: "ancestor", Title: "Project"},
		{ID: "foreign", Title: "Other", ParentID: "root"},
		{ID: "moved", Title: "Project", ParentID: "elsewhere"},
	} {
		t.Run(target.ID, func(t *testing.T) {
			f := newFake()
			if target.ID != "root" {
				f.folders = append(f.folders, target)
			}
			p := filepath.Join(t.TempDir(), "state.json")
			destination := destinationState{Endpoint: f.DestinationID(), RootID: "root"}
			s := state{Destination: &destination, Projects: map[string]projectState{"p": {ID: target.ID, Title: "Project"}}}
			if err := saveState(p, s); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Synchronize(context.Background(), f, []Chat{{ID: "plain"}, {ID: "c", ProjectID: "p", ProjectName: "Renamed"}}, "Knowledge", p)
			if err == nil || f.folderPuts != 0 || len(f.notes) != 0 {
				t.Fatal("unsafe mapping allowed", err)
			}
			after, err := os.ReadFile(p)
			if err != nil || string(before) != string(after) {
				t.Fatal("state changed", err)
			}
		})
	}
}
func TestSyncNeverRenamesCachedFolder(t *testing.T) {
	f := newFake()
	f.folders = append(f.folders, Folder{ID: "foreign", Title: "Project", ParentID: "root"})
	p := filepath.Join(t.TempDir(), "state.json")
	destination := destinationState{Endpoint: f.DestinationID(), RootID: "root"}
	if err := saveState(p, state{Destination: &destination, Projects: map[string]projectState{"p": {ID: "foreign", Title: "Project"}}}); err != nil {
		t.Fatal(err)
	}
	r := syncTest(t, f, []Chat{{ID: "c", ProjectID: "p", ProjectName: "Renamed"}}, p)
	if r.FoldersCreated != 1 || f.folderPuts != 0 || f.folders[1].Title != "Project" || f.notes[0].ParentID == "foreign" {
		t.Fatal(r, f.folders)
	}
}
