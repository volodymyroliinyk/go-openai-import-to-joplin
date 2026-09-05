package importer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	n, e := c.Notes(ctx)
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
		_, e := c.Notes(context.Background())
		s.Close()
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("unsafe error %v", e)
		}
		_, e = c.Notes(context.Background())
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("unsafe connection error %v", e)
		}
	}
}
func TestHTTPURLValidation(t *testing.T) {
	for _, u := range []string{"bad", "ftp://localhost", "http://user:secret@localhost", "http://localhost?token=secret"} {
		if _, e := NewClient("secret", u); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatalf("%s: %v", u, e)
		}
	}
}
