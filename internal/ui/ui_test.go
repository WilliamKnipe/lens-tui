package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/williamknipe/lens/internal/files"
)

func TestSplitLine(t *testing.T) {
	cases := map[string]struct {
		q    string
		line int
	}{
		"model.go:42": {"model.go", 42},
		"model.go:":   {"model.go", 0},
		"model.go":    {"model.go", 0},
		"a:b":         {"a:b", 0},
	}
	for in, want := range cases {
		q, l := splitLine(in)
		if q != want.q || l != want.line {
			t.Errorf("splitLine(%q) = %q,%d", in, q, l)
		}
	}
}

func TestDiffLineNumbers(t *testing.T) {
	v := &viewer{height: 10, width: 80}
	out := "diff --git a/x b/x\nindex 1..2\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	v.diff = true
	v.setDiff(out, nil, true)
	wantNums := []int{0, 1, 0, 2, 3}
	if len(v.diffNums) != len(wantNums) {
		t.Fatalf("nums = %v", v.diffNums)
	}
	for i, n := range wantNums {
		if v.diffNums[i] != n {
			t.Fatalf("nums = %v, want %v", v.diffNums, wantNums)
		}
	}
	if v.cur != 2 { // first change
		t.Errorf("cur = %d, want 2", v.cur)
	}
}

func TestViewerSearch(t *testing.T) {
	v := &viewer{height: 5, width: 40, loaded: true}
	v.lines = []string{"Foo", "bar", "foo foo", "baz"}
	v.styled = v.lines
	v.setQuery("foo") // smart case: matches Foo too
	if len(v.matches) != 3 {
		t.Fatalf("matches = %v", v.matches)
	}
	v.setQuery("Foo")
	if len(v.matches) != 1 {
		t.Fatalf("case-sensitive matches = %v", v.matches)
	}
}

// TestModelRender drives the model like Bubble Tea would and checks every
// rendered frame has the terminal's exact size.
func TestModelRender(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644)

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	// overlays are composited on a canvas, which trims trailing spaces
	check := func(stage string) {
		t.Helper()
		overlay := m.mode != modeNormal
		lines := strings.Split(m.render(), "\n")
		if len(lines) != 20 {
			t.Fatalf("%s: %d lines", stage, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > 100 || (!overlay && w != 100) {
				t.Fatalf("%s: line %d width %d: %q", stage, i, w, ansi.Strip(l))
			}
		}
	}
	check("start")

	// open src/main.go through the finder
	m.Update(indexMsg{files.ListFiles(dir)})
	m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, r := range "mai" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	check("finder")
	if rel, ok := m.finder.picked(); !ok || rel != "src/main.go" {
		t.Fatalf("finder picked %q", rel)
	}
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if m.view.rel != "src/main.go" || m.view.count() != 5 {
		t.Fatalf("viewer: %q %d lines", m.view.rel, m.view.count())
	}
	if !strings.Contains(ansi.Strip(m.render()), "println") {
		t.Error("file content not rendered")
	}
	check("viewer")

	m.handleKey(tea.KeyPressMsg{Code: '?', Text: "?"})
	check("help")
}

func TestViewerScrollVsMove(t *testing.T) {
	v := &viewer{height: 5, width: 40, loaded: true}
	v.lines = []string{"a", "b", "", "c", "d", "", "", "e", "f", "g", "h", "i"}
	v.styled = v.lines

	v.scroll(1) // page moves at once, cursor keeps its screen row
	if v.off != 1 || v.cur != 1 {
		t.Fatalf("scroll: off=%d cur=%d", v.off, v.cur)
	}
	v.move(1) // cursor moves, page stays
	if v.off != 1 || v.cur != 2 {
		t.Fatalf("move: off=%d cur=%d", v.off, v.cur)
	}
	v.cursorToScreen(2)
	if v.cur != 5 {
		t.Fatalf("L: cur=%d", v.cur)
	}
	v.cur = 0
	v.jumpBlock(1)
	if v.cur != 2 {
		t.Fatalf("} from 0: cur=%d", v.cur)
	}
	v.jumpBlock(1)
	if v.cur != 5 {
		t.Fatalf("} again: cur=%d", v.cur)
	}
	v.jumpBlock(-1)
	if v.cur != 2 {
		t.Fatalf("{: cur=%d", v.cur)
	}
	for range 20 {
		v.scroll(1) // at the end, scrolling walks the cursor to the last line
	}
	if v.cur != len(v.lines)-1 {
		t.Fatalf("scroll to end: cur=%d", v.cur)
	}
}

