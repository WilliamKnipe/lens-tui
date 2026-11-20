package ui

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"github.com/sahilm/fuzzy"

	"github.com/williamknipe/lens-tui/internal/debug"
)

const (
	finderResults = 5
	recentPerList = 5
)

// recentItem is a row in the finder's empty-query list.
type recentItem struct {
	rel     string
	changed bool   // "recently changed" (else "recently viewed")
	mark    string // git status letter
	mod     time.Time
}

// finder is the Ctrl+P quick-open popup.
type finder struct {
	input   textinput.Model
	files   []string
	indexed bool
	results []fuzzy.Match
	recent  []recentItem // shown while the query is empty, picked with 1-9, 0
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

// showRecent is true while the query is empty, so the recent lists show.
func (f *finder) showRecent() bool {
	q, _ := splitLine(f.input.Value())
	return strings.TrimSpace(q) == ""
}

func (f *finder) move(d int) {
	n := len(f.results)
	if f.showRecent() {
		n = len(f.recent)
	}
	f.sel = clamp(f.sel+d, 0, n-1)
}

func (f *finder) picked() (string, bool) {
	if f.showRecent() {
		if f.sel < 0 || f.sel >= len(f.recent) {
			return "", false
		}
		return f.recent[f.sel].rel, true
	}
	if f.sel < 0 || f.sel >= len(f.results) {
		return "", false
	}
	return f.results[f.sel].Str, true
}

// digitIndex maps "1".."9" to 0..8 and "0" to 9; -1 for other keys.
func digitIndex(k string) int {
	if len(k) != 1 || k[0] < '0' || k[0] > '9' {
		return -1
	}
	if k[0] == '0' {
		return 9
	}
	return int(k[0] - '1')
}

// setRecent builds the empty-query lists: up to five changed files (newest
// first) and up to five recently viewed files.
func (f *finder) setRecent(changed, viewed []recentItem) {
	f.recent = f.recent[:0]
	f.recent = append(f.recent, changed[:min(len(changed), recentPerList)]...)
	f.recent = append(f.recent, viewed[:min(len(viewed), recentPerList)]...)
	f.sel = 0
}

func (f *finder) view(w int) string {
	f.input.SetWidth(w - 3)
	lines := []string{f.input.View(), sFaint.Render(strings.Repeat("─", w))}
	q, _ := splitLine(f.input.Value())
	switch {
	case strings.TrimSpace(q) == "" && len(f.recent) > 0:
		lines = append(lines, f.recentRows(w)...)
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

// recentRows renders the numbered "recently changed" / "recently viewed"
// sections.
func (f *finder) recentRows(w int) []string {
	var lines []string
	now := time.Now()
	for i, it := range f.recent {
		if i == 0 || it.changed != f.recent[i-1].changed {
			if i > 0 {
				lines = append(lines, "")
			}
			title := "Recently viewed"
			if it.changed {
				title = "Recently changed"
			}
			lines = append(lines, sDim.Render("  "+title))
		}
		sel := i == f.sel
		prefix := "  "
		if sel {
			prefix = sAccent.Render("› ")
		}
		num := sAccent.Render(strconv.Itoa((i + 1) % 10))
		dir, name := path.Split(it.rel)
		if sel {
			name = sBold.Render(name)
		}
		left := prefix + num + "  " + name
		if dir != "" {
			left += "  " + sDim.Render(dir)
		}
		var right string
		if it.changed {
			// committed files have no status letter, just the age
			right = sDim.Render(fmt.Sprintf("%4s", ago(now.Sub(it.mod))))
			if it.mark != "" {
				right = gitStyle(it.mark).Render(it.mark) + " " + right
			}
		} else if it.mark != "" {
			right = gitStyle(it.mark).Render(it.mark)
		}
		rw := ansiWidth(right)
		lines = append(lines, fit(left, w-rw-1)+" "+right)
	}
	return lines
}

// ago formats a duration as a short age: "now", "12s", "3m", "2h", "4d".
func ago(d time.Duration) string {
	switch {
	case d < 2*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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
