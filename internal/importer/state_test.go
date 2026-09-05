package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type checkpointClient struct {
	*fakeClient
	beforeNote func()
}

func (c checkpointClient) CreateNote(ctx context.Context, n Note) (Note, error) {
	c.beforeNote()
	return c.fakeClient.CreateNote(ctx, n)
}

func TestSyncCompactsStateOnceForManyProjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	write(t, path, `{"notes":{"legacy":{"joplin_id":"obsolete"}},"projects":{}}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f := newFake()
	chats := []Chat{}
	for i := 0; i < 100; i++ {
		for j := 0; j < 2; j++ {
			chats = append(chats, Chat{ID: fmt.Sprintf("c%d-%d", i, j), ProjectID: fmt.Sprintf("p%d", i)})
		}
	}
	client := checkpointClient{f, func() {
		current, err := os.Stat(path)
		if err != nil || !os.SameFile(before, current) {
			t.Fatal("state rewritten before final compaction", err)
		}
		recovered, err := loadState(path)
		if err != nil || len(recovered.Projects) != len(f.folders)-1 {
			t.Fatal("folder not checkpointed before note", err)
		}
	}}
	r, err := Synchronize(context.Background(), client, chats, "Knowledge", path)
	if err != nil || r.Created != 200 || r.FoldersCreated != 100 {
		t.Fatal(r, err)
	}
	after, err := os.Stat(path)
	if err != nil || os.SameFile(before, after) {
		t.Fatal("state not atomically compacted", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(b), `"notes"`) {
		t.Fatal("legacy note cache retained", err)
	}
	files, err := projectCheckpointFiles(path)
	if err != nil || len(files) != 0 {
		t.Fatal("checkpoints not cleared", err)
	}
	r = syncTest(t, f, chats, path)
	if r.Unchanged != 200 || r.FoldersCreated != 0 {
		t.Fatal(r)
	}
}

func TestSyncCheckpointSurvivesProcessExit(t *testing.T) {
	if path := os.Getenv("IMPORTER_TEST_CRASH_STATE"); path != "" {
		c := checkpointClient{newFake(), func() { os.Exit(23) }}
		_, err := Synchronize(context.Background(), c, []Chat{{ID: "c", ProjectID: "p"}}, "Knowledge", path)
		t.Fatal("did not reach checkpoint", err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSyncCheckpointSurvivesProcessExit$")
	cmd.Env = append(os.Environ(), "IMPORTER_TEST_CRASH_STATE="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child: %s %v", output, err)
	}
	// Simulate documented manual lock recovery after confirming child exit.
	if err = os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.folders = append(f.folders, Folder{ID: "f1", Title: "ChatGPT project p", ParentID: "root"})
	r := syncTest(t, f, []Chat{{ID: "c", ProjectID: "p"}}, path)
	if r.FoldersCreated != 0 || r.Created != 1 || f.notes[0].ParentID != "f1" {
		t.Fatal(r)
	}
}

func TestSyncRetainsCheckpointsWhenCompactionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := newFake()
	c := checkpointClient{f, func() {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	chats := []Chat{{ID: "c", ProjectID: "p"}}
	if _, err := Synchronize(context.Background(), c, chats, "Knowledge", path); err == nil {
		t.Fatal("expected compaction failure")
	}
	files, err := projectCheckpointFiles(path)
	if err != nil || len(files) != 1 {
		t.Fatal("checkpoint lost", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r := syncTest(t, f, chats, path)
	if r.FoldersCreated != 0 || r.Unchanged != 1 {
		t.Fatal(r)
	}
}

func TestStateCheckpointReplayAfterCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	p := projectState{ID: "f", Title: "Project"}
	if err := checkpointProject(path, "p", p); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveState(path, s); err != nil {
		t.Fatal(err)
	}
	// A crash between compaction and cleanup leaves a replayable checkpoint.
	s, err = loadState(path)
	if err != nil || s.Projects["p"] != p {
		t.Fatal(s, err)
	}
}

func TestSyncRejectsCorruptCheckpointBeforeJoplin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.Mkdir(path+".projects", 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(path+".projects", checkpointName("p")), `{"project_id":`)
	if _, err := Synchronize(context.Background(), nil, nil, "root", path); err == nil {
		t.Fatal("accepted corrupt checkpoint")
	}
}

type failedCheckpointClient struct {
	*fakeClient
	path string
}

func (c failedCheckpointClient) CreateFolder(ctx context.Context, f Folder) (Folder, error) {
	folder, err := c.fakeClient.CreateFolder(ctx, f)
	if err != nil {
		return folder, err
	}
	// Simulate storage becoming unavailable after the Joplin POST succeeds.
	return folder, os.WriteFile(c.path+".projects", []byte("blocked"), 0600)
}
func TestSyncCheckpointFailureStopsBeforeNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := newFake()
	r, err := Synchronize(context.Background(), failedCheckpointClient{f, path}, []Chat{{ID: "c", ProjectID: "p"}}, "Knowledge", path)
	if err == nil || !strings.Contains(err.Error(), "folder f1") || r.FoldersCreated != 1 || len(f.notes) != 0 {
		t.Fatal(r, err)
	}
}
