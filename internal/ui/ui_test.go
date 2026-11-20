package ui

import (
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