func TestFirstChangedLine(t *testing.T) {
	diff := "@@ -10,7 +10,8 @@ func x\n a\n b\n c\n+new\n d\n"
	if n := firstChangedLine(diff); n != 13 {
		t.Errorf("firstChangedLine = %d, want 13", n)
	}
	if n := firstChangedLine(""); n != 0 {
		t.Errorf("empty diff = %d", n)
	}
	if n := firstDiffLine([]string{"a", "b", "c"}, []string{"a", "B", "c"}); n != 2 {
		t.Errorf("firstDiffLine changed = %d", n)
	}
	if n := firstDiffLine([]string{"a"}, []string{"a", "b"}); n != 2 {
		t.Errorf("firstDiffLine appended = %d", n)
	}
	if n := firstDiffLine([]string{"a"}, []string{"a"}); n != 0 {
		t.Errorf("firstDiffLine same = %d", n)
	}
}

// TestFollowAndRecent: follow opens a file edited since the last poll at
// its edited line, and the finder lists it as recently changed, picked with "1".
func TestFollowAndRecent(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644)
	}
	write("a.txt", "one\ntwo\nthree\n")
	write("b.txt", "x\ny\nz\n")

	m := New(dir, "")
	m.git.OK = true // follow needs git; the poll results below are faked
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.loadCmd("a.txt", 0)())

	t0 := time.Now().Add(-time.Minute)
	poll := func(mods map[string]time.Time) tea.Cmd {
		st := files.Status{Files: map[string]string{}}
		for p := range mods {
			st.Files[p] = "M"
		}
		return m.handlePoll(pollResultMsg{status: st, mods: mods})
	}
	poll(map[string]time.Time{"a.txt": t0}) // baseline
	m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if !m.follow {
		t.Fatal("follow not on")
	}

	write("b.txt", "x\ny\nZ\n")
	cmd := poll(map[string]time.Time{"a.txt": t0, "b.txt": time.Now()})
	// run the batch and feed back the loaded file
	for _, msg := range runBatch(cmd) {
		if lm, ok := msg.(loadedMsg); ok {
			lm.target = firstDiffLine([]string{"x", "y", "z"}, lm.content.Lines) // no git here
			m.Update(lm)
		}
	}
	if m.view.rel != "b.txt" || m.view.cur != 2 {
		t.Fatalf("follow: rel=%q cur=%d", m.view.rel, m.view.cur)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if len(m.finder.recent) != 3 || m.finder.recent[0].rel != "b.txt" || m.finder.recent[2].rel != "a.txt" {
		t.Fatalf("recent = %+v", m.finder.recent)
	}
	if !strings.Contains(ansi.Strip(m.render()), "Recently viewed") {
		t.Error("recent lists not rendered")
	}
	cmd = m.handleKey(tea.KeyPressMsg{Code: '3', Text: "3"})
	if m.mode != modeNormal || cmd == nil {
		t.Fatal("digit did not pick")
	}
	m.Update(cmd())
	if m.view.rel != "a.txt" {
		t.Fatalf("picked %q", m.view.rel)
	}
}

