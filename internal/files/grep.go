package files

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
)

// grep is the fallback for Search when ripgrep is not installed. It scans the
// same files as the Ctrl+P index, so .gitignore is respected in git repos.
func grep(ctx context.Context, root, query string, limit int) ([]Match, error) {
	fold := strings.ToLower(query) == query
	q := []byte(query)
	if fold {
		q = bytes.ToLower(q)
	}
	var res []Match
	for _, rel := range ListFiles(root) {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || len(data) > maxHighlightSize || IsBinary(data) {
			continue
		}
		hay := data
		if fold {
			hay = bytes.ToLower(data)
			if len(hay) != len(data) { // non-ASCII case change shifted offsets
				hay = data
			}
		}
		if !bytes.Contains(hay, q) {
			continue
		}
		perFile := 0
		lineNo, start := 1, 0
		for start <= len(data) && perFile < 50 {
			end := bytes.IndexByte(data[start:], '\n')
			if end < 0 {
				end = len(data)
			} else {
				end += start
			}
			if i := bytes.Index(hay[start:end], q); i >= 0 {
				text := strings.TrimRight(string(data[start:end]), "\r")
				m := Match{Path: rel, Line: lineNo, Text: text, Start: i, End: i + len(q)}
				if m.End > len(text) {
					m.Start, m.End = 0, 0
				}
				res = append(res, m)
				perFile++
				if len(res) >= limit {
					return res, nil
				}
			}
			lineNo++
			start = end + 1
		}
	}
	return res, nil
}
