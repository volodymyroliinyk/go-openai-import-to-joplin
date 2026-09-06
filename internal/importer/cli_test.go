package importer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDryRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conversations.json")
	write(t, p, fixture)
	state := filepath.Join(t.TempDir(), "absent", "state.json")
	for _, args := range [][]string{{p, "--dry-run", "--state", state}, {"--dry-run", p, "--state=" + state}} {
		var out, err bytes.Buffer
		code := Run(context.Background(), args, &out, &err, func(string) string { return "" })
		if code != 0 || !strings.Contains(out.String(), "1 conversations in 1 projects") {
			t.Fatalf("%d %s %s", code, &out, &err)
		}
		if _, e := os.Stat(state); !os.IsNotExist(e) {
			t.Fatal("dry run wrote state")
		}
	}
}
func TestCLIExitCodes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 2}, {[]string{"--help"}, 0}, {[]string{"file"}, 2}, {[]string{"--notebook"}, 2}, {[]string{"a", "b", "--dry-run"}, 2}, {[]string{"--unknown=secret"}, 2}, {[]string{"missing.json", "--dry-run"}, 1}} {
		var out, err bytes.Buffer
		code := Run(context.Background(), tc.args, &out, &err, func(string) string { return "" })
		if code != tc.code {
			t.Fatalf("%v: %d %s", tc.args, code, &err)
		}
		if strings.Contains(err.String(), "secret") {
			t.Fatal("secret echoed")
		}
	}
}

func TestCLILimits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conversations.json")
	write(t, p, fixture)
	for _, tc := range []struct {
		args    []string
		code    int
		message string
	}{
		{[]string{p, "--dry-run", "--limit", "file-bytes=1KiB"}, 0, "Parsed 1 conversations"},
		{[]string{p, "--dry-run", "--limit=file-bytes=10"}, 1, "export limit file-bytes exceeded"},
		{[]string{p, "--dry-run", "--limit=unknown=1"}, 2, "known NAME=VALUE"},
		{[]string{p, "--dry-run", "--limit=file-bytes=0"}, 2, "positive integer"},
	} {
		var out, err bytes.Buffer
		code := Run(context.Background(), tc.args, &out, &err, func(string) string { return "" })
		combined := out.String() + err.String()
		if code != tc.code || !strings.Contains(combined, tc.message) {
			t.Fatalf("%v: code %d, output %q", tc.args, code, combined)
		}
	}
}

func TestCLIRejectsConflictingConversationIDsBeforeStateOrJoplin(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "conversations.json")
	state := filepath.Join(dir, "state", "state.json")
	write(t, p, `[{"id":"same","title":"First"},{"id":"same","title":"Second"}]`)

	var out, err bytes.Buffer
	code := Run(context.Background(), []string{
		p,
		"--joplin-token=secret",
		"--notebook=Knowledge",
		"--state=" + state,
		"--joplin-url=http://127.0.0.1:1",
	}, &out, &err, func(string) string { return "" })
	if code != 1 || !strings.Contains(err.String(), `conflicting duplicate conversation ID "same"`) || !strings.Contains(err.String(), "no Joplin or state changes") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), err.String())
	}
	if strings.Contains(err.String(), "secret") {
		t.Fatal("token echoed")
	}
	if _, statErr := os.Stat(filepath.Dir(state)); !os.IsNotExist(statErr) {
		t.Fatal("conflicting export touched state", statErr)
	}
}

func TestCLIInsecureHTTPPolicy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conversations.json")
	write(t, p, fixture)
	state := filepath.Join(t.TempDir(), "state.json")
	baseArgs := []string{p, "--joplin-token=secret", "--notebook=Knowledge", "--state=" + state, "--joplin-url=http://192.0.2.1:41184"}

	var out, err bytes.Buffer
	if code := Run(context.Background(), baseArgs, &out, &err, func(string) string { return "" }); code != 1 || !strings.Contains(err.String(), "--allow-insecure-http") || strings.Contains(err.String(), "secret") {
		t.Fatalf("unsafe endpoint was not rejected safely: code=%d stderr=%q", code, err.String())
	}
	if _, statErr := os.Stat(state + ".lock"); !os.IsNotExist(statErr) {
		t.Fatal("transport policy created state lock", statErr)
	}

	out.Reset()
	err.Reset()
	args := append(append([]string{}, baseArgs...), "--allow-insecure-http")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := Run(ctx, args, &out, &err, func(string) string { return "" }); code != 1 || !strings.Contains(err.String(), "warning: sending the Joplin token over insecure HTTP") || strings.Contains(err.String(), "secret") {
		t.Fatalf("override warning missing: code=%d stderr=%q", code, err.String())
	}
}
