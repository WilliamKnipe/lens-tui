package ui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/williamknipe/lens-tui/internal/files"
)

// Review: the changed-files sidebar plus the diff view. Staging a hunk or a
// file marks it as reviewed; the git index is the only state.

// gitOpMsg reports a stage/unstage/commit/push that finished.
type gitOpMsg struct {
	op     string // "stage", "unstage", "commit", "push"
	rel    string // the file staged or unstaged ("" for all)
	err    error
	next   *reviewNext
	status *files.Status // read right after the op
}

// reviewNext says where the diff cursor goes once the file's diff reloads
// after staging: the same position among the unstaged hunks, or the next
// file when this one has none left.
type reviewNext struct {
	rel      string
	ordinal  int
	nextFile bool // go to the next file even if hunks are left (S)
}

// reviewing is true where the stage keys apply: the diff view or the
// changed-files sidebar.
func (m *Model) reviewing() bool {
	return m.git.OK && (m.view.diff || m.tree.changed)
}

// gitOpCmd runs fn in the background and reports it as a gitOpMsg.
func (m *Model) gitOpCmd(op, rel string, next *reviewNext, fn func() error) tea.Cmd {
	g := m.git
	return func() tea.Msg {
		msg := gitOpMsg{op: op, rel: rel, err: fn(), next: next}
		st := g.Status()
		msg.status = &st
		return msg
	}
}

// keyReview handles the stage keys. ok is false when k isn't one of them
// here, so it falls through to the usual keys.
func (m *Model) keyReview(k string) (tea.Cmd, bool) {
	switch k {
	case "s", "S", "u", "U":
	case "{", "}":
		if m.focus != focusViewer || !m.view.diff {
			return nil, false
		}
		dir := 1
		if k == "{" {
			dir = -1
		}
		m.view.jumpHunk(dir)
		return nil, true
	default:
		return nil, false
	}
	if !m.reviewing() {
		if !m.git.OK {
			return nil, false
		}
		return m.setFlash("stage from the diff view: press d"), true
	}
	g := m.git

	// sidebar: whole files
	if m.focus == focusTree {
		n := m.tree.selected()
		if n == nil {
			return nil, true
		}
		rel := n.rel
		if k == "s" || k == "S" {
			if _, ok := m.status.Unstaged[rel]; !ok {
				return m.setFlash("nothing to stage in " + rel), true
			}
			return m.gitOpCmd("stage", rel, nil, func() error { return g.StageFile(rel) }), true
		}
		if _, ok := m.status.Staged[rel]; !ok {
			return m.setFlash("nothing staged in " + rel), true
		}
		return m.gitOpCmd("unstage", rel, nil, func() error { return g.UnstageFile(rel) }), true
	}

	v := m.view
	rel := v.rel
	if !v.diff || rel == "" {
		return nil, true
	}
	switch k {
	case "S":
		if _, ok := m.status.Unstaged[rel]; !ok {
			return m.setFlash("nothing to stage in this file"), true
		}
		return m.gitOpCmd("stage", rel, &reviewNext{rel: rel, nextFile: true}, func() error { return g.StageFile(rel) }), true
	case "U":
		if _, ok := m.status.Staged[rel]; !ok {
			return m.setFlash("nothing staged in this file"), true
		}
		return m.gitOpCmd("unstage", rel, &reviewNext{rel: rel}, func() error { return g.UnstageFile(rel) }), true
	}

	hi := v.hunkAt(v.cur)
	if hi < 0 {
		return m.setFlash("move the cursor onto a hunk ({ } jump between them)"), true
	}
	h := v.hunks[hi]
	next := &reviewNext{rel: rel, ordinal: v.hunkOrdinal(hi)}
	if k == "s" {
		if h.staged {
			return m.setFlash("already staged · u unstages it"), true
		}
		if m.status.Unstaged[rel] == "?" {
			// an untracked file is one hunk: add the file
			return m.gitOpCmd("stage", rel, next, func() error { return g.StageFile(rel) }), true
		}
		return m.gitOpCmd("stage", rel, next, func() error { return g.ApplyCached(h.patch, false) }), true
	}
	if !h.staged {
		return m.setFlash("not staged · s stages it"), true
	}
	if m.status.Staged[rel] == "A" && v.countHunks(true) == 1 {
		return m.gitOpCmd("unstage", rel, next, func() error { return g.UnstageFile(rel) }), true
	}
	return m.gitOpCmd("unstage", rel, next, func() error { return g.ApplyCached(h.patch, true) }), true
}

// handleGitOp refreshes everything a stage, unstage or commit changed.
func (m *Model) handleGitOp(msg gitOpMsg) tea.Cmd {
	// polls started before this op would bring back the old status
	m.statusGen++
	if msg.status != nil {
		m.status = *msg.status
		m.tree.setStatus(m.status)
	}
	if msg.err != nil {
		return tea.Batch(m.refreshCmd(), m.setFlash(msg.op+" failed: "+firstLine(msg.err.Error())))
	}
	cmds := []tea.Cmd{m.refreshCmd()}
	m.reviewNext = msg.next
	if m.view.diff && m.view.rel != "" {
		cmds = append(cmds, m.diffCmd(false))
	}
	switch msg.op {
	case "commit", "push":
		cmds = append(cmds, m.committedCmd())
	}
	if m.mode == modeCommit {
		cmds = append(cmds, m.commit.reload(m.git))
	}
	return tea.Batch(cmds...)
}

// afterReviewDiff places the cursor after the diff reloaded following a
// stage: on the hunk now in the staged one's place, or on the next file.
func (m *Model) afterReviewDiff() tea.Cmd {
	rn := m.reviewNext
	m.reviewNext = nil
	if rn == nil || rn.rel != m.view.rel {
		return nil
	}
	v := m.view
	if !rn.nextFile && v.gotoHunk(false, rn.ordinal) {
		return nil
	}
	if next := m.nextUnstaged(rn.rel); next != "" {
		m.tree.reveal(next)
		return m.openChanged(next)
	}
	if v.countHunks(false) == 0 {
		v.gotoHunk(true, rn.ordinal)
	}
	return m.setFlash("all changes staged · c to commit")
}

// nextUnstaged is the next file after rel (in sidebar order, wrapping) that
// still has unstaged changes, "" if none.
func (m *Model) nextUnstaged(rel string) string {
	var list []string
	for p := range m.status.Unstaged {
		if p != rel {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		return ""
	}
	sort.Strings(list)
	for _, p := range list {
		if p > rel {
			return p
		}
	}
	return list[0]
}

// refreshCmd reads git status once, outside the regular poll.
func (m *Model) refreshCmd() tea.Cmd {
	poll := m.pollCmd()
	return func() tea.Msg {
		r := poll().(pollResultMsg)
		r.once = true
		return r
	}
}

// reviewProgress counts the changed files with nothing left to stage.
func reviewProgress(st files.Status) (done, total int) {
	for p := range st.Files {
		if _, ok := st.Unstaged[p]; !ok {
			done++
		}
	}
	return done, len(st.Files)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// git often puts the useful part on the last line ("error: …")
		lines := strings.Split(s, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "fatal:") {
				return l
			}
		}
		return s[:i]
	}
	return s
}
