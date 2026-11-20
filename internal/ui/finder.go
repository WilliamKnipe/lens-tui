package ui

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"github.com/sahilm/fuzzy"

	"github.com/williamknipe/lens/internal/debug"
)

const finderResults = 5

// finder is the Ctrl+P quick-open popup.
type finder struct {
	input   textinput.Model
	files   []string
	indexed bool
	results []fuzzy.Match
	sel     int
	line    int // from a "file:42" suffix
}

func newFinder() *finder {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "Search files by name (add :42 to jump to a line)"
	return &finder{input: in}
}

func (f *finder) setFiles(list []string) {
	f.files, f.indexed = list, true
	f.refilter()
}

// splitLine separates a trailing ":123" line number from the query.
func splitLine(q string) (string, int) {
	if i := strings.LastIndexByte(q, ':'); i >= 0 {
		if n, err := strconv.Atoi(q[i+1:]); err == nil {
			return q[:i], n
		}
		if q[i+1:] == "" {
			return q[:i], 0
		}
	}
	return q, 0
}

func (f *finder) refilter() {
	q, line := splitLine(f.input.Value())
	q = strings.ReplaceAll(q, " ", "")
	f.line = line
	f.sel = 0
	if q == "" {
		f.results = nil
		return
	}
	start := time.Now()
	res := fuzzy.Find(q, f.files)
	debug.Since("fuzzy", start, 30*time.Millisecond, true, fmt.Sprintf("%d files", len(f.files)))
	if len(res) > finderResults {
		res = res[:finderResults]
	}
	f.results = res
}

func (f *finder) move(d int) {
	f.sel = clamp(f.sel+d, 0, len(f.results)-1)
}

func (f *finder) picked() (string, bool) {
	if f.sel < 0 || f.sel >= len(f.results) {
		return "", false
	}
	return f.results[f.sel].Str, true
}

func (f *finder) view(w int) string {
	f.input.SetWidth(w - 3)
	lines := []string{f.input.View(), sFaint.Render(strings.Repeat("─", w))}
	q, _ := splitLine(f.input.Value())
	switch {
	case !f.indexed:
		lines = append(lines, sDim.Render("  indexing…"))
	case strings.TrimSpace(q) == "":
		lines = append(lines, sDim.Render(fmt.Sprintf("  %d files", len(f.files))))
	case len(f.results) == 0:
		lines = append(lines, sDim.Render("  no matches"))
	}
	for i, r := range f.results {
		lines = append(lines, f.row(r, i == f.sel, w))
	}
	for len(lines) < finderResults+2 {
		lines = append(lines, "")
	}
	return box(lines, w)
}

// row shows "name  dir/" with the fuzzy-matched characters highlighted.
func (f *finder) row(r fuzzy.Match, selected bool, w int) string {
	hit := make(map[int]bool, len(r.MatchedIndexes))
	for _, i := range r.MatchedIndexes {
		hit[i] = true
	}
	dir, name := path.Split(r.Str)
	paint := func(s string, offset int, base func(string) string) string {
		var b strings.Builder
		for i, c := range s {
			if hit[offset+i] {
				b.WriteString(sFuzzyHit.Render(string(c)))
			} else {
				b.WriteString(base(string(c)))
			}
		}
		return b.String()
	}
	plain := func(s string) string { return s }
	if selected {
		plain = func(s string) string { return sBold.Render(s) }
	}
	dim := func(s string) string { return sDim.Render(s) }
	prefix := "  "
	if selected {
		prefix = sAccent.Render("› ")
	}
	out := prefix + paint(name, len(dir), plain)
	if dir != "" {
		out += "  " + paint(dir, 0, dim)
	}
	return fit(out, w)
}
