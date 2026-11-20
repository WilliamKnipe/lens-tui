package ui

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/williamknipe/lens-tui/internal/files"
)

const commitInputRows = 3

// commitEntry is a row of the commit popup's file list.
type commitEntry struct {
	header   bool
	title    string
	rel      string
	staged   bool
	code     string
	add, del int // -1: binary / unknown
}

// commitPopup is the "c" popup: a message input above the files that will be
// committed (and the ones that won't), with a diff preview of the selected file.
type commitPopup struct {
	input     textarea.Model
	focusList bool
	amend     bool
	lastHash  string
	lastMsg   string
	draft     string // the message before amend filled in the last one

	status  files.Status // as of the last reload
	entries []commitEntry
	sel     int
	loaded  bool
	busy    bool
	err     string

	previewRel    string
	previewStaged bool
	preview       []string // plain diff lines, header dropped
	previewOff    int
	previewH      int
}

type (
	commitDataMsg struct {
		status   files.Status
		staged   map[string]files.Stat
		unstaged map[string]files.Stat
		lastHash string
		lastMsg  string
	}
	commitPreviewMsg struct {
		rel    string
		staged bool
		out    string
	}
	commitDoneMsg struct {
		hash  string
		amend bool
		ahead int
		err   error
	}
)

func newCommitPopup() *commitPopup {
	in := textarea.New()
	in.Prompt = ""
	in.ShowLineNumbers = false
	in.Placeholder = "Commit message: first line is the subject"
	in.CharLimit = 0
	in.KeyMap.LineStart = key.NewBinding(key.WithKeys("home")) // ctrl+a toggles amend
	st := in.Styles()
	st.Focused.CursorLine = lipgloss.NewStyle()
	st.Focused.Placeholder = sDim
	st.Blurred.Placeholder = sDim
	st.Blurred.Text = lipgloss.NewStyle()
	st.Blurred.CursorLine = lipgloss.NewStyle()
	in.SetStyles(st)
	in.SetHeight(commitInputRows)
	return &commitPopup{input: in}
}

// nothingStaged: enter commits every change (git add -A first).
func (c *commitPopup) nothingStaged() bool {
	return len(c.status.Staged) == 0
}

// reload fetches status and line counts in the background.
func (c *commitPopup) reload(g files.Git) tea.Cmd {
	return func() tea.Msg {
		st := g.Status()
		msg := commitDataMsg{status: st, staged: g.NumStat(true), unstaged: g.NumStat(false)}
		for p, code := range st.Unstaged {
			if code == "?" {
				msg.unstaged[p] = files.Stat{Add: g.CountLines(p)}
			}
		}
		msg.lastHash, msg.lastMsg = g.LastCommit()
		return msg
	}
}

func (c *commitPopup) setData(msg commitDataMsg) {
	keepRel, keepStaged := "", false
	if e, ok := c.selected(); ok {
		keepRel, keepStaged = e.rel, e.staged
	}
	c.status, c.lastHash, c.lastMsg, c.loaded = msg.status, msg.lastHash, msg.lastMsg, true
	c.entries = c.entries[:0]
	section := func(title string, staged bool, m map[string]string, stats map[string]files.Stat) {
		if len(m) == 0 {
			return
		}
		c.entries = append(c.entries, commitEntry{header: true, title: fmt.Sprintf("%s (%d)", title, len(m)), staged: staged})
		paths := make([]string, 0, len(m))
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			e := commitEntry{rel: p, staged: staged, code: m[p], add: -1, del: -1}
			if s, ok := stats[p]; ok {
				e.add, e.del = s.Add, s.Del
			}
			c.entries = append(c.entries, e)
		}
	}
	section("Staged", true, msg.status.Staged, msg.staged)
	title := "Changes"
	if c.nothingStaged() {
		title = "Changes · all will be committed"
	}
	section(title, false, msg.status.Unstaged, msg.unstaged)
	// stay on the same file: same section first, else where it moved to
	c.sel = -1
	best := 0
	for i, e := range c.entries {
		score := 1
		if e.rel == keepRel {
			score = 2
			if e.staged == keepStaged {
				score = 3
			}
		}
		if !e.header && score > best {
			c.sel, best = i, score
		}
	}
}

