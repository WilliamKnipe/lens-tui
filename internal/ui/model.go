// Package ui is the Bubble Tea front end of lens.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/williamknipe/lens-tui/internal/debug"
	"github.com/williamknipe/lens-tui/internal/files"
)

type focusArea int

const (
	focusTree focusArea = iota
	focusViewer
)

type mode int

const (
	modeNormal mode = iota
	modeFinder      // ctrl+p
	modeSearch      // ctrl+f
	modeFind        // "/" in-file search
	modeGoto        // ":" go to line
	modeHelp        // "?"
	modeCommit      // "c" commit popup
	modePush        // "P" unpushed commits / push popup
)

// Polling slows down when `git status` is slow, so huge repos don't keep
// git busy. See pollInterval.
const (
	pollFast = time.Second
	pollMid  = 2 * time.Second
	pollSlow = 5 * time.Second
)

// pollInterval picks the next poll delay from how long git status took.
func pollInterval(gitTook time.Duration) time.Duration {
	switch {
	case gitTook > 300*time.Millisecond:
		return pollSlow
	case gitTook > 100*time.Millisecond:
		return pollMid
	}
	return pollFast
}

// Messages produced by background commands.
type (
	indexMsg  struct{ files []string }
	loadedMsg struct {
		rel     string
		content files.Content
		target  int
		follow  bool // target is an edit: show it with context above
	}
	changesMsg struct {
		rel   string
		marks map[int]byte
	}
	colouredMsg struct {
		rel    string
		mod    time.Time
		styled []string
	}
	diffMsg struct {
		rel      string
		out      string // against HEAD, for the file view's change marks
		unstaged string
		staged   string
		err      error
		first    bool
	}
	pollMsg       struct{}
	pollResultMsg struct {
		status  files.Status
		mods    map[string]time.Time // mtime of each changed file (not deleted ones)
		rel     string
		mod     time.Time
		size    int64
		missing bool
		gitTook time.Duration
		texts   map[string][]string // new contents of changed files edited since their last snapshot
		once    bool                // a one-off refresh: don't schedule the next poll
		gen     int                 // statusGen when the poll started
	}
	committedMsg  struct{ files []files.Committed }
	flashClearMsg struct{ seq int }
	copiedMsg     struct {
		text string
		err  error
	}
)

// Model is the root Bubble Tea model.
type Model struct {
	root     string
	name     string
	git      files.Git
	tree     *tree
	view     *viewer
	finder   *finder
	search   *searcher
	prompt   textinput.Model
	focus    focusArea
	mode     mode
	showTree bool

	width, height int
	status        files.Status
	flash         string
	flashSeq      int
	findOrigin    int // cursor line when "/" was pressed, restored on esc
	startFile     string
	wantDiff      bool   // open the next loaded file in diff view
	wantRel       string // the file most recently asked for; older loads are dropped
	themeFixed    bool   // LENS_THEME set: don't follow the terminal background
	pollEvery     time.Duration

	changedMod map[string]time.Time // last poll's mtimes of changed files (nil before the first poll)
	viewed     []string             // recently opened files, newest first
	allFiles   []string             // the file index in sidebar order, for tab
	follow     bool                 // follow mode: open whatever file was just edited
	followJump bool                 // next reload of the open file: jump to the edited line
	followLine int                  // 1-based line to show once the diff view reloads

	// an edit seen while a popup was open, followed once it closes
	pendingFollow     string
	pendingFollowLine int

	// last known contents of changed files, so follow mode can find what an
	// edit touched rather than the first change against HEAD
	snaps   map[string][]string
	snapMod map[string]time.Time

	committed []files.Committed // files from the latest commits, newest first

	reviewNext *reviewNext // where to go once the diff reloads after staging
	statusGen  int         // bumped by every stage/commit/push; older polls are dropped
	commit     *commitPopup
	push       *pushPopup
}

const (
	maxSnapSize     = 512 << 10 // larger files are not snapshotted
	maxSnapsPerPoll = 200
)

const maxViewed = 20

// New creates the model for root. openFile (relative to root) is opened on
// start when not empty.
func New(root, openFile string) *Model {
	p := textinput.New()
	m := &Model{
		root:      root,
		name:      filepath.Base(root),
		git:       files.OpenGit(root),
		tree:      newTree(root),
		view:      &viewer{},
		finder:    newFinder(),
		search:    newSearcher(),
		commit:    newCommitPopup(),
		push:      &pushPopup{},
		prompt:    p,
		showTree:  true,
		startFile: openFile,
		status:    files.Status{Files: map[string]string{}, Staged: map[string]string{}, Unstaged: map[string]string{}},
	}
	m.pollEvery = pollFast
	debug.Logf("start root=%s git=%v term=%s", root, m.git.OK, os.Getenv("TERM_PROGRAM"))
	if t := os.Getenv("LENS_THEME"); t != "" {
		files.SetTheme(t)
		m.themeFixed = true
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.indexCmd(), m.pollCmd(), m.committedCmd(), tea.RequestBackgroundColor}
	if m.startFile != "" {
		m.tree.reveal(m.startFile)
		m.focus = focusViewer
		cmds = append(cmds, m.loadCmd(m.startFile, 0))
	}
	return tea.Batch(cmds...)
}

// ---- background commands ----

func (m *Model) indexCmd() tea.Cmd {
	root := m.root
	return func() tea.Msg {
		start := time.Now()
		list := files.ListFiles(root)
		debug.Since("index", start, 500*time.Millisecond, false, fmt.Sprintf("%d files", len(list)))
		return indexMsg{list}
	}
}

// committedCmd lists files from recent commits for the finder's
// "Recently changed" list.
func (m *Model) committedCmd() tea.Cmd {
	g := m.git
	return func() tea.Msg {
		start := time.Now()
		list := g.RecentCommitted(recentPerList * 2)
		debug.Since("git log", start, 200*time.Millisecond, true, fmt.Sprintf("%d files", len(list)))
		return committedMsg{list}
	}
}

