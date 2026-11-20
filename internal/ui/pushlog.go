package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/williamknipe/lens-tui/internal/files"
)

const pushLogLines = 60

// pushPopup is the "P" popup: the commit graph with unpushed commits
// highlighted, the selected commit's files, and enter to push.
type pushPopup struct {
	lines    []files.LogLine
	sel      int
	off      int
	listH    int
	loaded   bool
	args     []string // git push arguments
	target   string   // e.g. "origin/main" or "-u origin main"
	stat     []string
	statHash string
	busy     bool
	err      string
}

type (
	pushLogMsg struct {
		lines  []files.LogLine
		args   []string
		target string
	}
	pushStatMsg struct {
		hash  string
		lines []string
	}
	pushDoneMsg struct {
		target string
		n      int
		err    error
	}
)

func (p *pushPopup) setLog(msg pushLogMsg) {
	p.lines, p.args, p.target, p.loaded = msg.lines, msg.args, msg.target, true
	p.sel, p.off = -1, 0
	for i, l := range p.lines {
		if l.Hash != "" {
			p.sel = i
			break
		}
	}
}

func (p *pushPopup) selected() (files.LogLine, bool) {
	if p.sel < 0 || p.sel >= len(p.lines) {
		return files.LogLine{}, false
	}
	return p.lines[p.sel], true
}

// move selects the next or previous commit line, skipping graph-only lines.
func (p *pushPopup) move(d int) {
	for i := p.sel + d; i >= 0 && i < len(p.lines); i += d {
		if p.lines[i].Hash != "" {
			p.sel = i
			break
		}
	}
	if p.sel < p.off {
		p.off = p.sel
	}
	if p.listH > 0 && p.sel >= p.off+p.listH {
		p.off = p.sel - p.listH + 1
	}
}

func (p *pushPopup) unpushed() int {
	n := 0
	for _, l := range p.lines {
		if l.Unpushed {
			n++
		}
	}
	return n
}

// ---- model side ----

func (m *Model) openPush() tea.Cmd {
	if !m.git.OK {
		return m.setFlash("not a git repository")
	}
	m.mode = modePush
	p := m.push
	p.err, p.busy, p.loaded, p.stat, p.statHash = "", false, false, nil, ""
	g, st := m.git, m.status
	return func() tea.Msg {
		args, target := g.PushTarget(st)
		return pushLogMsg{g.Log(pushLogLines, st), args, target}
	}
}

func (m *Model) pushStatCmd() tea.Cmd {
	l, ok := m.push.selected()
	if !ok || l.Hash == m.push.statHash {
		return nil
	}
	g, hash := m.git, l.Hash
	return func() tea.Msg { return pushStatMsg{hash, g.ShowStat(hash)} }
}

func (p *pushPopup) setStat(msg pushStatMsg) {
	if l, ok := p.selected(); ok && l.Hash == msg.hash {
		p.stat, p.statHash = msg.lines, msg.hash
	}
}

func (m *Model) keyPush(msg tea.KeyPressMsg) tea.Cmd {
	p := m.push
	if p.busy {
		return nil
	}
	switch msg.String() {
	case "esc", "q", "P", "super+d":
		m.mode = modeNormal
	case "j", "down", "ctrl+n", "tab":
		p.move(1)
		return m.pushStatCmd()
	case "k", "up", "ctrl+p", "shift+tab":
		p.move(-1)
		return m.pushStatCmd()
	case "enter":
		if !p.loaded {
			return nil
		}
		if !m.git.HasRemote {
			p.err = "this repository has no remote to push to"
			return nil
		}
		n := p.unpushed()
		if n == 0 && m.status.Upstream {
			p.err = "nothing to push: " + p.target + " is up to date"
			return nil
		}
		p.busy, p.err = true, ""
		g, args, target := m.git, p.args, p.target
		return func() tea.Msg {
			_, err := g.Push(args)
			return pushDoneMsg{target, n, err}
		}
	}
	return nil
}