func (c *commitPopup) selected() (commitEntry, bool) {
	if c.sel < 0 || c.sel >= len(c.entries) || c.entries[c.sel].header {
		return commitEntry{}, false
	}
	return c.entries[c.sel], true
}

func (c *commitPopup) move(d int) {
	for i := c.sel + d; i >= 0 && i < len(c.entries); i += d {
		if !c.entries[i].header {
			c.sel = i
			return
		}
	}
}

// previewCmd loads the selected file's diff, if it isn't showing already.
func (c *commitPopup) previewCmd(g files.Git) tea.Cmd {
	e, ok := c.selected()
	if !ok {
		c.preview, c.previewRel = nil, ""
		return nil
	}
	if e.rel == c.previewRel && e.staged == c.previewStaged && c.preview != nil {
		return nil
	}
	return func() tea.Msg {
		var out string
		if e.staged {
			out, _ = g.DiffStaged(e.rel)
		} else {
			out, _ = g.DiffUnstaged(e.rel, e.code == "?")
		}
		return commitPreviewMsg{e.rel, e.staged, out}
	}
}

func (c *commitPopup) setPreview(msg commitPreviewMsg) {
	e, ok := c.selected()
	if !ok || e.rel != msg.rel || e.staged != msg.staged {
		return
	}
	c.previewRel, c.previewStaged, c.previewOff = msg.rel, msg.staged, 0
	c.preview = []string{}
	d := files.ParseDiff(msg.out)
	for _, h := range d.Hunks {
		c.preview = append(c.preview, h.Header)
		c.preview = append(c.preview, h.Lines...)
	}
	if len(d.Hunks) == 0 && msg.out != "" {
		c.preview = append(c.preview, "binary file")
	}
}

func (c *commitPopup) wheel(d int) {
	c.previewOff = clamp(c.previewOff+3*d, 0, len(c.preview)-c.previewH)
}

// subject is the message's first line.
func (c *commitPopup) subject() string {
	s, _, _ := strings.Cut(strings.TrimLeft(c.input.Value(), "\n"), "\n")
	return s
}

func (c *commitPopup) toggleAmend() {
	c.amend = !c.amend
	val := strings.TrimSpace(c.input.Value())
	if c.amend {
		if val == "" || val == strings.TrimSpace(c.draft) {
			c.draft = c.input.Value()
			c.input.SetValue(c.lastMsg)
		}
		return
	}
	if val == strings.TrimSpace(c.lastMsg) {
		c.input.SetValue(c.draft)
	}
}

// ---- model side ----

func (m *Model) openCommit() tea.Cmd {
	if !m.git.OK {
		return m.setFlash("not a git repository")
	}
	c := m.commit
	m.mode = modeCommit
	c.err, c.busy, c.focusList = "", false, false
	c.preview, c.previewRel = nil, ""
	c.status = m.status
	return tea.Batch(c.input.Focus(), c.reload(m.git))
}

func (m *Model) keyCommit(msg tea.KeyPressMsg) tea.Cmd {
	c := m.commit
	k := msg.String()
	if c.busy {
		return nil
	}
	switch k {
	case "esc":
		c.input.Blur()
		m.mode = modeNormal // the message stays as a draft
		return nil
	case "tab", "shift+tab":
		c.focusList = !c.focusList
		if c.focusList {
			c.input.Blur()
			return m.commitPreview()
		}
		return c.input.Focus()
	case "ctrl+a":
		c.toggleAmend()
		return nil
	case "enter":
		return m.submitCommit()
	case "ctrl+d", "pgdown":
		c.previewOff = clamp(c.previewOff+max(1, c.previewH/2), 0, len(c.preview)-c.previewH)
		return nil
	case "ctrl+u", "pgup":
		c.previewOff = clamp(c.previewOff-max(1, c.previewH/2), 0, len(c.preview)-c.previewH)
		return nil
	}
	if c.focusList {
		switch k {
		case "j", "down", "ctrl+n":
			c.move(1)
			return m.commitPreview()
		case "k", "up", "ctrl+p":
			c.move(-1)
			return m.commitPreview()
		case "space", " ":
			e, ok := c.selected()
			if !ok {
				return nil
			}
			g, rel := m.git, e.rel
			if e.staged {
				return m.gitOpCmd("unstage", rel, nil, func() error { return g.UnstageFile(rel) })
			}
			return m.gitOpCmd("stage", rel, nil, func() error { return g.StageFile(rel) })
		}
		return nil
	}
	switch k {
	case "shift+enter", "ctrl+j", "alt+enter":
		c.input.InsertString("\n")
		return nil
	case "ctrl+n":
		c.move(1)
		return m.commitPreview()
	case "ctrl+p":
		c.move(-1)
		return m.commitPreview()
	}
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return cmd
}

