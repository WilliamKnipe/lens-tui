package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