// deferHighlightLines: opening a file longer than this shows it plain at
// once and colours it in the background. Reloads of the open file are
// coloured in one go so they don't flicker.
const deferHighlightLines = 1500

func (m *Model) loadCmd(rel string, target int) tea.Cmd {
	root := m.root
	m.wantRel = rel
	deferAt := deferHighlightLines
	if rel == m.view.rel {
		deferAt = 0
	}
	return func() tea.Msg {
		start := time.Now()
		c := files.Load(root, rel, deferAt)
		debug.Since("open", start, 200*time.Millisecond, false, fmt.Sprintf("%s  %d lines  %d KB  %s %s",
			rel, len(c.Lines), c.Size/1024, highlightInfo(c), c.Note))
		return loadedMsg{rel, c, target, false}
	}
}

// colourCmd highlights a file that was shown plain first.
func (m *Model) colourCmd(rel string, c files.Content) tea.Cmd {
	root := m.root
	return func() tea.Msg {
		styled := files.Colour(root, rel, &c)
		debug.Since("highlight", time.Now().Add(-c.HighlightTime), 200*time.Millisecond, false,
			fmt.Sprintf("%s  %d lines (background)", rel, len(c.Lines)))
		return colouredMsg{rel, c.ModTime, styled}
	}
}

func highlightInfo(c files.Content) string {
	switch {
	case c.Cached:
		return "colours cached"
	case c.Pending:
		return "colouring in background"
	}
	return fmt.Sprintf("highlight %.1fms", float64(c.HighlightTime.Microseconds())/1000)
}

func (m *Model) diffCmd(first bool) tea.Cmd {
	g, rel, untracked := m.git, m.view.rel, m.status.Files[m.view.rel] == "?"
	return func() tea.Msg {
		start := time.Now()
		out, err := g.Diff(rel, untracked)
		msg := diffMsg{rel: rel, out: out, err: err, first: first}
		if err == nil {
			msg.unstaged, msg.err = g.DiffUnstaged(rel, untracked)
		}
		if msg.err == nil && !untracked {
			msg.staged, msg.err = g.DiffStaged(rel)
		}
		// refreshes are logged only when slow; opening a diff always is
		debug.Since("git diff", start, 300*time.Millisecond, !first, rel)
		return msg
	}
}

// changesCmd fetches the change marks for the open file. New files get none:
// every line would be marked.
func (m *Model) changesCmd() tea.Cmd {
	g, rel, code := m.git, m.view.rel, m.status.Files[m.view.rel]
	if code == "" || code == "?" || code == "A" || code == "D" || !g.OK {
		return func() tea.Msg { return changesMsg{rel: rel} }
	}
	return func() tea.Msg {
		out, err := g.Diff(rel, false)
		if err != nil {
			return changesMsg{rel: rel}
		}
		return changesMsg{rel, changeMarks(out)}
	}
}

// pollCmd checks git status and the open file's mtime. The next poll is
// scheduled only after this one returns, so slow repos never pile up.
func (m *Model) pollCmd() tea.Cmd {
	g, root, rel, gen := m.git, m.root, m.view.rel, m.statusGen
	snapMod := make(map[string]time.Time, len(m.snapMod))
	for p, t := range m.snapMod {
		snapMod[p] = t
	}
	return func() tea.Msg {
		start := time.Now()
		r := pollResultMsg{status: g.Status(), rel: rel, gen: gen}
		r.gitTook = debug.Since("git status", start, 100*time.Millisecond, true, fmt.Sprintf("%d changed", len(r.status.Files)))
		r.mods = make(map[string]time.Time, len(r.status.Files))
		for p, c := range r.status.Files {
			if c == "D" {
				continue
			}
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && !fi.IsDir() {
				r.mods[p] = fi.ModTime()
			}
		}
		r.texts = readSnaps(root, r.mods, snapMod)
		if rel != "" {
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				r.mod, r.size = fi.ModTime(), fi.Size()
			} else {
				r.missing = true
			}
		}
		return r
	}
}

// readSnaps reads the changed files whose mtime differs from their last
// snapshot. Big and binary files are skipped.
func readSnaps(root string, mods, snapMod map[string]time.Time) map[string][]string {
	out := map[string][]string{}
	for p, t := range mods {
		if len(out) >= maxSnapsPerPoll {
			break
		}
		if old, ok := snapMod[p]; ok && old.Equal(t) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(p))
		if fi, err := os.Stat(abs); err != nil || fi.Size() > maxSnapSize {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil || files.IsBinary(data) {
			continue
		}
		out[p] = files.SplitLines(string(data))
	}
	return out
}

// updateSnaps stores the polled file contents and drops snapshots of files
// that are no longer changed. It returns the first line of newest that
// differs from its previous snapshot (0 if unknown).
func (m *Model) updateSnaps(r pollResultMsg, newest string) int {
	if m.snaps == nil {
		m.snaps, m.snapMod = map[string][]string{}, map[string]time.Time{}
	}
	line := 0
	if old, ok := m.snaps[newest]; ok {
		if cur, ok := r.texts[newest]; ok {
			line = firstDiffLine(old, cur)
		}
	}
	for p, lines := range r.texts {
		m.snaps[p], m.snapMod[p] = lines, r.mods[p]
	}
	for p := range m.snaps {
		if _, ok := r.mods[p]; !ok {
			delete(m.snaps, p)
			delete(m.snapMod, p)
		}
	}
	return line
}

func (m *Model) setFlash(s string) tea.Cmd {
	m.flash = s
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(2500*time.Millisecond, func(time.Time) tea.Msg { return flashClearMsg{seq} })
}

func copyCmd(s string) tea.Cmd {
	if _, err := exec.LookPath("pbcopy"); err == nil {
		return func() tea.Msg {
			c := exec.Command("pbcopy")
			c.Stdin = strings.NewReader(s)
			return copiedMsg{s, c.Run()}
		}
	}
	// OSC 52: the terminal sets the clipboard (works over ssh too)
	return tea.Batch(tea.SetClipboard(s), func() tea.Msg { return copiedMsg{text: s} })
}

