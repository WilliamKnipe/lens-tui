package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// gitRepo makes a repo with the given committed files.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		gitIn(t, dir, args...)
	}
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Skipf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

// drive runs cmd and feeds the messages back until things settle.
func drive(m *Model, cmd tea.Cmd) {
	queue := runBatch(cmd)
	for i := 0; i < len(queue) && i < 200; i++ {
		_, c := m.Update(queue[i])
		queue = append(queue, runBatch(c)...)
	}
}

func press(m *Model, k string) {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		r := []rune(k)[0]
		msg = tea.KeyPressMsg{Code: r, Text: k}
	}
	drive(m, m.handleKey(msg))
}

func twoHunkFile() (orig, changed string) {
	var a []string
	for i := range 30 {
		a = append(a, "line "+itoa(i))
	}
	b := append([]string(nil), a...)
	b[2] = "TOP"
	b[26] = "BOTTOM"
	return strings.Join(a, "\n") + "\n", strings.Join(b, "\n") + "\n"
}

// TestStageHunkFlow: in the diff, s stages the hunk under the cursor only,
// and the cursor moves on to the hunk that is left.
func TestStageHunkFlow(t *testing.T) {
	orig, changed := twoHunkFile()
	dir := gitRepo(t, map[string]string{"f.txt": orig, "g.txt": "g\n"})
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte(changed), 0o644)
	os.WriteFile(filepath.Join(dir, "g.txt"), []byte("g2\n"), 0o644)

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())
	m.focus = focusTree
	press(m, "d") // review: changed files, first one's diff
	if m.view.rel != "f.txt" || !m.view.diff || len(m.view.hunks) != 2 {
		t.Fatalf("rel=%q diff=%v hunks=%d", m.view.rel, m.view.diff, len(m.view.hunks))
	}
	m.focus = focusViewer
	m.view.showHunk(0)
	press(m, "s")
	staged := gitIn(t, dir, "diff", "--cached")
	if !strings.Contains(staged, "+TOP") || strings.Contains(staged, "BOTTOM") {
		t.Fatalf("staged:\n%s", staged)
	}
	v := m.view
	if v.countHunks(false) != 1 || v.countHunks(true) != 1 {
		t.Fatalf("hunks: %d unstaged, %d staged", v.countHunks(false), v.countHunks(true))
	}
	if h := v.hunkAt(v.cur); h < 0 || v.hunks[h].staged || !strings.Contains(v.hunks[h].patch, "BOTTOM") {
		t.Fatalf("cursor not on the remaining hunk (row %d)", v.cur)
	}
	if out := ansi.Strip(strings.Join(v.render(true), "\n")); !strings.Contains(out, "── staged") {
		t.Errorf("no staged divider:\n%s", out)
	}

	// s on the last hunk: the file is done, so review moves to the next one
	press(m, "s")
	if m.view.rel != "g.txt" {
		t.Fatalf("after the last hunk: rel=%q", m.view.rel)
	}
	if done, total := reviewProgress(m.status); done != 1 || total != 2 {
		t.Errorf("progress %d/%d", done, total)
	}

	// u on a staged hunk takes it back out
	press(m, "S") // stage g.txt whole
	m.tree.reveal("f.txt")
	drive(m, m.openChanged("f.txt"))
	if !m.view.gotoHunk(true, 0) {
		t.Fatal("no staged hunk")
	}
	press(m, "u")
	if staged := gitIn(t, dir, "diff", "--cached", "--", "f.txt"); strings.Contains(staged, "TOP") {
		t.Fatalf("still staged after u:\n%s", staged)
	}
}

// TestCommitAllWhenNothingStaged: with nothing staged, enter in the commit
// popup commits every change.
func TestCommitAllWhenNothingStaged(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nb\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\n"), 0o644)
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())

	press(m, "c")
	if m.mode != modeCommit || !m.commit.loaded || !m.commit.nothingStaged() {
		t.Fatalf("mode=%v loaded=%v", m.mode, m.commit.loaded)
	}
	out := ansi.Strip(m.render())
	if !strings.Contains(out, "enter commits all 2 files") || !strings.Contains(out, "new.txt") {
		t.Fatalf("popup:\n%s", out)
	}
	press(m, "enter")
	if m.commit.err == "" {
		t.Fatal("empty message should be refused")
	}
	for _, r := range "Add b" {
		press(m, string(r))
	}
	press(m, "enter")
	if m.mode != modeNormal || !strings.HasPrefix(m.flash, "committed ") {
		t.Fatalf("mode=%v flash=%q err=%q", m.mode, m.flash, m.commit.err)
	}
	if st := gitIn(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("not clean:\n%s", st)
	}
	if log := gitIn(t, dir, "log", "-1", "--format=%s"); strings.TrimSpace(log) != "Add b" {
		t.Errorf("subject = %q", log)
	}
	if m.commit.input.Value() != "" {
		t.Error("message not cleared after commit")
	}
}

