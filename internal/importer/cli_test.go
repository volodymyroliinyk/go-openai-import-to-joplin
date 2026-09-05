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
