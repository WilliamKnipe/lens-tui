package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStatusSides(t *testing.T) {
	out := "## main...origin/main [ahead 3, behind 1]\x00MM both.go\x00A  added.go\x00 M work.go\x00?? new.txt\x00 D gone.go\x00UU conflict.go\x00"
	st := parseStatus([]byte(out), "")
	if !st.Upstream || st.Ahead != 3 || st.Behind != 1 {
		t.Errorf("tracking = %v %d %d", st.Upstream, st.Ahead, st.Behind)
	}
	wantStaged := map[string]string{"both.go": "M", "added.go": "A"}
	wantUnstaged := map[string]string{"both.go": "M", "work.go": "M", "new.txt": "?", "gone.go": "D", "conflict.go": "U"}
	if !reflect.DeepEqual(st.Staged, wantStaged) {
		t.Errorf("staged = %v", st.Staged)
	}
	if !reflect.DeepEqual(st.Unstaged, wantUnstaged) {
		t.Errorf("unstaged = %v", st.Unstaged)
	}
}

func TestParseTracking(t *testing.T) {
	for in, want := range map[string][3]int{
		"main":                                   {0, 0, 0},
		"main...origin/main":                     {1, 0, 0},
		"main...origin/main [ahead 2]":           {1, 2, 0},
		"main...origin/main [behind 4]":          {1, 0, 4},
		"main...origin/main [gone]":              {0, 0, 0},
		"No commits yet on main":                 {0, 0, 0},
		"feat...origin/feat [ahead 1, behind 2]": {1, 1, 2},
	} {
		up, a, b := parseTracking(in)
		got := [3]int{0, a, b}
		if up {
			got[0] = 1
		}
		if got != want {
			t.Errorf("parseTracking(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseNumStat(t *testing.T) {
	out := "3\t1\ta.go\x00-\t-\timg.png\x002\t0\t\x00old.go\x00new.go\x00"
	got := parseNumStat(out)
	want := map[string]Stat{"a.go": {3, 1}, "img.png": {-1, -1}, "new.go": {2, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseLog(t *testing.T) {
	out := "* \x00abc1234\x00HEAD -> main\x00second\x002 hours ago\n|\\\n* \x00def5678\x00origin/main\x00first\x003 days ago\n"
	got := parseLog(out, map[string]bool{"abc1234": true})
	if len(got) != 3 || got[0].Hash != "abc1234" || !got[0].Unpushed || got[1].Hash != "" || got[2].Unpushed {
		t.Errorf("got %+v", got)
	}
}

// newRepo makes a repo with one committed file and returns its Git.
func newRepo(t *testing.T, files map[string]string) Git {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return OpenGit(dir)
}

func TestStageOneHunk(t *testing.T) {
	var orig []string
	for i := range 30 {
		orig = append(orig, "line "+string(rune('a'+i%26)))
	}
	g := newRepo(t, map[string]string{"f.txt": strings.Join(orig, "\n") + "\n"})
	changed := append([]string(nil), orig...)
	changed[1] = "\tCHANGED top"
	changed[27] = "CHANGED bottom"
	os.WriteFile(filepath.Join(g.Root, "f.txt"), []byte(strings.Join(changed, "\n")+"\n"), 0o644)

	out, err := g.DiffUnstaged("f.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	d := ParseDiff(out)
	if len(d.Hunks) != 2 {
		t.Fatalf("hunks = %d\n%s", len(d.Hunks), out)
	}
	// stage only the second hunk
	if err := g.ApplyCached(d.Patch(1), false); err != nil {
		t.Fatal(err)
	}
	staged, _ := g.DiffStaged("f.txt")
	if !strings.Contains(staged, "+CHANGED bottom") || strings.Contains(staged, "CHANGED top") {
		t.Fatalf("staged diff:\n%s", staged)
	}
	st := g.Status()
	if st.Staged["f.txt"] != "M" || st.Unstaged["f.txt"] != "M" {
		t.Errorf("status = %+v", st)
	}
	// and take it out again
	sd := ParseDiff(staged)
	if err := g.ApplyCached(sd.Patch(0), true); err != nil {
		t.Fatal(err)
	}
	if staged, _ := g.DiffStaged("f.txt"); staged != "" {
		t.Errorf("still staged:\n%s", staged)
	}
}

func TestStageCommitFlow(t *testing.T) {
	g := newRepo(t, map[string]string{"a.txt": "a\n"})
	os.WriteFile(filepath.Join(g.Root, "a.txt"), []byte("a\nb\n"), 0o644)
	os.WriteFile(filepath.Join(g.Root, "new.txt"), []byte("1\n2\n3"), 0o644)
	if n := g.CountLines("new.txt"); n != 3 {
		t.Errorf("CountLines = %d", n)
	}
	if err := g.StageFile("new.txt"); err != nil {
		t.Fatal(err)
	}
	if st := g.NumStat(true); st["new.txt"] != (Stat{3, 0}) {
		t.Errorf("numstat cached = %v", st)
	}
	if err := g.UnstageFile("new.txt"); err != nil {
		t.Fatal(err)
	}
	if err := g.StageAll(); err != nil {
		t.Fatal(err)
	}
	hash, err := g.Commit("second\n\nbody", false)
	if err != nil || hash == "" {
		t.Fatalf("commit: %q %v", hash, err)
	}
	if h, msg := g.LastCommit(); h != hash || msg != "second\n\nbody" {
		t.Errorf("last commit = %q %q", h, msg)
	}
	if st := g.Status(); len(st.Files) != 0 {
		t.Errorf("still changed: %v", st.Files)
	}
	if _, err := g.Commit("empty", false); err == nil {
		t.Error("empty commit should fail")
	}
	log := g.Log(10, g.Status())
	if len(log) != 2 || log[0].Subject != "second" || log[0].Unpushed {
		t.Errorf("log = %+v", log)
	}
}

func TestUnstageWithoutHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q").Run()
	os.WriteFile(filepath.Join(dir, "x"), []byte("x\n"), 0o644)
	g := OpenGit(dir)
	if err := g.StageFile("x"); err != nil {
		t.Fatal(err)
	}
	if err := g.UnstageFile("x"); err != nil {
		t.Fatal(err)
	}
	if st := g.Status(); st.Unstaged["x"] != "?" {
		t.Errorf("status = %+v", st)
	}
}
