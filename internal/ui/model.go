// Package ui is the Bubble Tea front end of lens.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/williamknipe/lens/internal/debug"
	"github.com/williamknipe/lens/internal/files"
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
	}
	colouredMsg struct {
		rel    string
		mod    time.Time
		styled []string
	}
	diffMsg struct {
		rel   string
		out   string
		err   error
		first bool
	}
	pollMsg       struct{}
	pollResultMsg struct {
		status  files.Status
		rel     string
		mod     time.Time
		size    int64
		missing bool
		gitTook time.Duration
	}
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
	themeFixed    bool // LENS_THEME set: don't follow the terminal background
	pollEvery     time.Duration
}

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
		prompt:    p,
		showTree:  true,
		startFile: openFile,
		status:    files.Status{Files: map[string]string{}},
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
	cmds := []tea.Cmd{m.indexCmd(), m.pollCmd(), tea.RequestBackgroundColor}
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

// deferHighlightLines: opening a file longer than this shows it plain at
// once and colours it in the background. Reloads of the open file are
// coloured in one go so they don't flicker.
const deferHighlightLines = 1500

func (m *Model) loadCmd(rel string, target int) tea.Cmd {
	root := m.root
	deferAt := deferHighlightLines
	if rel == m.view.rel {
		deferAt = 0
	}
	return func() tea.Msg {
		start := time.Now()
		c := files.Load(root, rel, deferAt)
		debug.Since("open", start, 200*time.Millisecond, false, fmt.Sprintf("%s  %d lines  %d KB  %s %s",
			rel, len(c.Lines), c.Size/1024, highlightInfo(c), c.Note))
		return loadedMsg{rel, c, target}
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
		debug.Since("git diff", start, 300*time.Millisecond, false, rel)
		return diffMsg{rel, out, err, first}
	}
}