// TestCommitStagedOnly: with something staged, only that is committed, and
// esc keeps the message as a draft.
func TestCommitStagedOnly(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b2\n"), 0o644)
	gitIn(t, dir, "add", "a.txt")
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())

	press(m, "c")
	for _, r := range "Only a" {
		press(m, string(r))
	}
	press(m, "esc")
	press(m, "c")
	if m.commit.input.Value() != "Only a" {
		t.Fatalf("draft = %q", m.commit.input.Value())
	}
	press(m, "enter")
	if st := gitIn(t, dir, "status", "--porcelain"); strings.TrimSpace(st) != "M b.txt" {
		t.Fatalf("status:\n%s", st)
	}
}

// TestPushPopup: the log lists commits; with no remote, enter explains why
// it can't push.
func TestPushPopup(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())
	press(m, "P")
	if m.mode != modePush || len(m.push.lines) != 1 || m.push.stat == nil {
		t.Fatalf("mode=%v lines=%d stat=%v", m.mode, len(m.push.lines), m.push.stat)
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, "init") || !strings.Contains(out, "a.txt") {
		t.Fatalf("popup:\n%s", out)
	}
	press(m, "enter")
	if !strings.Contains(m.push.err, "no remote") {
		t.Fatalf("err = %q", m.push.err)
	}
	press(m, "esc")
	if m.mode != modeNormal {
		t.Fatal("esc didn't close")
	}
}

// TestPushToRemote pushes to a local bare repo.
func TestPushToRemote(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare")
	gitIn(t, dir, "remote", "add", "origin", bare)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644)
	gitIn(t, dir, "commit", "-qam", "second")

	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())
	if m.status.Ahead != 2 {
		t.Fatalf("ahead = %d", m.status.Ahead)
	}
	if !strings.Contains(ansi.Strip(m.statusBar()), "↑2 P push") {
		t.Errorf("status bar: %q", ansi.Strip(m.statusBar()))
	}
	press(m, "P")
	if !strings.HasPrefix(m.push.target, "-u origin ") {
		t.Fatalf("target = %q", m.push.target)
	}
	press(m, "enter")
	if m.push.err != "" || m.mode != modeNormal || !strings.Contains(m.flash, "pushed 2 commits") {
		t.Fatalf("err=%q mode=%v flash=%q", m.push.err, m.mode, m.flash)
	}
	if !m.status.Upstream || m.status.Ahead != 0 {
		t.Errorf("after push: upstream=%v ahead=%d", m.status.Upstream, m.status.Ahead)
	}
}

// TestPopupsFit: the commit and push popups stay inside small terminals.
func TestPopupsFit(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644)
	for _, size := range [][2]int{{60, 16}, {200, 60}} {
		m := New(dir, "")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		drive(m, m.pollCmd())
		for _, k := range []string{"c", "esc", "P"} {
			press(m, k)
			lines := strings.Split(m.render(), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v %s: %d lines", size, k, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Fatalf("%v %s: line %d width %d", size, k, i, w)
				}
			}
		}
	}
}

// TestNextFileSkipsStaged: after a file's last hunk is staged, review moves to
// a file that still has changes, even when a poll that started before the
// staging comes back late with the old status.
func TestNextFileSkipsStaged(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"})
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f+"2\n"), 0o644)
	}
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())
	m.focus = focusTree
	press(m, "d")
	m.focus = focusViewer
	stale := m.pollCmd()() // a poll in flight: a, b, c all unstaged

	press(m, "s")
	press(m, "s")
	if m.view.rel != "c.txt" {
		t.Fatalf("rel = %q, want c.txt", m.view.rel)
	}
	m.Update(stale) // arrives late
	if _, ok := m.status.Unstaged["a.txt"]; ok {
		t.Fatal("late poll brought back the old status")
	}
	press(m, "s") // c's last hunk: a and b are staged, so stay put
	if m.view.rel != "c.txt" || !strings.Contains(m.flash, "all changes staged") {
		t.Fatalf("rel=%q flash=%q", m.view.rel, m.flash)
	}
}

// TestCommitNeverAmends: ctrl+a is just "start of line" in the message, and
// a commit always adds a new commit on top of HEAD.
func TestCommitNeverAmends(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	before := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644)
	m := New(dir, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, m.pollCmd())

	press(m, "c")
	for _, r := range "bc" {
		press(m, string(r))
	}
	drive(m, m.handleKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}))
	press(m, "a")
	if v := m.commit.input.Value(); v != "abc" {
		t.Fatalf("message = %q, want ctrl+a to go to the line start", v)
	}
	press(m, "enter")
	if parent := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD~1")); parent != before {
		t.Fatalf("HEAD~1 = %s, want the old HEAD %s (history was rewritten)", parent, before)
	}
}
