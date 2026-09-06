package importer

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// Limits are resource budgets, never filters. A violation aborts the entire Load.
type Limits map[string]int64

func DefaultLimits() Limits {
	return Limits{
		"json-bytes": 1 << 30, "file-bytes": 256 << 20,
		"string-bytes": 16 << 20, "render-bytes": 512 << 20,
		"json-values": 2_000_000, "depth": 256, "entries": 100_000,
		"zip-index-bytes": 64 << 20, "conversations": 100_000,
		"messages": 100_000, "parts": 100_000,
		"id-bytes": 4096, "title-bytes": 65536,
	}
}

func (l Limits) set(option string) error {
	name, value, ok := strings.Cut(option, "=")
	if _, known := l[name]; !ok || !known {
		return fmt.Errorf("--limit requires a known NAME=VALUE (see README)")
	}
	multiplier := int64(1)
	if strings.HasSuffix(name, "bytes") {
		for _, unit := range []struct {
			suffix string
			size   int64
		}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}} {
			if strings.HasSuffix(value, unit.suffix) {
				value = strings.TrimSuffix(value, unit.suffix)
				multiplier = unit.size
				break
			}
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/multiplier {
		return fmt.Errorf("--limit %s requires a positive integer; byte limits also accept KiB, MiB, GiB", name)
	}
	l[name] = n * multiplier
	return nil
}

type exportLimitError struct {
	name     string
	limit    int64
	observed uint64
}

func (e *exportLimitError) Error() string {
	return fmt.Sprintf("export limit %s exceeded: observed at least %d, limit %d; increase --limit %s=VALUE and retry the whole export (no chats skipped)", e.name, e.observed, e.limit, e.name)
}
func limitError(name string, limit int64, observed uint64) error {
	return &exportLimitError{name, limit, observed}
}
func (l Limits) check(name string, observed uint64) error {
	if observed > uint64(l[name]) {
		return limitError(name, l[name], observed)
	}
	return nil
}

type exportBudget struct {
	limits                                 Limits
	bytes, values, conversations, rendered int64
}

// Scan before handing bytes to encoding/json: depth, token count and raw JSON
// string length are bounded before Decode can allocate an entire object graph.
// JSON syntax itself is still validated by encoding/json.
type guardedJSON struct {
	r                             io.Reader
	budget                        *exportBudget
	fileBytes, depth, stringBytes int64
	inString, escaped, scalar     bool
	err                           error
}

func (g *guardedJSON) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	if len(p) > 32768 {
		p = p[:32768]
	}
	remaining := min(g.budget.limits["file-bytes"]-g.fileBytes, g.budget.limits["json-bytes"]-g.budget.bytes)
	if int64(len(p)) > remaining {
		p = p[:min(int64(len(p)), remaining+1)]
	}
	n, err := g.r.Read(p)
	if e := g.budget.limits.check("file-bytes", uint64(g.fileBytes)+uint64(n)); e != nil {
		g.err = e
		return 0, e
	}
	if e := g.budget.limits.check("json-bytes", uint64(g.budget.bytes)+uint64(n)); e != nil {
		g.err = e
		return 0, e
	}
	g.fileBytes += int64(n)
	g.budget.bytes += int64(n)
	for _, c := range p[:n] {
		if g.inString {
			if c == '"' && !g.escaped {
				g.inString = false
				continue
			}
			g.stringBytes++
			if e := g.budget.limits.check("string-bytes", uint64(g.stringBytes)); e != nil {
				g.err = e
				return 0, e
			}
			if g.escaped {
				g.escaped = false
			} else if c == '\\' {
				g.escaped = true
			}
			continue
		}
		newValue := false
		switch c {
		case '"':
			g.inString = true
			g.stringBytes = 0
			g.scalar = false
			newValue = true
		case '{', '[':
			g.depth++
			g.scalar = false
			newValue = true
		case '}', ']':
			g.depth--
			g.scalar = false
		case ' ', '\n', '\r', '\t', ',', ':':
			g.scalar = false
		default:
			if !g.scalar {
				newValue = true
				g.scalar = true
			}
		}
		if g.depth > g.budget.limits["depth"] {
			g.err = limitError("depth", g.budget.limits["depth"], uint64(g.depth))
			return 0, g.err
		}
		if newValue {
			if g.budget.values == g.budget.limits["json-values"] {
				g.err = limitError("json-values", g.budget.limits["json-values"], uint64(g.budget.values)+1)
				return 0, g.err
			}
			g.budget.values++
		}
	}
	return n, err
}

// Bound central-directory reads during ZIP initialization, before archive/zip
// can allocate metadata for arbitrarily many entries. Entry payload reads are
// guarded separately; binary attachments are not read or charged as JSON.
type guardedZIPIndex struct {
	r           io.ReaderAt
	limit, used int64
	active      bool
	err         error
}

func (g *guardedZIPIndex) ReadAt(p []byte, off int64) (int, error) {
	if !g.active {
		return g.r.ReadAt(p, off)
	}
	if g.err != nil {
		return 0, g.err
	}
	if int64(len(p)) > g.limit-g.used {
		g.err = limitError("zip-index-bytes", g.limit, uint64(g.used)+uint64(len(p)))
		return 0, g.err
	}
	n, err := g.r.ReadAt(p, off)
	g.used += int64(n)
	return n, err
}

func checkNames(l Limits, id, title string) error {
	if err := l.check("id-bytes", uint64(len(id))); err != nil {
		return err
	}
	return l.check("title-bytes", uint64(len(title)))
}

func openExportJSON(path string, limits Limits) (io.ReadCloser, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: expected a regular JSON file", path)
	}
	if err := limits.check("file-bytes", uint64(info.Size())); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return os.Open(path)
}
