package importer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateLockProcess(t *testing.T) {
	if path := os.Getenv("IMPORTER_TEST_LOCK_PATH"); path != "" {
		_, unlock, err := lockState(path)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	path := filepath.Join(t.TempDir(), "state.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStateLockProcess$")
	cmd.Env = append(os.Environ(), "IMPORTER_TEST_LOCK_PATH="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child lock: %q %v", line, err)
	}
	// A nil client would panic if the competing import reached Joplin.
	_, err = Synchronize(context.Background(), nil, nil, "root", path)
	if err == nil || !strings.Contains(err.Error(), "state is locked") {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("competing import wrote state", err)
	}
	_ = in.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	syncTest(t, f, []Chat{{ID: "c"}}, path)
	if _, err = os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock not released", err)
	}
}

func TestSyncReleasesLockOnFailureAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	f := newFake()
	f.failNote = true
	if _, err := Synchronize(context.Background(), f, []Chat{{ID: "c"}}, "Knowledge", path); err == nil {
		t.Fatal("expected failure")
	}
	f.failNote = false
	syncTest(t, f, []Chat{{ID: "c"}}, path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Synchronize(ctx, f, nil, "Knowledge", path); err != context.Canceled {
		t.Fatal(err)
	}
	syncTest(t, f, nil, path)
}

type cancelFoldersClient struct {
	*fakeClient
	cancel context.CancelFunc
}

func (c cancelFoldersClient) Folders(ctx context.Context) ([]Folder, error) {
	c.cancel()
	return c.fakeClient.Folders(ctx)
}
func TestSyncCancellationReleasesActiveLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFake()
	_, err := Synchronize(ctx, cancelFoldersClient{f, cancel}, []Chat{{ID: "c"}}, "Knowledge", path)
	if err != context.Canceled {
		t.Fatal(err)
	}
	if len(f.notes) != 0 {
		t.Fatal("wrote after cancellation")
	}
	syncTest(t, f, []Chat{{ID: "c"}}, path)
}
func TestStateLockDirectoryAlias(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skip(err)
	}
	_, unlock, err := lockState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = Synchronize(context.Background(), nil, nil, "root", filepath.Join(alias, "state.json"))
	if err == nil || !strings.Contains(err.Error(), "state is locked") {
		t.Fatal(err)
	}
}