func (m *Model) commitPreview() tea.Cmd {
	return m.commit.previewCmd(m.git)
}

func (m *Model) submitCommit() tea.Cmd {
	c := m.commit
	text := strings.TrimSpace(c.input.Value())
	switch {
	case text == "":
		c.err = "write a commit message first"
		return nil
	case !c.loaded:
		return nil
	case !c.amend && len(c.status.Files) == 0:
		c.err = "nothing to commit"
		return nil
	}
	c.err, c.busy = "", true
	g, amend, all := m.git, c.amend, c.nothingStaged() && !c.amend
	return func() tea.Msg {
		if all {
			if err := g.StageAll(); err != nil {
				return commitDoneMsg{err: err}
			}
		}
		hash, err := g.Commit(text+"\n", amend)
		return commitDoneMsg{hash: hash, amend: amend, ahead: g.Status().Ahead, err: err}
	}
}

func (m *Model) handleCommitDone(msg commitDoneMsg) tea.Cmd {
	c := m.commit
	c.busy = false
	if msg.err != nil {
		c.err = msg.err.Error()
		return m.handleGitOp(gitOpMsg{op: "commit"}) // a failed commit-all may have staged files
	}
	c.input.SetValue("")
	c.draft, c.amend = "", false
	c.input.Blur()
	m.mode = modeNormal
	verb := "committed"
	if msg.amend {
		verb = "amended"
	}
	flash := verb + " " + msg.hash
	if msg.ahead > 0 {
		flash += fmt.Sprintf(" · ↑%d unpushed · P to push", msg.ahead)
	}
	return tea.Batch(m.handleGitOp(gitOpMsg{op: "commit"}), m.setFlash(flash))
}

// ---- view ----

func (c *commitPopup) view(w, h int, st files.Status) string {
	c.input.SetWidth(w)
	lines := make([]string, 0, h)

	// title
	title := sAccent.Bold(true).Render("Commit") + sDim.Render(" to ") + sBold.Render(st.Branch)
	var right string
	switch {
	case c.amend:
		title = sAccent.Bold(true).Render("Amend ") + sBold.Render(c.lastHash) + sDim.Render(" on "+st.Branch)
	case !c.loaded:
		right = sDim.Render("loading…")
	case len(c.status.Files) == 0:
		right = sDim.Render("nothing to commit")
	case c.nothingStaged():
		right = lipgloss.NewStyle().Foreground(colYellow).Render(fmt.Sprintf("nothing staged: enter commits all %d files", len(c.status.Files)))
	default:
		right = sDim.Render(fmt.Sprintf("%d staged", len(c.status.Staged)))
	}
	if st.Ahead > 0 {
		right += sDim.Render("  ↑" + itoa(st.Ahead) + " unpushed")
	}
	lines = append(lines, spread(title, right, w))

	// message
	lines = append(lines, strings.Split(c.input.View(), "\n")...)
	n := utf8.RuneCountInString(c.subject())
	cnt := sDim
	switch {
	case n > 72:
		cnt = sDel
	case n > 50:
		cnt = lipgloss.NewStyle().Foreground(colYellow)
	}
	hint := "shift+enter new line"
	if c.focusList {
		hint = "tab back to message"
	}
	lines = append(lines, spread(sFaint.Render(hint), cnt.Render(fmt.Sprintf("subject %d/50", n)), w))
	lines = append(lines, sFaint.Render(strings.Repeat("─", w)))

	// files | preview
	footer := 2
	if c.err != "" {
		footer += min(4, len(strings.Split(c.err, "\n")))
	}
	bodyH := max(3, h-len(lines)-footer)
	c.previewH = bodyH - 1
	lw := clamp(w*2/5, 24, 56)
	pw := w - lw - 3
	left := c.listRows(lw, bodyH)
	rightRows := c.previewRows(pw, bodyH)
	for i := range bodyH {
		lines = append(lines, fit(left[i], lw)+sFaint.Render(" │ ")+fit(rightRows[i], pw))
	}

	lines = append(lines, sFaint.Render(strings.Repeat("─", w)))
	if c.err != "" {
		for i, l := range strings.Split(c.err, "\n") {
			if i >= 4 {
				break
			}
			lines = append(lines, sDel.Render(l))
		}
	}
	keys := "enter commit · tab files · ctrl+a amend · esc close (keeps draft)"
	if c.focusList {
		keys = "space stage/unstage · j/k select · ctrl+d/u scroll diff · enter commit · esc"
	}
	if c.busy {
		keys = "committing…"
	}
	lines = append(lines, sDim.Render(keys))
	return box(lines, w)
}