// runBatch runs a command and any batch it returns, collecting messages.
// Tick commands are skipped.
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return nil // a tick
	}
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runBatch(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestDiffWithNoFileOpen: "d" with nothing open lists changed files and
// opens the first one in diff view.
func TestDiffWithNoFileOpen(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("two\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("TWO\n"), 0o644)

	for _, focus := range []focusArea{focusTree, focusViewer} {
		m := New(dir, "")
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
		m.Update(m.pollCmd()())
		m.focus = focus
		for _, msg := range runBatch(m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})) {
			if _, ok := msg.(loadedMsg); ok {
				for _, dm := range runBatch(func() tea.Msg { _, c := m.Update(msg); return c() }) {
					m.Update(dm)
				}
			}
		}
		if !m.tree.changed || m.view.rel != "b.txt" || !m.view.diff || len(m.view.diffLines) == 0 {
			t.Fatalf("focus %d: changed=%v rel=%q diff=%v lines=%d", focus, m.tree.changed, m.view.rel, m.view.diff, len(m.view.diffLines))
		}
	}
}

// TestFollowJumpsToLatestEdit: a file that already had a change near the top
// is edited further down; follow mode lands on the new edit, not the first
// change against HEAD.
func TestFollowJumpsToLatestEdit(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	write := func(rel string, ls []string) {
		os.WriteFile(filepath.Join(dir, rel), []byte(strings.Join(ls, "\n")+"\n"), 0o644)
	}
	git("init", "-q")
	write("a.txt", lines)
	write("b.txt", []string{"b"})
	git("add", ".")
	git("commit", "-qm", "init")

	edited := append([]string(nil), lines...)
	edited[2] = "early edit"
	write("a.txt", edited)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(filepath.Join(dir, "a.txt"), old, old)

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.loadCmd("b.txt", 0)())
	m.Update(m.pollCmd()()) // baseline: snapshots a.txt with the early edit
	m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})

	edited[149] = "late edit"
	write("a.txt", edited)
	_, cmd := m.Update(m.pollCmd()())
	for _, msg := range runBatch(cmd) {
		if lm, ok := msg.(loadedMsg); ok {
			m.Update(lm)
		}
	}
	if m.view.rel != "a.txt" || m.view.fileLine() != 150 {
		t.Fatalf("follow: rel=%q line=%d, want a.txt:150", m.view.rel, m.view.fileLine())
	}
	if m.view.cur < m.view.off || m.view.cur >= m.view.off+m.view.height {
		t.Fatalf("edited line not on screen: cur=%d off=%d", m.view.cur, m.view.off)
	}
}

func TestChangeMarks(t *testing.T) {
	diff := "diff --git a/x b/x\n@@ -1,6 +1,6 @@\n a\n-b\n+B\n+new\n c\n-d\n e\n f\n"
	got := changeMarks(diff)
	want := map[int]byte{2: markMod, 3: markAdd, 5: markDel}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("marks = %v, want %v", got, want)
	}
}

func TestRevealLeavesContext(t *testing.T) {
	v := &viewer{lines: make([]string, 300), height: 40, loaded: true}
	v.reveal(150)
	if v.cur != 150 || v.off != 140 {
		t.Fatalf("cur=%d off=%d, want 150/140", v.cur, v.off)
	}
	v.reveal(160) // already comfortably on screen: no scroll
	if v.off != 140 {
		t.Fatalf("off=%d after in-view reveal", v.off)
	}
	v.reveal(3)
	if v.off != 0 {
		t.Fatalf("off=%d near top", v.off)
	}
}

func TestCycleFileAcrossFolders(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"b/x.go", "a/c/y.go", "a/z.go", "README.md", "main.go"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x\n"), 0o644)
	}
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.indexCmd()())
	want := []string{"a/c/y.go", "a/z.go", "b/x.go", "main.go", "README.md"}
	if fmt.Sprint(m.allFiles) != fmt.Sprint(want) {
		t.Fatalf("order = %v", m.allFiles)
	}
	m.Update(m.loadCmd("b/x.go", 0)())
	for _, step := range []struct {
		d    int
		want string
	}{{1, "main.go"}, {1, "README.md"}, {1, "a/c/y.go"}, {-1, "README.md"}} {
		m.Update(m.cycleFile(step.d)())
		if m.view.rel != step.want {
			t.Fatalf("tab %+d: got %q, want %q", step.d, m.view.rel, step.want)
		}
		if n := m.tree.selected(); n == nil || n.rel != step.want {
			t.Fatalf("sidebar not on %q", step.want)
		}
	}
}

