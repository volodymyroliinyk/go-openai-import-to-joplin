package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPPaginationAndWrites(t *testing.T) {
	gets, puts, posts := 0, 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "secret" {
			t.Error("missing token")
		}
		switch r.Method {
		case "GET":
			gets++
			if r.URL.Query().Get("fields") == "" || r.URL.Query().Get("limit") != "100" {
				t.Error("missing query")
			}
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `{"items":[{"id":"a"}],"has_more":true}`)
			} else {
				fmt.Fprint(w, `{"items":[{"id":"b"}],"has_more":false}`)
			}
		case "PUT":
			puts++
			w.WriteHeader(204)
		case "POST":
			posts++
			fmt.Fprint(w, `{"id":"new"}`)
		}
	}))
	defer s.Close()
	c, e := NewClient("secret", s.URL)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	n, e := c.MarkerNotes(ctx)
	if e != nil || len(n) != 2 || gets != 2 {
		t.Fatalf("%v %v", n, e)
	}
	if _, e = c.Folders(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = c.CreateNote(ctx, Note{Title: "Title"}); e != nil {
		t.Fatal(e)
	}
	if e = c.UpdateNote(ctx, Note{ID: "a"}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.CreateFolder(ctx, Folder{Title: "Project"}); e != nil {
		t.Fatal(e)
	}
	if e = c.UpdateFolder(ctx, Folder{ID: "f"}); e != nil {
		t.Fatal(e)
	}
	if puts != 2 || posts != 2 {
		t.Fatal(puts, posts)
	}
}
func TestHTTPFailuresDoNotLeakToken(t *testing.T) {
	for _, status := range []int{200, 403, 302} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://example.invalid/?token=secret")
			w.WriteHeader(status)
			fmt.Fprint(w, "secret invalid response")
		}))
		c, _ := NewClient("secret", s.URL)
		_, e := c.MarkerNotes(context.Background())
		s.Close()
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("unsafe error %v", e)
		}
		_, e = c.MarkerNotes(context.Background())
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("unsafe connection error %v", e)
		}
	}
}
func TestHTTPURLValidation(t *testing.T) {
	for _, u := range []string{"bad", "ftp://localhost", "http://user:secret@localhost", "http://localhost?token=secret", "http://:41184"} {
		if _, e := NewClient("secret", u); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("%s: %v", u, e)
		}
	}
}

func TestHTTPTransportPolicy(t *testing.T) {
	for _, u := range []string{
		"http://localhost:41184", "http://LOCALHOST.:41184", "http://127.0.0.1:41184",
		"http://127.42.0.9:41184", "http://[::1]:41184", "https://joplin.example:41184",
	} {
		if _, err := NewClient("secret", u); err != nil {
			t.Errorf("rejected safe URL %q: %v", u, err)
		}
	}
	for _, u := range []string{"http://joplin.example:41184", "http://192.168.1.20:41184", "http://[2001:db8::1]:41184"} {
		if _, err := NewClient("secret", u); err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "--allow-insecure-http") {
			t.Errorf("did not safely reject %q: %v", u, err)
		}
		if _, err := newClient("secret", u, true); err != nil {
			t.Errorf("explicit override rejected for %q: %v", u, err)
		}
	}
}

// Exercise discovery through the real HTTP client, including unrelated notes,
// moved legacy imports, stale state, later-page duplicates, and partial errors.
func TestSyncHTTPMarkerDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		stale, duplicate, failPage, unchanged bool
	}{
		{name: "missing state and moved legacy note"},
		{name: "stale state", stale: true},
		{name: "unchanged reimport", unchanged: true},
		{name: "duplicate on later page", duplicate: true},
		{name: "later page failure", failPage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chats := []Chat{{ID: "c", Title: "Chat", Body: "Body"}}
			existing := Note{ID: "original", Title: "Chat", Body: "<!-- chatgpt-conversation-id: c -->\n\nBody", ParentID: "moved"}
			if tc.unchanged {
				existing.ParentID = "root"
				existing.Source = "chatgpt-import-to-joplin"
				existing.SourceURL = "https://chatgpt.com/c/c"
			}
			candidates := []Note{existing, {ID: "quote", Body: "Ordinary text\n<!-- chatgpt-conversation-id: c -->"}}
			if tc.duplicate {
				candidates[1] = Note{ID: "duplicate", Body: existing.Body, ParentID: "elsewhere"}
			}
			// These notes must never be transferred to the importer.
			unrelated := make([]Note, 10000)
			for i := range unrelated {
				unrelated[i] = Note{ID: fmt.Sprintf("private-%d", i), Body: strings.Repeat("private content ", 100)}
			}
			all := append(unrelated, candidates...)
			var searches, writes, returned atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/folders":
					fmt.Fprint(w, `{"items":[{"id":"root","title":"Knowledge"}],"has_more":false}`)
				case r.Method == "GET" && r.URL.Path == "/search":
					searches.Add(1)
					q := r.URL.Query()
					if q.Get("query") != `/"chatgpt-conversation-id:"` || q.Get("type") != "note" || q.Get("limit") != "100" || q.Get("fields") != "id,title,body,parent_id,source,source_url,user_created_time,user_updated_time" {
						t.Errorf("incorrect search parameters: query=%q type=%q fields=%q", q.Get("query"), q.Get("type"), q.Get("fields"))
						w.WriteHeader(400)
						return
					}
					page, err := strconv.Atoi(q.Get("page"))
					if err != nil || page < 1 || page > 2 {
						t.Error("unexpected page")
						w.WriteHeader(400)
						return
					}
					if tc.failPage && page == 2 {
						w.WriteHeader(500)
						return
					}
					matches := []Note{}
					for _, n := range all {
						if strings.Contains(n.Body, "chatgpt-conversation-id:") {
							matches = append(matches, n)
						}
					}
					// Smaller pages exercise pagination even for a small set of matches.
					returned.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"items": matches[page-1 : page], "has_more": page < len(matches)})
				case r.Method == "PUT" && r.URL.Path == "/notes/original":
					writes.Add(1)
					var n Note
					if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
						t.Error(err)
					}
					if n.ParentID != "root" || n.Body != existing.Body {
						t.Error("incorrect update", n)
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			c, err := NewClient("secret", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "state.json")
			if tc.stale {
				write(t, path, `{"notes":{"c":{"joplin_id":"private-0"}}}`)
			}
			result, err := Synchronize(context.Background(), c, chats, "Knowledge", path)
			if tc.duplicate || tc.failPage {
				if err == nil || writes.Load() != 0 {
					t.Fatal("did not stop before writes", result, err)
				}
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("state written", e)
				}
			} else if tc.unchanged {
				if err != nil || result.Unchanged != 1 || result.Created != 0 || writes.Load() != 0 {
					t.Fatal(result, err)
				}
			} else if err != nil || result.Updated != 1 || result.Created != 0 || writes.Load() != 1 {
				t.Fatal(result, err)
			}
			if searches.Load() != 2 || returned.Load() > 2 {
				t.Fatal("unexpected discovery cost", searches.Load(), returned.Load())
			}
		})
	}
}

func TestHTTPMarkerSearchRejectsIncompletePages(t *testing.T) {
	for _, response := range []string{`{}`, `{"items":null}`, `{"items":[],"has_more":true}`} {
		t.Run(response, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
			defer s.Close()
			c, _ := NewClient("secret", s.URL)
			if _, err := c.MarkerNotes(context.Background()); err == nil {
				t.Fatal("accepted incomplete search results")
			}
		})
	}
}
