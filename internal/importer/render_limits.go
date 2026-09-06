package importer

import (
	"encoding/json"
	"strings"
)

type limitedText struct {
	strings.Builder
	limit int64
}

func (b *limitedText) add(s string) error {
	n := uint64(b.Len()) + uint64(len(s))
	if n > uint64(b.limit) {
		return limitError("render-bytes", b.limit, n)
	}
	_, err := b.WriteString(s)
	return err
}

// Indent incrementally: json.MarshalIndent would allocate all indentation
// before we could enforce the output budget on deeply nested structured parts.
func indentedPart(value map[string]any, limit int64) (string, error) {
	compact, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	out := limitedText{limit: limit}
	depth := 0
	quoted, escaped := false, false
	newline := func() error {
		if err := out.add("\n"); err != nil {
			return err
		}
		for i := 0; i < depth; i++ {
			if err := out.add("  "); err != nil {
				return err
			}
		}
		return nil
	}
	for i, c := range compact {
		if quoted {
			if err := out.add(string(compact[i : i+1])); err != nil {
				return "", err
			}
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			if err := out.add(string(compact[i : i+1])); err != nil {
				return "", err
			}
			depth++
			if i+1 < len(compact) && compact[i+1] != '}' && compact[i+1] != ']' {
				if err := newline(); err != nil {
					return "", err
				}
			}
			continue
		case '}', ']':
			depth--
			if i > 0 && compact[i-1] != '{' && compact[i-1] != '[' {
				if err := newline(); err != nil {
					return "", err
				}
			}
		case ',':
			if err := out.add(","); err != nil {
				return "", err
			}
			if err := newline(); err != nil {
				return "", err
			}
			continue
		case ':':
			if err := out.add(": "); err != nil {
				return "", err
			}
			continue
		}
		if err := out.add(string(compact[i : i+1])); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}