func (m *Model) handlePushDone(msg pushDoneMsg) tea.Cmd {
	p := m.push
	p.busy = false
	if msg.err != nil {
		p.err = msg.err.Error()
		return nil
	}
	m.mode = modeNormal
	m.statusGen++
	what := "pushed"
	if msg.n > 0 {
		what = fmt.Sprintf("pushed %d commit%s", msg.n, plural(msg.n))
	}
	return tea.Batch(m.refreshCmd(), m.committedCmd(), m.setFlash(what+" to "+strings.TrimPrefix(msg.target, "-u ")))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- view ----

func (p *pushPopup) view(w, h int, st files.Status) string {
	lines := make([]string, 0, h)
	n := p.unpushed()
	title := sAccent.Bold(true).Render("Push") + " " + sBold.Render(st.Branch)
	if p.target != "" {
		title += sDim.Render(" → " + p.target)
	} else if p.loaded {
		title += sDim.Render(" (no remote)")
	}
	var right string
	switch {
	case !p.loaded:
		right = sDim.Render("loading…")
	case n == 0:
		right = sDim.Render("nothing to push")
	default:
		right = sAccent.Render(fmt.Sprintf("↑%d to push", n))
	}
	if st.Behind > 0 {
		right += lipgloss.NewStyle().Foreground(colYellow).Render(fmt.Sprintf("  ↓%d behind: pull first", st.Behind))
	}
	lines = append(lines, spread(title, right, w))
	lines = append(lines, sFaint.Render(strings.Repeat("─", w)))

	footer := 2
	errLines := 0
	if p.err != "" {
		errLines = min(4, len(strings.Split(p.err, "\n")))
	}
	statH := clamp(len(p.stat)+1, 3, 8)
	p.listH = clamp(len(p.lines), 3, max(3, h-len(lines)-statH-1-footer-errLines))
	if p.sel >= p.off+p.listH {
		p.off = p.sel - p.listH + 1
	}
	for i := range p.listH {
		j := p.off + i
		if j >= len(p.lines) {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, p.row(p.lines[j], j == p.sel, w))
	}

	lines = append(lines, sFaint.Render(strings.Repeat("─", w)))
	if l, ok := p.selected(); ok {
		lines = append(lines, sAccent.Render(l.Hash)+" "+sBold.Render(l.Subject))
		for i := 0; i < statH-1; i++ {
			if i >= len(p.stat) {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, statRow(p.stat[i], i == statH-2 && len(p.stat) > statH-1, len(p.stat)-i))
		}
	} else {
		for range statH {
			lines = append(lines, "")
		}
	}

	if p.err != "" {
		for i, l := range strings.Split(p.err, "\n") {
			if i >= 4 {
				break
			}
			lines = append(lines, sDel.Render(l))
		}
	}
	lines = append(lines, sFaint.Render(strings.Repeat("─", w)))
	keys := "enter push · j/k select commit · esc close"
	if p.busy {
		keys = "pushing…"
	}
	lines = append(lines, sDim.Render(keys))
	return box(lines, w)
}

// row renders a log line: graph, hash, refs, subject and age. Unpushed
// commits are bright, pushed ones faint.
func (p *pushPopup) row(l files.LogLine, sel bool, w int) string {
	prefix := "  "
	if sel {
		prefix = sAccent.Render("› ")
	}
	graph := sDim.Render(l.Graph)
	if l.Hash == "" {
		return prefix + graph
	}
	hash, subject := sFaint.Render(l.Hash), sDim.Render(l.Subject)
	mark := " "
	if l.Unpushed {
		hash, subject, mark = sAccent.Render(l.Hash), l.Subject, sAccent.Render("↑")
		if sel {
			subject = sBold.Render(subject)
		}
	} else if sel {
		subject = l.Subject
	}
	left := prefix + mark + graph + hash + " "
	if l.Refs != "" {
		left += lipgloss.NewStyle().Foreground(colYellow).Render("("+l.Refs+")") + " "
	}
	left += subject
	ago := sFaint.Render(l.Ago)
	return spread(left, ago, w)
}

// statRow renders a `git show --numstat` line as "+12 −3 path".
func statRow(s string, last bool, remaining int) string {
	if last && remaining > 1 {
		return sDim.Render(fmt.Sprintf("  … %d more files", remaining))
	}
	f := strings.SplitN(s, "\t", 3)
	if len(f) < 3 {
		return sDim.Render(s)
	}
	add, del := f[0], f[1]
	if add == "-" {
		return "  " + sDim.Render("binary ") + f[2]
	}
	return "  " + sAdd.Render(fmt.Sprintf("%5s", "+"+add)) + " " + sDel.Render(fmt.Sprintf("%-5s", "−"+del)) + " " + f[2]
}
