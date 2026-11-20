package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	out := "## main...origin/main [ahead 1]\x00 M sub/a.go\x00?? sub/new.txt\x00R  sub/b.go\x00sub/old.go\x00A  other/x.go\x00D  sub/gone.go\x00"
	st := parseStatus([]byte(out), "sub/")
	if st.Branch != "main" {
		t.Errorf("branch = %q", st.Branch)
	}
	want := map[string]string{"a.go": "M", "new.txt": "?", "b.go": "R", "gone.go": "D"}
	if !reflect.DeepEqual(st.Files, want) {
		t.Errorf("files = %v, want %v", st.Files, want)
	}
}

func TestParseBranch(t *testing.T) {
	for in, want := range map[string]string{
		"main":                   "main",
		"feat/x...origin/feat/x": "feat/x",
		"No commits yet on main": "main",
		"HEAD (no branch)":       "detached",
	} {
		if got := parseBranch(in); got != want {
			t.Errorf("parseBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRgLine(t *testing.T) {
	line := `{"type":"match","data":{"path":{"text":"./ui/model.go"},"lines":{"text":"\tfunc New() {\n"},"line_number":42,"absolute_offset":0,"submatches":[{"match":{"text":"New"},"start":6,"end":9}]}}`
	m, ok := parseRgLine([]byte(line))
	if !ok {
		t.Fatal("not parsed")
	}
	if m.Path != "ui/model.go" || m.Line != 42 || m.Text != "\tfunc New() {" || m.Text[m.Start:m.End] != "New" {
		t.Errorf("got %+v", m)
	}
	if _, ok := parseRgLine([]byte(`{"type":"begin","data":{}}`)); ok {
		t.Error("begin event should be skipped")
	}
}

func TestSplitLinesAndTabs(t *testing.T) {
	got := SplitLines("a\tb\r\nab\tc\n")
	want := []string{"a   b", "ab  c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadAndHighlight(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc main() {}\n"
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0x7f, 'E', 0, 1}, 0o644)

	c := Load(dir, "main.go", 0)
	if c.Note != "" || len(c.Lines) != 3 || len(c.Styled) != 3 {
		t.Fatalf("unexpected content: %+v", c)
	}
	if !strings.Contains(c.Styled[0], "\x1b[") {
		t.Error("expected ANSI colours in highlighted Go")
	}

	// over the defer limit: plain first, then Colour fills in (and caches)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte(src), 0o644)
	d := Load(dir, "other.go", 1)
	if !d.Pending || d.Styled[0] != d.Lines[0] {
		t.Fatalf("expected deferred highlight: %+v", d)
	}
	if styled := Colour(dir, "other.go", &d); d.Cached || styled[0] != c.Styled[0] {
		t.Error("expected a fresh highlight")
	}
	if again := Load(dir, "other.go", 1); again.Pending || !again.Cached {
		t.Error("expected reopen to use the cache")
	}
	if b := Load(dir, "bin", 0); !strings.HasPrefix(b.Note, "Binary") {
		t.Errorf("binary note = %q", b.Note)
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.txt", "src/b.go", "node_modules/x/y.js", "ignored.log"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}

	// no git: walk, skipping node_modules
	got := ListFiles(dir)
	want := []string{"a.txt", "ignored.log", "src/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walk: got %v, want %v", got, want)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	exec.Command("git", "-C", dir, "init", "-q").Run()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\nnode_modules/\n"), 0o644)
	got = ListFiles(dir)
	want = []string{".gitignore", "a.txt", "src/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("git: got %v, want %v", got, want)
	}

	st := OpenGit(dir).Status()
	if st.Files["a.txt"] != "?" {
		t.Errorf("status = %v", st.Files)
	}
}

func TestBuiltinGrep(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("func Three() {}\nthree := 3\nnothing\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.bin"), []byte("Three\x00"), 0o644)

	res, err := grep(t.Context(), dir, "three", 10) // lower case: matches both
	if err != nil || len(res) != 2 {
		t.Fatalf("got %v, %v", res, err)
	}
	if res[0].Line != 1 || res[0].Text[res[0].Start:res[0].End] != "Three" {
		t.Errorf("first match %+v", res[0])
	}
	res, _ = grep(t.Context(), dir, "Three", 10) // upper case: exact only
	if len(res) != 1 || res[0].Path != "a.go" {
		t.Errorf("case-sensitive got %v", res)
	}
}

func TestParseCommitted(t *testing.T) {
	out := "\x001700000200\n\na.go\n1700000000\n\x001700000100\n\nb.go\na.go\ngone.go\n"
	got := parseCommitted(out, 10, func(p string) bool { return p != "gone.go" })
	if len(got) != 3 || got[0].Path != "a.go" || got[1].Path != "1700000000" || got[2].Path != "b.go" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Time.Unix() != 1700000200 || got[2].Time.Unix() != 1700000100 {
		t.Errorf("times %v %v", got[0].Time, got[2].Time)
	}
	if n := len(parseCommitted(out, 1, func(string) bool { return true })); n != 1 {
		t.Errorf("limit: got %d", n)
	}
}

func TestRecentCommitted(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	for _, p := range []string{"top.txt", "sub/a.txt", "sub/b.txt"} {
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	git("add", ".")
	git("commit", "-qm", "one")
	os.WriteFile(filepath.Join(dir, "sub/b.txt"), []byte("y"), 0o644)
	git("commit", "-qam", "two")

	got := OpenGit(dir).RecentCommitted(10)
	if len(got) != 3 || got[0].Path != "sub/b.txt" {
		t.Fatalf("top: %+v", got)
	}
	// opened on a subfolder: paths are relative to it, other folders left out
	got = OpenGit(filepath.Join(dir, "sub")).RecentCommitted(10)
	if len(got) != 2 || got[0].Path != "b.txt" || got[1].Path != "a.txt" {
		t.Fatalf("sub: %+v", got)
	}
}