func (c *commitPopup) listRows(w, h int) []string {
	rows := make([]string, 0, len(c.entries))
	selRow := 0
	all := c.nothingStaged()
	for i, e := range c.entries {
		if e.header {
			rows = append(rows, sDim.Bold(true).Render(e.title))
			continue
		}
		sel := i == c.sel
		if sel {
			selRow = len(rows)
		}
		prefix := "  "
		if sel && c.focusList {
			prefix = sAccent.Render("› ")
		} else if sel {
			prefix = sDim.Render("› ")
		}
		check := "  "
		if e.staged || all {
			check = sAdd.Render("✓ ")
		}
		dir, name := path.Split(e.rel)
		label := name
		if sel {
			label = sBold.Render(name)
		} else if !e.staged && !all {
			label = sDim.Render(name)
		}
		if dir != "" {
			label += " " + sFaint.Render(strings.TrimSuffix(dir, "/"))
		}
		stat := gitStyle(e.code).Render(e.code)
		if e.add > 0 {
			stat += " " + sAdd.Render(fmt.Sprintf("+%d", e.add))
		}
		if e.del > 0 {
			stat += " " + sDel.Render(fmt.Sprintf("−%d", e.del))
		}
		sw := ansiWidth(stat)
		rows = append(rows, fit(prefix+check+label, w-sw-1)+" "+stat)
	}
	off := 0
	if selRow >= h {
		off = selRow - h + 1
	}
	out := make([]string, h)
	for i := range h {
		if off+i < len(rows) {
			out[i] = rows[off+i]
		}
	}
	return out
}

func (c *commitPopup) previewRows(w, h int) []string {
	out := make([]string, h)
	e, ok := c.selected()
	if !ok {
		return out
	}
	what := "staged"
	if !e.staged {
		what = "not staged"
		if c.nothingStaged() {
			what = "will be committed"
		}
	}
	out[0] = sBold.Render(e.rel) + sDim.Render("  "+what)
	if c.preview == nil {
		out[1] = sDim.Render("loading…")
		return out
	}
	for i := 1; i < h; i++ {
		j := c.previewOff + i - 1
		if j >= len(c.preview) {
			break
		}
		l := c.preview[j]
		body := l
		if l != "" && strings.IndexByte("+- \\", l[0]) >= 0 {
			body = l[:1] + files.ExpandTabs(l[1:])
		}
		switch {
		case strings.HasPrefix(l, "@@"):
			out[i] = sHunk.Render(body)
		case strings.HasPrefix(l, "+"):
			out[i] = sAdd.Render(body)
		case strings.HasPrefix(l, "-"):
			out[i] = sDel.Render(body)
		default:
			out[i] = sDim.Render(body)
		}
	}
	return out
}

// spread lays out left and right-aligned right across w cells.
func spread(left, right string, w int) string {
	rw := ansiWidth(right)
	return fit(left, max(0, w-rw-1)) + " " + right
}