// pollCmd checks git status and the open file's mtime. The next poll is
// scheduled only after this one returns, so slow repos never pile up.
func (m *Model) pollCmd() tea.Cmd {
	g, root, rel := m.git, m.root, m.view.rel
	return func() tea.Msg {
		start := time.Now()
		r := pollResultMsg{status: g.Status(), rel: rel}
		r.gitTook = debug.Since("git status", start, 100*time.Millisecond, true, fmt.Sprintf("%d changed", len(r.status.Files)))
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
	if rel == m.view.rel && m.view.loaded {
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
			if m.view.rel != "" {
				return m, m.loadCmd(m.view.rel, 0)
			}
		}
		return m, nil

	case indexMsg:
		m.finder.setFiles(msg.files)
		return m, nil

	case loadedMsg:
		m.view.setFile(msg.rel, msg.content, msg.target)
		var cmds []tea.Cmd
		if msg.content.Pending {
			cmds = append(cmds, m.colourCmd(msg.rel, msg.content))
		}
		if m.view.diff {
			cmds = append(cmds, m.diffCmd(false))
		}
		return m, tea.Batch(cmds...)

	case colouredMsg:
		// apply only if the same version of the file is still showing
		if msg.rel == m.view.rel && msg.mod.Equal(m.view.mod) && len(msg.styled) == len(m.view.lines) {
			m.view.styled = msg.styled
		}
		return m, nil

	case diffMsg:
		if msg.rel == m.view.rel && m.view.diff {
			m.view.setDiff(msg.out, msg.err, msg.first)
		}
		return m, nil

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
	cmds := []tea.Cmd{tea.Tick(m.pollEvery, func(time.Time) tea.Msg { return pollMsg{} })}
	start := time.Now()
	defer func() { debug.Since("tree refresh", start, 50*time.Millisecond, true, "") }()
	m.status = r.status
	m.tree.setStatus(r.status.Files)
	m.tree.refresh()
	if r.rel != "" && r.rel == m.view.rel && m.view.loaded {
		switch {
		case r.missing && m.view.note == "":
			m.view.note = "File was deleted"
		case !r.missing && (!r.mod.Equal(m.view.mod) || r.size != m.view.size):
			cmds = append(cmds, m.loadCmd(r.rel, 0))
		case m.view.diff:
			// git state may have changed (e.g. a commit) without touching the file
			cmds = append(cmds, m.diffCmd(false))
		}
	}
	return tea.Batch(cmds...)
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
	case "tab", "shift+tab":
		if m.focus == focusTree {
			m.focus = focusViewer
		} else if m.showTree {
			m.focus = focusTree
		}
		return nil
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
	case "r":
		m.tree.refresh()
		cmds := []tea.Cmd{m.indexCmd()}
		if m.view.rel != "" {
			cmds = append(cmds, m.loadCmd(m.view.rel, 0))
		}
		return tea.Batch(append(cmds, m.setFlash("refreshed"))...)
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
	case "ctrl+d", "pgdown":
		t.move(max(1, t.height/2))
	case "ctrl+u", "pgup":
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
			} else {
				m.focus = focusViewer
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
	case "ctrl+d":
		v.scroll(max(1, v.height/2))
	case "ctrl+u":
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

func (m *Model) currentPath() string {
	if m.focus == focusTree {
		if n := m.tree.selected(); n != nil {
			return n.rel
		}
	}
	return m.view.rel
}

func (m *Model) toggleDiff() tea.Cmd {
	v := m.view
	if v.rel == "" || !v.loaded {
		return m.setFlash("open a file first")
	}
	if !m.git.OK {
		return m.setFlash("not a git repository")
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
	// refresh the index in the background so new files show up
	return tea.Batch(m.finder.input.Focus(), m.indexCmd())
}

func (m *Model) keyFinder(msg tea.KeyPressMsg) tea.Cmd {
	f := m.finder
	switch msg.String() {
	case "esc", "ctrl+p", "super+p":
		m.closeOverlay()
	case "up", "ctrl+k", "shift+tab":
		f.move(-1)
	case "down", "ctrl+j", "ctrl+n", "tab":
		f.move(1)
	case "enter":
		if rel, ok := f.picked(); ok {
			line := f.line
			m.closeOverlay()
			m.tree.reveal(rel)
			m.focus = focusViewer
			if m.view.rel != rel {
				m.view.setQuery("")
			}
			return m.openFile(rel, line)
		}
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		f.refilter()
		return cmd
	}
	return nil
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
		m.tree.cursor = i
		n := m.tree.rows[i]
		if n.isDir {
			m.tree.toggle()
			return nil
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
		title := " " + sBold.Render(m.name)
		if m.focus == focusTree {
			title = " " + sAccent.Bold(true).Render(m.name)
		}
		treeLines = append([]string{fit(title, tw)}, m.tree.render(tw, m.focus == focusTree && m.mode == modeNormal)...)
	}
	viewLines = append([]string{m.view.title(m.view.width, m.status.Files[m.view.rel])},
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
	rw := lipgloss.Width(r)
	return fit(left, m.width-rw) + r
}

func helpBox() string {
	keys := [][2]string{
		{"ctrl+p", "find file (add :42 for a line)"},
		{"ctrl+f", "search in all files (ripgrep)"},
		{"tab", "switch sidebar / viewer"},
		{"ctrl+b", "hide / show sidebar"},
		{"j k ↑ ↓", "scroll (sidebar: move)"},
		{"J K ⇧↑ ⇧↓", "move cursor only"},
		{"H M L", "cursor to top / middle / bottom"},
		{"{ }", "previous / next block"},
		{"enter", "open file / toggle folder"},
		{"l space", "preview file, stay in sidebar"},
		{"h", "collapse folder / go to parent"},
		{"g G", "top / bottom"},
		{"ctrl+d ctrl+u", "half page down / up"},
		{"space pgdn", "page down (pgup: up)"},
		{"/  n N", "search in file, next / prev"},
		{":", "go to line"},
		{"d", "toggle git diff (enter jumps to line)"},
		{"y  Y", "copy path / path:line"},
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
