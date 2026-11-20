package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/williamknipe/lens-tui/internal/debug"
	"github.com/williamknipe/lens-tui/internal/files"
)

const searchLimit = 500

type searchTickMsg struct{ seq int }

type searchResultMsg struct {
	seq     int
	results []files.Match
	err     error
}

// searcher is the Ctrl+F "search in files" popup, backed by ripgrep.
type searcher struct {
	input   textinput.Model
	results []files.Match
	sel     int
	off     int
	seq     int
	running bool
	err     error
	rows    int // visible result rows
	cancel  context.CancelFunc
}

func newSearcher() *searcher {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "Search in files (smart case)"
	return &searcher{input: in}
}

// changed is called after each keystroke; it debounces the actual search.
func (s *searcher) changed() tea.Cmd {
	s.seq++
	seq := s.seq
	if len([]rune(s.input.Value())) < 2 {
		s.results, s.err, s.running = nil, nil, false
		return nil
	}
	s.running = true
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return searchTickMsg{seq} })
}

func (s *searcher) run(root string) tea.Cmd {
	seq, q := s.seq, s.input.Value()
	if s.cancel != nil {
		s.cancel() // stop the previous, now stale, search
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	s.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		start := time.Now()
		res, err := files.Search(ctx, root, q, searchLimit)
		if err == nil {
			debug.Since("search", start, 500*time.Millisecond, false, fmt.Sprintf("%s  %d results", files.SearchEngine(), len(res)))
		}
		return searchResultMsg{seq, res, err}
	}
}

func (s *searcher) setResults(msg searchResultMsg) {
	if msg.seq != s.seq {
		return // stale
	}
	if msg.err == context.Canceled || msg.err == context.DeadlineExceeded {
		msg.err = nil // keep whatever was found
	}
	s.results, s.err, s.running = msg.results, msg.err, false
	s.sel, s.off = 0, 0
}

func (s *searcher) move(d int) {
	s.sel = clamp(s.sel+d, 0, len(s.results)-1)
	if s.sel < s.off {
		s.off = s.sel
	}
	if s.sel >= s.off+s.rows {
		s.off = s.sel - s.rows + 1
	}
}

func (s *searcher) picked() (files.Match, bool) {
	if s.sel < 0 || s.sel >= len(s.results) {
		return files.Match{}, false
	}
	return s.results[s.sel], true
}

func (s *searcher) view(w, h int) string {
	s.rows = max(3, h-4)
	s.input.SetWidth(w - 3)
	status := ""
	switch {
	case s.err != nil:
		status = sDel.Render(s.err.Error())
	case s.running:
		status = sDim.Render("searching…")
	case len([]rune(s.input.Value())) < 2:
		status = sDim.Render("type at least 2 characters")
	case len(s.results) == 0:
		status = sDim.Render("no matches")
	case len(s.results) >= searchLimit:
		status = sDim.Render(fmt.Sprintf("first %d matches", searchLimit))
	default:
		status = sDim.Render(fmt.Sprintf("%d matches", len(s.results)))
	}
	lines := []string{s.input.View(), sFaint.Render(strings.Repeat("─", w-ansiWidth(status)-1)) + " " + status}
	for i := s.off; i < s.off+s.rows; i++ {
		if i >= len(s.results) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, s.row(s.results[i], i == s.sel, w))
	}
	return box(lines, w)
}

func (s *searcher) row(m files.Match, selected bool, w int) string {
	loc := fmt.Sprintf("%s:%d", m.Path, m.Line)
	// trim indentation, keeping the match offsets in step
	text := strings.ReplaceAll(m.Text, "\t", " ")
	trimmed := strings.TrimLeft(text, " ")
	cut := len(text) - len(trimmed)
	var body string
	if m.End > m.Start && m.Start >= cut {
		body = highlightRanges(trimmed, [][2]int{{m.Start - cut, m.End - cut}}, -1)
	} else {
		body = trimmed
	}
	prefix := "  "
	locStyled := sDim.Render(loc)
	if selected {
		prefix = sAccent.Render("› ")
		locStyled = sAccent.Render(loc)
	}
	return fit(prefix+locStyled+"  "+body, w)
}
