package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Folder struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parent_id"`
}
type Note struct {
	ID        string `json:"id,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ParentID  string `json:"parent_id"`
	Source    string `json:"source"`
	SourceURL string `json:"source_url"`
	CreatedMS int64  `json:"user_created_time,omitempty"`
	UpdatedMS int64  `json:"user_updated_time,omitempty"`
}
type Client interface {
	Folders(context.Context) ([]Folder, error)
	MarkerNotes(context.Context) ([]Note, error)
	CreateFolder(context.Context, Folder) (Folder, error)
	UpdateFolder(context.Context, Folder) error
	CreateNote(context.Context, Note) (Note, error)
	UpdateNote(context.Context, Note) error
}
type HTTPClient struct {
	base  *url.URL
	token string
	http  *http.Client
}

func NewClient(token, base string) (*HTTPClient, error) {
	return newClient(token, base, false)
}

func newClient(token, base string, allowInsecureHTTP bool) (*HTTPClient, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid Joplin URL: use an HTTP(S) URL without credentials, query, or fragment")
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) && !allowInsecureHTTP {
		return nil, fmt.Errorf("refusing to send the Joplin token over HTTP to a non-loopback host; use HTTPS or explicitly pass --allow-insecure-http")
	}
	return &HTTPClient{u, token, &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (c *HTTPClient) request(ctx context.Context, method, endpoint string, q url.Values, payload, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + endpoint
	if q == nil {
		q = url.Values{}
	}
	q.Set("token", c.token)
	u.RawQuery = q.Encode()
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return fmt.Errorf("encode Joplin request: %w", e)
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return fmt.Errorf("cannot construct Joplin request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("cannot connect to Joplin (check URL and Web Clipper availability)")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Joplin API %s %s: HTTP %d", method, endpoint, resp.StatusCode)
	}
	if out == nil {
		_, e = io.Copy(io.Discard, resp.Body)
		return e
	}
	if e = json.NewDecoder(resp.Body).Decode(out); e != nil {
		return fmt.Errorf("invalid Joplin response JSON")
	}
	return nil
}
func pages[T any](ctx context.Context, c *HTTPClient, endpoint, fields string, query url.Values) ([]T, error) {
	result := []T{}
	if query == nil {
		query = url.Values{}
	}
	query.Set("limit", "100")
	query.Set("fields", fields)
	for page := 1; ; page++ {
		var p struct {
			Items   *[]T `json:"items"`
			HasMore bool `json:"has_more"`
		}
		query.Set("page", strconv.Itoa(page))
		if e := c.request(ctx, "GET", endpoint, query, nil, &p); e != nil {
			return nil, e
		}
		if p.Items == nil {
			return nil, fmt.Errorf("Joplin returned a page without items")
		}
		result = append(result, (*p.Items)...)
		if !p.HasMore {
			return result, nil
		}
		if len(*p.Items) == 0 {
			return nil, fmt.Errorf("Joplin returned empty page with has_more")
		}
	}
}
func (c *HTTPClient) Folders(ctx context.Context) ([]Folder, error) {
	return pages[Folder](ctx, c, "/folders", "id,title,parent_id", nil)
}
func (c *HTTPClient) MarkerNotes(ctx context.Context) ([]Note, error) {
	// Basic search preserves marker punctuation and searches raw note text.
	// Do not restrict by source or notebook: legacy and moved notes must count,
	// and all duplicate identities must be detected before any writes.
	return pages[Note](ctx, c, "/search", "id,title,body,parent_id,source,source_url,user_created_time,user_updated_time",
		url.Values{"query": {`/"chatgpt-conversation-id:"`}, "type": {"note"}})
}
func (c *HTTPClient) CreateFolder(ctx context.Context, f Folder) (Folder, error) {
	var out Folder
	e := c.request(ctx, "POST", "/folders", nil, f, &out)
	if e == nil && out.ID == "" {
		e = fmt.Errorf("Joplin returned folder without ID")
	}
	return out, e
}
func (c *HTTPClient) UpdateFolder(ctx context.Context, f Folder) error {
	return c.request(ctx, "PUT", "/folders/"+url.PathEscape(f.ID), nil, f, nil)
}
func (c *HTTPClient) CreateNote(ctx context.Context, n Note) (Note, error) {
	var out Note
	e := c.request(ctx, "POST", "/notes", nil, n, &out)
	if e == nil && out.ID == "" {
		e = fmt.Errorf("Joplin returned note without ID")
	}
	return out, e
}
func (c *HTTPClient) UpdateNote(ctx context.Context, n Note) error {
	return c.request(ctx, "PUT", "/notes/"+url.PathEscape(n.ID), nil, n, nil)
}