// openFile shows rel in the viewer, jumping to line (1-based) if > 0.
func (m *Model) openFile(rel string, line int) tea.Cmd {
	m.wantDiff = false
	if rel == m.view.rel && m.view.loaded {
		m.wantRel = rel // drop any load still on its way
		if m.view.diff {
			m.view.leaveDiff(0)
		}
		if line > 0 {
			m.view.moveTo(line - 1)
			m.view.syncMatch()
		}
		return nil
	}
	return m.loadCmd(rel, line)
}

// openChanged opens a file from the changed-files list straight into its diff.
func (m *Model) openChanged(rel string) tea.Cmd {
	if rel == m.view.rel && m.view.loaded {
		m.wantRel, m.wantDiff = rel, false
		if !m.view.diff {
			m.view.enterDiff()
		}
		return m.diffCmd(true)
	}
	m.wantDiff = true
	return m.loadCmd(rel, 0)
}

// followOpenCmd loads rel for follow mode and puts the cursor on line (the
// edited line, 1-based), or on its first change against HEAD if line is 0.
func (m *Model) followOpenCmd(rel string, line int) tea.Cmd {
	g, root, untracked := m.git, m.root, m.status.Files[rel] == "?"
	m.wantRel, m.wantDiff = rel, false
	return func() tea.Msg {
		c := files.Load(root, rel, deferHighlightLines)
		if line == 0 {
			out, _ := g.Diff(rel, untracked)
			line = firstChangedLine(out)
		}
		return loadedMsg{rel, c, line, true}
	}
}