func TestCycleFileInDiffView(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	for _, p := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		os.WriteFile(filepath.Join(dir, p), []byte("x\n"), 0o644)
	}
	git("add", ".")
	git("commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "d.txt"), []byte("y\n"), 0o644)

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.indexCmd()())
	m.Update(m.pollCmd()())
	m.Update(m.loadCmd("b.txt", 0)())
	m.view.enterDiff()
	for _, want := range []string{"d.txt", "b.txt"} {
		for _, msg := range runBatch(m.cycleFile(1)) {
			m.Update(msg)
		}
		if m.view.rel != want || !m.view.diff {
			t.Fatalf("tab: rel=%q diff=%v, want %q in diff", m.view.rel, m.view.diff, want)
		}
	}
}

// TestCycleFileChangedSidebar: in the changed-files sidebar (plain path
// order, README.md before internal/), tab steps through every changed file.
func TestCycleFileChangedSidebar(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	all := []string{"README.md", "internal/a.go", "internal/b.go", "internal/c.go", "z.go"}
	for _, p := range all {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x\n"), 0o644)
	}
	git("add", ".")
	git("commit", "-qm", "init")
	for _, p := range all[:4] {
		os.WriteFile(filepath.Join(dir, p), []byte("y\n"), 0o644)
	}

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.indexCmd()())
	m.Update(m.pollCmd()())
	m.focus = focusTree
	for _, msg := range runBatch(m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})) {
		m.Update(msg)
	}
	if !m.tree.changed || !m.view.diff {
		t.Fatalf("not in changed view: changed=%v diff=%v", m.tree.changed, m.view.diff)
	}
	seen := map[string]bool{m.view.rel: true}
	for range 4 {
		for _, msg := range runBatch(m.cycleFile(1)) {
			m.Update(msg)
		}
		if !m.view.diff {
			t.Fatalf("%s not in diff view", m.view.rel)
		}
		seen[m.view.rel] = true
	}
	if len(seen) != 4 || seen["z.go"] {
		t.Fatalf("visited %v, want the 4 changed files", seen)
	}
}

// TestDiffRefreshOnlyOnGitChange: in diff view, a poll with no git change
// doesn't rerun git diff; a commit does.
func TestDiffRefreshOnlyOnGitChange(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("y\n"), 0o644)

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.pollCmd()())
	for _, msg := range runBatch(m.openChanged("a.txt")) {
		for _, dm := range runBatch(func() tea.Msg { _, c := m.Update(msg); return c() }) {
			m.Update(dm)
		}
	}
	if !m.view.diff || len(m.view.diffLines) == 0 {
		t.Fatal("diff not open")
	}
	diffs := func() int {
		n := 0
		for _, msg := range runBatch(m.handlePoll(m.pollCmd()().(pollResultMsg))) {
			if _, ok := msg.(diffMsg); ok {
				n++
			}
			m.Update(msg)
		}
		return n
	}
	if n := diffs(); n != 0 {
		t.Fatalf("idle poll ran git diff %d times", n)
	}
	git("commit", "-qam", "edit")
	if n := diffs(); n != 1 {
		t.Fatalf("poll after commit ran git diff %d times, want 1", n)
	}
	if m.view.diffNote != "No changes against HEAD" {
		t.Fatalf("diff not refreshed: note=%q", m.view.diffNote)
	}
}

// TestCmdArrowsHalfPage: Ghostty sends Cmd+Down as meta+down (kitty protocol).
func TestCmdArrowsHalfPage(t *testing.T) {
	v := &viewer{lines: make([]string, 300), height: 40, loaded: true}
	m := New(t.TempDir(), "")
	m.view, m.focus = v, focusViewer
	for _, mod := range []tea.KeyMod{tea.ModMeta, tea.ModSuper} {
		v.cur, v.off = 0, 0
		k := tea.KeyPressMsg{Code: tea.KeyDown, Mod: mod}
		m.handleKey(k)
		if v.off != 20 {
			t.Fatalf("%s: off=%d, want 20", k.String(), v.off)
		}
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: mod})
		if v.off != 0 {
			t.Fatalf("%s up: off=%d", k.String(), v.off)
		}
	}
}

