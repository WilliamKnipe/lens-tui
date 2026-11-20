package files

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// Match is one ripgrep hit. Start/End are byte offsets of the first match in Text.
type Match struct {
	Path       string
	Line       int
	Text       string
	Start, End int
}

// Search finds query in the files under root as a fixed string with smart
// case (case-insensitive unless the query has an upper-case letter). It uses
// ripgrep when installed and a built-in search otherwise.
func Search(ctx context.Context, root, query string, limit int) ([]Match, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return grep(ctx, root, query, limit)
	}
	return ripgrep(ctx, root, query, limit)
}

// SearchEngine says which engine Search will use.
func SearchEngine() string {
	if _, err := exec.LookPath("rg"); err != nil {
		return "builtin"
	}
	return "ripgrep"
}

func ripgrep(ctx context.Context, root, query string, limit int) ([]Match, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rg", "--json", "--smart-case", "--fixed-strings",
		"--hidden", "--glob", "!.git", "--max-columns", "400", "--max-count", "50", "--", query)
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var res []Match
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if m, ok := parseRgLine(sc.Bytes()); ok {
			res = append(res, m)
			if len(res) >= limit {
				break
			}
		}
	}
	cancel() // stop rg early if we hit the limit
	_ = cmd.Wait()
	return res, nil
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       struct{ Text string } `json:"path"`
		Lines      struct{ Text string } `json:"lines"`
		LineNumber int                   `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

func parseRgLine(b []byte) (Match, bool) {
	var ev rgEvent
	if json.Unmarshal(b, &ev) != nil || ev.Type != "match" || ev.Data.Path.Text == "" {
		return Match{}, false
	}
	m := Match{
		Path: strings.TrimPrefix(ev.Data.Path.Text, "./"),
		Line: ev.Data.LineNumber,
		Text: strings.TrimRight(ev.Data.Lines.Text, "\r\n"),
	}
	if len(ev.Data.Submatches) > 0 {
		m.Start, m.End = ev.Data.Submatches[0].Start, ev.Data.Submatches[0].End
	}
	if m.End > len(m.Text) {
		m.Start, m.End = 0, 0
	}
	return m, true
}