// firstChangedLine is the 1-based line in the new file of the first change
// in a unified diff (0 if none).
func firstChangedLine(diff string) int {
	n := 0
	inHunk := false
	for _, l := range files.SplitLines(diff) {
		if mm := hunkRe.FindStringSubmatch(l); mm != nil {
			n, _ = strconv.Atoi(mm[1])
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(l, "+"), strings.HasPrefix(l, "-"):
			return max(1, n)
		case strings.HasPrefix(l, `\`):
		default:
			n++
		}
	}
	return 0
}

// firstDiffLine is the 1-based first line where old and new differ (0 if
// they are the same).
func firstDiffLine(old, new []string) int {
	for i := range min(len(old), len(new)) {
		if old[i] != new[i] {
			return i + 1
		}
	}
	if len(old) != len(new) {
		return min(len(old), len(new)) + 1
	}
	return 0
}

// pushViewed records rel as the most recently viewed file.
func (m *Model) pushViewed(rel string) {
	out := []string{rel}
	for _, p := range m.viewed {
		if p != rel && len(out) < maxViewed {
			out = append(out, p)
		}
	}
	m.viewed = out
}

// recentLists builds the finder's "recently changed" and "recently viewed"
// lists. The open file is left out of the viewed list.
func (m *Model) recentLists() (changed, viewed []recentItem) {
	for p, t := range m.changedMod {
		changed = append(changed, recentItem{rel: p, changed: true, mark: m.status.Files[p], mod: t})
	}
	// files from recent commits, so the list isn't empty once work is committed
	for _, c := range m.committed {
		if _, ok := m.changedMod[c.Path]; !ok {
			changed = append(changed, recentItem{rel: c.Path, changed: true, mod: c.Time})
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		if !changed[i].mod.Equal(changed[j].mod) {
			return changed[i].mod.After(changed[j].mod)
		}
		return changed[i].rel < changed[j].rel
	})
	for _, p := range m.viewed {
		if p == m.view.rel {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(p))); err != nil {
			continue
		}
		viewed = append(viewed, recentItem{rel: p, mark: m.status.Files[p]})
	}
	return changed, viewed
}

// ---- update ----

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		debug.Logf("keyboard enhancements: %+v", msg)
		return m, nil

	case tea.BackgroundColorMsg:
		if !m.themeFixed && !msg.IsDark() {
			files.SetTheme("github")
			if m.wantRel != "" {
				return m, m.loadCmd(m.wantRel, 0)
			}
		}
		return m, nil

	case indexMsg:
		m.finder.setFiles(msg.files)
		m.allFiles = append([]string(nil), msg.files...)
		sort.Slice(m.allFiles, func(i, j int) bool { return treeLess(m.allFiles[i], m.allFiles[j]) })
		return m, nil

	case loadedMsg:
		// drop loads overtaken by a newer request, or an older read of the open file
		if msg.rel != m.wantRel ||
			msg.rel == m.view.rel && m.view.loaded && msg.content.ModTime.Before(m.view.mod) {
			return m, nil
		}
		if msg.rel != m.view.rel {
			m.pushViewed(msg.rel)
		}
		if m.followJump && msg.rel == m.view.rel && m.view.loaded {
			m.followJump = false
			if line := firstDiffLine(m.view.lines, msg.content.Lines); line > 0 {
				if m.view.diff {
					m.followLine = line // applied when the diff reloads
				} else {
					msg.target, msg.follow = line, true
				}
			}
		}
		target := msg.target
		if msg.follow {
			target = 0
		}
		m.view.setFile(msg.rel, msg.content, target)
		if msg.follow && msg.target > 0 {
			m.view.reveal(msg.target - 1)
			m.view.syncMatch()
		}
		var cmds []tea.Cmd
		diffing := false
		if m.wantDiff {
			m.wantDiff, diffing = false, true
			m.view.enterDiff()
			cmds = append(cmds, m.diffCmd(true))
		}
		if msg.content.Pending {
			cmds = append(cmds, m.colourCmd(msg.rel, msg.content))
		} else if m.view.diff && !diffing {
			cmds = append(cmds, m.diffCmd(false))
		}
		if !m.view.diff {
			cmds = append(cmds, m.changesCmd())
		}
		return m, tea.Batch(cmds...)

	case committedMsg:
		m.committed = msg.files
		if m.mode == modeFinder && m.finder.showRecent() {
			sel := m.finder.sel
			m.finder.setRecent(m.recentLists())
			m.finder.sel = clamp(sel, 0, len(m.finder.recent)-1)
		}
		return m, nil

	case changesMsg:
		if msg.rel == m.view.rel {
			m.view.marks = msg.marks
		}
		return m, nil

	case colouredMsg:
		// apply only if the same version of the file is still showing
		if msg.rel == m.view.rel && msg.mod.Equal(m.view.mod) && len(msg.styled) == len(m.view.lines) {
			m.view.styled = msg.styled
		}
		return m, nil

	case diffMsg:
		if msg.rel == m.view.rel && m.view.diff {
			m.view.setDiff(msg.unstaged, msg.staged, msg.err, msg.first)
			if msg.err == nil && m.status.Files[msg.rel] != "?" && m.status.Files[msg.rel] != "A" {
				m.view.marks = changeMarks(msg.out)
			} else {
				m.view.marks = nil
			}
			if m.followLine > 0 {
				m.view.showFileLine(m.followLine)
				m.followLine = 0
			}
			return m, m.afterReviewDiff()
		}
		return m, nil

	case gitOpMsg:
		return m, m.handleGitOp(msg)

	case commitDataMsg:
		m.commit.setData(msg)
		return m, m.commitPreview()

	case commitPreviewMsg:
		m.commit.setPreview(msg)
		return m, nil

	case commitDoneMsg:
		return m, m.handleCommitDone(msg)

	case pushLogMsg:
		m.push.setLog(msg)
		return m, m.pushStatCmd()

	case pushStatMsg:
		m.push.setStat(msg)
		return m, nil

	case pushDoneMsg:
		return m, m.handlePushDone(msg)

	case pollMsg:
		return m, m.pollCmd()

	case pollResultMsg:
		return m, m.handlePoll(msg)

	case searchTickMsg:
		if msg.seq == m.search.seq {
			return m, m.search.run(m.root)
		}
		return m, nil

	case searchResultMsg:
		m.search.setResults(msg)
		return m, nil

	case flashClearMsg:
		if msg.seq == m.flashSeq {
			m.flash = ""
		}
		return m, nil

	case copiedMsg:
		if msg.err != nil {
			return m, m.setFlash("copy failed: " + msg.err.Error())
		}
		return m, m.setFlash("copied " + msg.text)

	case tea.MouseWheelMsg:
		m.handleWheel(msg.Mouse())
		return m, nil

	case tea.MouseClickMsg:
		return m, m.handleClick(msg.Mouse())

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	// anything else (e.g. cursor blink) goes to the active text input
	return m, m.updateInput(msg)
}

func (m *Model) handlePoll(r pollResultMsg) tea.Cmd {
	if next := pollInterval(r.gitTook); next != m.pollEvery {
		debug.Logf("poll interval %s -> %s (git status took %s)", m.pollEvery, next, r.gitTook.Round(time.Millisecond))
		m.pollEvery = next
	}
	var cmds []tea.Cmd
	if !r.once {
		cmds = append(cmds, tea.Tick(m.pollEvery, func(time.Time) tea.Msg { return pollMsg{} }))
	}
	if r.gen != m.statusGen {
		// started before a stage/commit/push: its status is out of date
		return tea.Batch(cmds...)
	}
	start := time.Now()
	defer func() { debug.Since("tree refresh", start, 50*time.Millisecond, true, "") }()
	oldCode, oldBranch := gitState(m.status, m.view.rel), m.status.Branch
	m.status = r.status
	m.tree.setStatus(r.status)
	m.tree.refresh()

	// files edited since the last poll; the newest one is followed
	var newest string
	var newestMod time.Time
	if m.changedMod != nil {
		for p, t := range r.mods {
			if old, ok := m.changedMod[p]; (!ok || t.After(old)) && t.After(newestMod) {
				newest, newestMod = p, t
			}
		}
	}
	m.changedMod = r.mods
	edited := m.updateSnaps(r, newest)
	switch {
	case !m.follow:
		m.pendingFollow = ""
	case m.mode != modeNormal:
		// a popup is open: remember the edit and follow it once it closes
		if newest != "" {
			m.pendingFollow, m.pendingFollowLine = newest, edited
		}
		newest = ""
	case newest == "" && m.pendingFollow != "":
		if _, ok := r.mods[m.pendingFollow]; ok {
			newest, edited = m.pendingFollow, m.pendingFollowLine
		}
		m.pendingFollow = ""
	default:
		m.pendingFollow = ""
	}
	if m.follow && newest != "" && m.mode == modeNormal && newest != m.view.rel {
		debug.Logf("follow %s", newest)
		m.tree.reveal(newest)
		m.view.setQuery("")
		if m.view.diff || m.tree.changed {
			m.followLine = edited // 0: openChanged starts on the first change
			cmds = append(cmds, m.openChanged(newest))
		} else {
			cmds = append(cmds, m.followOpenCmd(newest, edited))
		}
		return tea.Batch(cmds...)
	}

	// (skipped while another file is loading, so it isn't overridden)
	if r.rel != "" && r.rel == m.view.rel && m.view.loaded && m.wantRel == m.view.rel {
		switch {
		case r.missing && m.view.note == "":
			m.view.note = "File was deleted"
		case !r.missing && (!r.mod.Equal(m.view.mod) || r.size != m.view.size):
			m.followJump = m.follow
			cmds = append(cmds, m.loadCmd(r.rel, 0))
		case oldCode == gitState(r.status, r.rel) && oldBranch == r.status.Branch:
			// git state unchanged: no need to diff again
		case m.view.diff:
			// e.g. committed, reverted or switched branch without touching the file
			cmds = append(cmds, m.diffCmd(false))
		default:
			cmds = append(cmds, m.changesCmd())
		}
	}
	return tea.Batch(cmds...)
}

// gitState sums up rel's status, staged and unstaged, so a change on either
// side (e.g. `git add` in a shell) reloads the diff.
func gitState(st files.Status, rel string) string {
	return st.Files[rel] + "/" + st.Staged[rel] + "/" + st.Unstaged[rel]
}

func (m *Model) updateInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.mode {
	case modeFinder:
		m.finder.input, cmd = m.finder.input.Update(msg)
	case modeSearch:
		m.search.input, cmd = m.search.input.Update(msg)
	case modeFind, modeGoto:
		m.prompt, cmd = m.prompt.Update(msg)
	case modeCommit:
		m.commit.input, cmd = m.commit.input.Update(msg)
	}
	return cmd
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	switch m.mode {
	case modeFinder:
		return m.keyFinder(msg)
	case modeSearch:
		return m.keySearch(msg)
	case modeFind, modeGoto:
		return m.keyPrompt(msg)
	case modeCommit:
		return m.keyCommit(msg)
	case modePush:
		return m.keyPush(msg)
	case modeHelp:
		m.mode = modeNormal
		return nil
	}

	// Log modifier combos (ctrl+p, super+p…) to check what the terminal
	// sends. Only key names outside text inputs, never typed queries.
	if strings.Contains(k, "+") {
		debug.Logf("key %q", k)
	}

	// global keys
	switch k {
	case "q":
		return tea.Quit
	case "ctrl+p", "super+p":
		return m.openFinder()
	case "ctrl+f", "super+shift+f":
		return m.openSearch()
	case "c":
		return m.openCommit()
	case "P", "super+d":
		return m.openPush()
	case "?":
		m.mode = modeHelp
		return nil
	case "ctrl+b":
		m.showTree = !m.showTree
		if !m.showTree {
			m.focus = focusViewer
		}
		m.layout()
		return nil
	case "super+left", "alt+left", "ctrl+a":
		if m.showTree {
			m.focus = focusTree
		}
		return nil
	case "super+right", "alt+right", "ctrl+e":
		m.focus = focusViewer
		return nil
	case "tab":
		return m.cycleFile(1)
	case "shift+tab":
		return m.cycleFile(-1)
	case "y":
		if p := m.currentPath(); p != "" {
			return copyCmd(p)
		}
		return nil
	case "Y":
		if p := m.currentPath(); p != "" {
			if l := m.view.fileLine(); l > 0 && p == m.view.rel {
				p += ":" + strconv.Itoa(l)
			}
			return copyCmd(p)
		}
		return nil
	case "d":
		if m.focus == focusTree {
			return m.toggleChangedView()
		}
		return m.toggleDiff()
	case "/":
		return m.openPrompt(modeFind, "/")
	case ":":
		return m.openPrompt(modeGoto, ":")
	case "n", "N":
		dir := 1
		if k == "N" {
			dir = -1
		}
		if !m.view.jumpMatch(dir) && m.view.query != "" {
			return m.setFlash("no matches for " + m.view.query)
		}
		return nil
	case "f":
		m.follow = !m.follow
		if m.follow {
			if !m.git.OK {
				m.follow = false
				return m.setFlash("follow needs a git repository")
			}
			return m.setFlash("follow on: opens files as they change")
		}
		return m.setFlash("follow off")
	case "r":
		m.tree.refresh()
		cmds := []tea.Cmd{m.indexCmd()}
		if m.wantRel != "" {
			cmds = append(cmds, m.loadCmd(m.wantRel, 0))
		}
		return tea.Batch(append(cmds, m.setFlash("refreshed"))...)
	}

	if cmd, ok := m.keyReview(k); ok {
		return cmd
	}
	if m.focus == focusTree {
		return m.keyTree(k)
	}
	return m.keyViewer(k)
}

func (m *Model) keyTree(k string) tea.Cmd {
	t := m.tree
	switch k {
	case "j", "down":
		t.move(1)
	case "k", "up":
		t.move(-1)
	case "ctrl+d", "super+down", "meta+down", "pgdown":
		t.move(max(1, t.height/2))
	case "ctrl+u", "super+up", "meta+up", "pgup":
		t.move(-max(1, t.height/2))
	case "g", "home":
		t.move(-len(t.rows))
	case "G", "end":
		t.move(len(t.rows))
	case "h", "left":
		t.collapse()
	case "l", "right", "space":
		// expand dirs, or preview the file while staying in the sidebar
		if n := t.selected(); n != nil {
			if n.isDir {
				if !n.open {
					t.toggle()
				} else if k != "space" {
					t.move(1)
				} else {
					t.toggle()
				}
			} else {
				return m.openFile(n.rel, 0)
			}
		}
	case "enter":
		if n := t.selected(); n != nil {
			if n.isDir {
				t.toggle()
			} else if t.changed {
				return m.openChanged(n.rel)
			} else {
				return m.openFile(n.rel, 0)
			}
		}
	case "esc":
		if m.view.loaded {
			m.focus = focusViewer
		}
	}
	return nil
}

func (m *Model) keyViewer(k string) tea.Cmd {
	v := m.view
	switch k {
	// plain keys scroll the page (the cursor keeps its place on screen)
	case "j", "down":
		v.scroll(1)
	case "k", "up":
		v.scroll(-1)
	case "ctrl+d", "super+down", "meta+down":
		v.scroll(max(1, v.height/2))
	case "ctrl+u", "super+up", "meta+up":
		v.scroll(-max(1, v.height/2))
	case "pgdown", "space":
		v.scroll(max(1, v.height-2))
	case "pgup", "shift+space":
		v.scroll(-max(1, v.height-2))
	// shifted keys move only the cursor
	case "J", "shift+down":
		v.move(1)
	case "K", "shift+up":
		v.move(-1)
	case "H":
		v.cursorToScreen(0)
	case "M":
		v.cursorToScreen(1)
	case "L":
		v.cursorToScreen(2)
	case "}":
		v.jumpBlock(1)
	case "{":
		v.jumpBlock(-1)
	case "g", "home":
		v.moveTo(0)
	case "G", "end":
		v.moveTo(v.count() - 1)
	case "h", "left":
		v.xoff = max(0, v.xoff-8)
	case "l", "right":
		v.xoff += 8
	case "0":
		v.xoff = 0
	case "enter":
		if v.diff {
			if n := v.fileLine(); n > 0 {
				v.leaveDiff(n)
			}
		}
	case "esc":
		switch {
		case v.query != "":
			v.setQuery("")
		case v.diff:
			v.leaveDiff(0)
		case m.showTree:
			m.focus = focusTree
		}
	}
	return nil
}

// cycleFile opens the next (d=1) or previous (d=-1) file in sidebar order,
// across all folders, wrapping around. In diff view or the changed-files
// sidebar it cycles through the changed files only. Focus is left alone.
func (m *Model) cycleFile(d int) tea.Cmd {
	t := m.tree
	cur := m.view.rel
	if n := t.selected(); cur == "" && n != nil && !n.isDir {
		cur = n.rel
	}
	list, sorted := m.allFiles, true
	switch {
	case m.view.diff && !t.changed:
		// diff view: only files with changes to show
		list = nil
		for p := range m.status.Files {
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool { return treeLess(list[i], list[j]) })
	case t.changed || len(list) == 0:
		// the sidebar's own order, which isn't treeLess order in the changed view
		sorted = false
		list = nil
		for _, r := range t.rows {
			if !r.isDir && !r.header && !slices.Contains(list, r.rel) {
				list = append(list, r.rel)
			}
		}
	}
	if len(list) == 0 {
		return nil
	}
	next := list[0]
	if i := slices.Index(list, cur); i >= 0 {
		next = list[(i+d+len(list))%len(list)]
	} else if cur != "" && sorted {
		// where cur would be (e.g. a file created after indexing)
		i := sort.Search(len(list), func(i int) bool { return !treeLess(list[i], cur) })
		if d > 0 {
			next = list[i%len(list)]
		} else {
			next = list[(i-1+len(list))%len(list)]
		}
	}
	if next == cur {
		return nil
	}
	t.reveal(next)
	if t.changed || m.view.diff {
		return m.openChanged(next)
	}
	return m.openFile(next, 0)
}

// treeLess orders paths like the sidebar: folder by folder, folders before
// files, names compared case-insensitively.
func treeLess(a, b string) bool {
	pa, pb := strings.Split(a, "/"), strings.Split(b, "/")
	for k := 0; k < len(pa) && k < len(pb); k++ {
		if pa[k] == pb[k] {
			continue
		}
		aDir, bDir := k < len(pa)-1, k < len(pb)-1
		if aDir != bDir {
			return aDir
		}
		la, lb := strings.ToLower(pa[k]), strings.ToLower(pb[k])
		if la != lb {
			return la < lb
		}
		return pa[k] < pb[k]
	}
	return len(pa) < len(pb)
}

func (m *Model) currentPath() string {
	if m.focus == focusTree {
		if n := m.tree.selected(); n != nil {
			return n.rel
		}
	}
	return m.view.rel
}

// toggleChangedView (sidebar `d`) flips the sidebar between all files and
// changed files, and puts the viewer in or out of diff view to match. Focus
// stays in the sidebar.
func (m *Model) toggleChangedView() tea.Cmd {
	if !m.git.OK {
		return m.setFlash("not a git repository")
	}
	t, v := m.tree, m.view
	t.toggleChanged()
	if t.changed {
		if len(t.rows) == 0 {
			return m.setFlash("no changed files")
		}
		if v.loaded && v.rel != "" && !v.diff {
			v.enterDiff()
			return m.diffCmd(true)
		}
		if !v.loaded || v.rel == "" {
			// nothing open yet: show the selected (or first) changed file
			if n := t.selected(); n != nil {
				return m.openChanged(n.rel)
			}
		}
		return nil
	}
	if v.diff {
		v.leaveDiff(0)
	}
	return nil
}

// hasChanges reports whether git status lists any changed file.
func (m *Model) hasChanges() bool {
	return len(m.status.Files) > 0
}

func (m *Model) toggleDiff() tea.Cmd {
	v := m.view
	if !m.git.OK {
		return m.setFlash("not a git repository")
	}
	if v.rel == "" || !v.loaded {
		// nothing open yet: list changed files and open the first one's diff
		if m.tree.changed {
			if n := m.tree.selected(); n != nil {
				return m.openChanged(n.rel)
			}
			return m.setFlash("no changed files")
		}
		if !m.hasChanges() {
			return m.setFlash("no changed files")
		}
		return m.toggleChangedView()
	}
	if v.diff {
		v.leaveDiff(0)
		return nil
	}
	m.focus = focusViewer
	v.enterDiff()
	return m.diffCmd(true)
}

// ---- overlays and prompts ----

func (m *Model) openFinder() tea.Cmd {
	m.mode = modeFinder
	m.finder.input.SetValue("")
	m.finder.refilter()
	m.finder.setRecent(m.recentLists())
	// refresh the index in the background so new files show up
	return tea.Batch(m.finder.input.Focus(), m.indexCmd(), m.committedCmd())
}

func (m *Model) keyFinder(msg tea.KeyPressMsg) tea.Cmd {
	f := m.finder
	k := msg.String()
	// with an empty query, digits open a recent file (otherwise they're typed)
	if i := digitIndex(k); i >= 0 && f.input.Value() == "" && i < len(f.recent) {
		f.sel = i
		return m.pickFinder()
	}
	switch k {
	case "esc", "ctrl+p", "super+p":
		m.closeOverlay()
	case "up", "ctrl+k", "shift+tab":
		f.move(-1)
	case "down", "ctrl+j", "ctrl+n", "tab":
		f.move(1)
	case "enter":
		return m.pickFinder()
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		f.refilter()
		return cmd
	}
	return nil
}

// pickFinder opens the finder's selected file.
func (m *Model) pickFinder() tea.Cmd {
	f := m.finder
	rel, ok := f.picked()
	if !ok {
		return nil
	}
	line := f.line
	m.closeOverlay()
	m.tree.reveal(rel)
	m.focus = focusViewer
	if m.view.rel != rel {
		m.view.setQuery("")
	}
	return m.openFile(rel, line)
}

func (m *Model) openSearch() tea.Cmd {
	m.mode = modeSearch
	s := m.search
	s.input.CursorEnd()
	return tea.Batch(s.input.Focus(), s.changed())
}

func (m *Model) keySearch(msg tea.KeyPressMsg) tea.Cmd {
	s := m.search
	switch msg.String() {
	case "esc", "ctrl+f":
		m.closeOverlay()
	case "up", "ctrl+k", "shift+tab":
		s.move(-1)
	case "down", "ctrl+j", "ctrl+n", "tab":
		s.move(1)
	case "pgdown":
		s.move(s.rows)
	case "pgup":
		s.move(-s.rows)
	case "enter":
		if r, ok := s.picked(); ok {
			m.closeOverlay()
			m.tree.reveal(r.Path)
			m.focus = focusViewer
			m.view.setQuery(s.input.Value())
			return m.openFile(r.Path, r.Line)
		}
	default:
		before := s.input.Value()
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		if s.input.Value() != before {
			return tea.Batch(cmd, s.changed())
		}
		return cmd
	}
	return nil
}

func (m *Model) closeOverlay() {
	m.finder.input.Blur()
	m.search.input.Blur()
	m.mode = modeNormal
}

func (m *Model) openPrompt(md mode, prefix string) tea.Cmd {
	if !m.view.loaded || m.view.count() == 0 {
		return m.setFlash("open a file first")
	}
	m.mode = md
	m.focus = focusViewer
	m.findOrigin = m.view.cur
	m.prompt.Prompt = prefix
	m.prompt.Placeholder = ""
	if md == modeFind {
		m.prompt.Placeholder = "search in this file"
	} else {
		m.prompt.Placeholder = "line number"
	}
	m.prompt.SetValue("")
	return m.prompt.Focus()
}

func (m *Model) keyPrompt(msg tea.KeyPressMsg) tea.Cmd {
	v := m.view
	switch msg.String() {
	case "esc":
		if m.mode == modeFind {
			v.setQuery("")
			v.moveTo(m.findOrigin)
		}
		m.prompt.Blur()
		m.mode = modeNormal
		return nil
	case "enter":
		val := strings.TrimSpace(m.prompt.Value())
		m.prompt.Blur()
		md := m.mode
		m.mode = modeNormal
		if md == modeGoto {
			if n, err := strconv.Atoi(val); err == nil {
				if v.diff {
					v.leaveDiff(n)
				} else {
					v.moveTo(n - 1)
				}
			}
			return nil
		}
		if val != "" && len(v.matches) == 0 {
			return m.setFlash("no matches for " + val)
		}
		return nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	if m.mode == modeFind {
		v.setQuery(m.prompt.Value()) // incremental search
		v.jumpFirstFrom(m.findOrigin)
	}
	return cmd
}

// ---- mouse ----

func (m *Model) treeWidth() int {
	if !m.showTree {
		return 0
	}
	if m.width < 60 {
		return m.width / 3
	}
	return clamp(m.width/4, 22, 40)
}

func (m *Model) handleWheel(ms tea.Mouse) {
	if m.mode != modeNormal {
		if ms.Button == tea.MouseWheelUp {
			m.wheelOverlay(-1)
		} else if ms.Button == tea.MouseWheelDown {
			m.wheelOverlay(1)
		}
		return
	}
	d := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		d = -3
	case tea.MouseWheelDown:
		d = 3
	case tea.MouseWheelLeft:
		m.view.xoff = max(0, m.view.xoff-4)
		return
	case tea.MouseWheelRight:
		m.view.xoff += 4
		return
	}
	if ms.X < m.treeWidth() {
		m.tree.scroll(d)
	} else {
		m.view.scroll(d)
	}
}

func (m *Model) wheelOverlay(d int) {
	switch m.mode {
	case modeFinder:
		m.finder.move(d)
	case modeSearch:
		m.search.move(d)
	case modeCommit:
		m.commit.wheel(d)
	case modePush:
		m.push.move(d)
	}
}

func (m *Model) handleClick(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft || m.mode != modeNormal {
		return nil
	}
	row := ms.Y - 1 // row 0 is the title line
	if row < 0 || ms.Y >= m.height-1 {
		return nil
	}
	if ms.X < m.treeWidth() {
		m.focus = focusTree
		i := m.tree.offset + row
		if i >= len(m.tree.rows) {
			return nil
		}
		n := m.tree.rows[i]
		if n.header {
			return nil
		}
		m.tree.cursor = i
		if n.isDir {
			m.tree.toggle()
			return nil
		}
		if m.tree.changed {
			return m.openChanged(n.rel)
		}
		return m.openFile(n.rel, 0)
	}
	if m.view.loaded {
		m.focus = focusViewer
		m.view.cur = clamp(m.view.off+row, 0, m.view.count()-1)
	}
	return nil
}

// ---- view ----

func (m *Model) layout() {
	contentH := max(1, m.height-2) // title row + status bar
	tw := m.treeWidth()
	m.tree.height = contentH
	m.tree.ensureVisible()
	m.view.height = contentH
	m.view.width = m.width - tw
	if tw > 0 {
		m.view.width-- // separator
	}
	m.view.clamp()
	m.prompt.SetWidth(max(10, m.width-30))
}

func (m *Model) View() tea.View {
	start := time.Now()
	v := tea.NewView(m.render())
	debug.Since("render", start, 16*time.Millisecond, true, "")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "lens · " + m.name
	return v
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	tw := m.treeWidth()
	sep := sFaint.Render("│")

	var treeLines, viewLines []string
	if tw > 0 {
		b := newBar(m.focus == focusTree)
		var right string
		if m.tree.changed {
			done, total := reviewProgress(m.status)
			right = b.chip.Render(fmt.Sprintf("REVIEW %d/%d", done, total))
		}
		title := b.fit(b.base.Render(" ")+b.bold.Render(m.name), right, tw)
		treeLines = append([]string{title}, m.tree.render(tw, m.focus == focusTree && m.mode == modeNormal)...)
	}
	viewLines = append([]string{m.view.title(m.view.width, m.status.Files[m.view.rel], m.focus == focusViewer, m.follow)},
		m.view.render(m.focus == focusViewer)...)

	var b strings.Builder
	for i := 0; i < m.height-1; i++ {
		if tw > 0 {
			b.WriteString(treeLines[i])
			b.WriteString(sep)
		}
		b.WriteString(viewLines[i])
		b.WriteByte('\n')
	}
	b.WriteString(m.statusBar())
	base := b.String()

	switch m.mode {
	case modeFinder:
		w := min(90, m.width-6)
		return overlay(base, m.finder.view(w), m.width, m.height, (m.width-w-4)/2, 2)
	case modeSearch:
		w := min(140, m.width-6)
		h := min(24, m.height-4)
		return overlay(base, m.search.view(w, h), m.width, m.height, (m.width-w-4)/2, 1)
	case modeCommit:
		w := min(140, m.width-6)
		h := min(30, m.height-4)
		return overlay(base, m.commit.view(w, h, m.status), m.width, m.height, (m.width-w-4)/2, 1)
	case modePush:
		w := min(110, m.width-6)
		h := min(28, m.height-4)
		return overlay(base, m.push.view(w, h, m.status), m.width, m.height, (m.width-w-4)/2, 1)
	case modeHelp:
		h := helpBox()
		return overlay(base, h, m.width, m.height, max(0, (m.width-lipgloss.Width(h))/2), max(0, (m.height-lipgloss.Height(h))/2))
	}
	return base
}

func overlay(base, top string, w, h, x, y int) string {
	c := lipgloss.NewCanvas(w, h)
	c.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(top).X(max(0, x)).Y(max(0, y)).Z(1),
	))
	return c.Render()
}

func (m *Model) statusBar() string {
	var left string
	switch m.mode {
	case modeFind, modeGoto:
		left = m.prompt.View()
	default:
		chip := "FILES"
		if m.focus == focusViewer {
			chip = "VIEW"
		}
		left = sChip.Render(chip) + " "
		switch {
		case m.flash != "":
			left += sAccent.Render(m.flash)
		case m.view.query != "" && len(m.view.matches) > 0:
			left += sDim.Render(fmt.Sprintf("/%s  %d/%d  n/N next/prev  esc clear", m.view.query, m.view.mi+1, len(m.view.matches)))
		case m.view.query != "":
			left += sDim.Render(fmt.Sprintf("/%s  no matches", m.view.query))
		case m.view.diff && m.focus == focusViewer:
			left += sDim.Render("s stage hunk · S file · u unstage · { } hunk · c commit · d back")
		case m.tree.changed && m.focus == focusTree:
			left += sDim.Render("s stage file · u unstage · enter diff · c commit · d all files")
		case m.view.diff:
			left += sDim.Render("enter jump to line · d back to file")
		default:
			left += sDim.Render("ctrl+p find · ctrl+f search · d diff · y copy path · ? help")
		}
	}

	var right []string
	if m.view.loaded && m.view.count() > 0 {
		right = append(right, fmt.Sprintf("L%d/%d", m.view.cur+1, m.view.count()))
	}
	if m.status.Branch != "" {
		right = append(right, "⎇ "+m.status.Branch)
	}
	r := sDim.Render(strings.Join(right, "  ")) + " "
	if a := m.status.Ahead; a > 0 {
		r = sAccent.Render(fmt.Sprintf("↑%d", a)) + sDim.Render(" P push  ") + r
	}
	rw := lipgloss.Width(r)
	return fit(left, m.width-rw) + r
}

func helpBox() string {
	keys := [][2]string{
		{"ctrl+p", "find file (add :42 for a line); 1-9 0 pick recent"},
		{"ctrl+f", "search in all files (ripgrep)"},
		{"tab / shift+tab", "next / previous file"},
		{"cmd+← / cmd+→", "focus sidebar / viewer"},
		{"ctrl+b", "hide / show sidebar"},
		{"j k ↑ ↓", "scroll (sidebar: move)"},
		{"J K ⇧↑ ⇧↓", "move cursor only"},
		{"H M L", "cursor to top / middle / bottom"},
		{"{ }", "previous / next block (in diff: hunk)"},
		{"enter", "open file / toggle folder"},
		{"l space", "preview file, stay in sidebar"},
		{"h", "collapse folder / go to parent"},
		{"g G", "top / bottom"},
		{"cmd+↓ cmd+↑", "half page down / up (also ctrl+d ctrl+u)"},
		{"space pgdn", "page down (pgup: up)"},
		{"/  n N", "search in file, next / prev"},
		{":", "go to line"},
		{"d", "toggle git diff (enter jumps to line); in sidebar: review changed files"},
		{"s  S", "stage hunk / file (in diff or review sidebar)"},
		{"u  U", "unstage hunk / file"},
		{"c", "commit: message + staged files"},
		{"P", "unpushed commits / push"},
		{"y  Y", "copy path / path:line"},
		{"f", "follow mode: open files as they change"},
		{"r", "refresh"},
		{"esc", "clear search · leave diff · back"},
		{"q", "quit"},
	}
	lines := []string{sAccent.Bold(true).Render("lens keys"), ""}
	for _, k := range keys {
		lines = append(lines, sAccent.Render(fmt.Sprintf("%-14s", k[0]))+k[1])
	}
	lines = append(lines, "")
	if debug.Enabled() {
		lines = append(lines, sDim.Render("timing log: lens --log"))
	}
	lines = append(lines, sDim.Render("press any key to close"))
	return box(lines, 52)
}