// TestStaleLoadDropped: a slow load that finishes after a newer request
// must not replace the file that was asked for last.
func TestStaleLoadDropped(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	slow := m.openFile("a.txt", 0)
	fast := m.openFile("b.txt", 0)
	m.Update(fast())
	m.Update(slow()) // arrives last
	if m.view.rel != "b.txt" {
		t.Fatalf("showing %q, want b.txt", m.view.rel)
	}
	// going back to the open file also cancels a load on its way
	slow = m.openFile("a.txt", 0)
	m.openFile("b.txt", 0)
	m.Update(slow())
	if m.view.rel != "b.txt" {
		t.Fatalf("showing %q after returning to b.txt", m.view.rel)
	}
}

// TestFollowAfterPopup: an edit made while ctrl+p is open is followed once
// the popup closes, rather than lost.
func TestFollowAfterPopup(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	m := New(dir, "")
	m.git.OK = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.loadCmd("a.txt", 0)())
	t0 := time.Now().Add(-time.Minute)
	poll := func(mods map[string]time.Time) tea.Cmd {
		st := files.Status{Files: map[string]string{}}
		for p := range mods {
			st.Files[p] = "M"
		}
		return m.handlePoll(pollResultMsg{status: st, mods: mods, rel: m.view.rel})
	}
	poll(map[string]time.Time{"a.txt": t0})
	m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})

	m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	edit := time.Now()
	poll(map[string]time.Time{"a.txt": t0, "b.txt": edit})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	for _, msg := range runBatch(poll(map[string]time.Time{"a.txt": t0, "b.txt": edit})) {
		if lm, ok := msg.(loadedMsg); ok {
			m.Update(lm)
		}
	}
	if m.view.rel != "b.txt" {
		t.Fatalf("follow after popup: showing %q, want b.txt", m.view.rel)
	}
}

// TestClickChangedOpensDiff: clicking a file in the changed-files sidebar
// opens its diff, like enter does.
func TestClickChangedOpensDiff(t *testing.T) {
	m := New(t.TempDir(), "")
	m.git.OK = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.tree.setStatus(map[string]string{"x.go": "M"})
	m.tree.toggleChanged()
	m.handleClick(tea.Mouse{X: 2, Y: 1, Button: tea.MouseLeft})
	if !m.wantDiff || m.wantRel != "x.go" {
		t.Fatalf("click: wantDiff=%v wantRel=%q", m.wantDiff, m.wantRel)
	}
}

// TestDiffTabsMatchFile: tabs in diff lines expand after the +/-/space
// marker, so indentation matches the file view.
func TestDiffTabsMatchFile(t *testing.T) {
	v := &viewer{height: 10, width: 80, diff: true}
	v.setDiff("@@ -1,2 +1,2 @@\n \tkeep\n-\told\n+\tnew\n", nil, false)
	want := files.SplitLines("\tx\n")[0][:4] // the file view's indent
	for _, l := range v.diffPlain[1:] {
		if l[1:5] != want || l[5] == ' ' {
			t.Fatalf("diff line %q: indent differs from the file view", l)
		}
	}
}

// TestDeletedFileInChangedList: deleted files are listed and their diff shows.
func TestDeletedFileInChangedList(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")
	os.Remove(filepath.Join(dir, "gone.txt"))

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.Update(m.pollCmd()())
	m.focus = focusTree
	for _, msg := range runBatch(m.handleKey(tea.KeyPressMsg{Code: 'd', Text: "d"})) {
		_, c := m.Update(msg)
		for _, dm := range runBatch(c) {
			m.Update(dm)
		}
	}
	if len(m.tree.rows) != 1 || m.view.rel != "gone.txt" || !m.view.diff {
		t.Fatalf("rows=%d rel=%q diff=%v", len(m.tree.rows), m.view.rel, m.view.diff)
	}
	out := ansi.Strip(strings.Join(m.view.render(true), "\n"))
	if !strings.Contains(out, "-bye") {
		t.Fatalf("deleted file's diff not shown:\n%s", out)
	}
}
