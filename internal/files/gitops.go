package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// gitRun runs git in root with stdin as its input. On failure the error
// carries git's own message (stderr), which is what users need to see.
func gitRun(ctx context.Context, root, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	// never wait for a password prompt: lens owns the terminal
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

func (g Git) run(stdin string, args ...string) (string, error) {
	if !g.OK {
		return "", errors.New("not a git repository")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return gitRun(ctx, g.Root, stdin, args...)
}

// hasHead reports whether the repo has a commit yet.
func (g Git) hasHead() bool {
	_, err := gitCmd(g.Root, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// DiffUnstaged is the diff of rel between the index and the working tree.
// Untracked files are diffed against /dev/null.
func (g Git) DiffUnstaged(rel string, untracked bool) (string, error) {
	if !g.OK {
		return "", errors.New("not a git repository")
	}
	if untracked {
		out, _ := gitCmd(g.Root, "diff", "--no-color", "--no-index", "--", "/dev/null", rel)
		return string(out), nil
	}
	out, err := gitCmd(g.Root, "diff", "--no-color", "--", rel)
	return string(out), err
}

// DiffStaged is the diff of rel between HEAD and the index.
func (g Git) DiffStaged(rel string) (string, error) {
	if !g.OK {
		return "", errors.New("not a git repository")
	}
	out, err := gitCmd(g.Root, "diff", "--no-color", "--cached", "--", rel)
	return string(out), err
}

// StageFile adds rel (also a deletion) to the index.
func (g Git) StageFile(rel string) error {
	_, err := g.run("", "add", "-A", "--", rel)
	return err
}

// UnstageFile takes rel's changes out of the index, keeping the working tree.
func (g Git) UnstageFile(rel string) error {
	if !g.hasHead() {
		_, err := g.run("", "rm", "--cached", "-q", "--", rel)
		return err
	}
	_, err := g.run("", "restore", "--staged", "--", rel)
	return err
}

// StageAll adds every change under root to the index.
func (g Git) StageAll() error {
	_, err := g.run("", "add", "-A", "--", ".")
	return err
}

// ApplyCached applies a patch to the index only (reverse: take it out).
func (g Git) ApplyCached(patch string, reverse bool) error {
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "-R")
	}
	_, err := g.run(patch, append(args, "-")...)
	return err
}

// Stat is a file's added and deleted line counts (-1 for binary files).
type Stat struct{ Add, Del int }

// NumStat returns line counts per root-relative path, for the staged
// changes (cached) or the unstaged ones.
func (g Git) NumStat(cached bool) map[string]Stat {
	args := []string{"-c", "core.quotePath=false", "diff", "--numstat", "--relative", "-z"}
	if cached {
		args = append(args, "--cached")
	}
	out, err := gitCmd(g.Root, args...)
	if err != nil {
		return map[string]Stat{}
	}
	return parseNumStat(string(out))
}

// parseNumStat reads `git diff --numstat -z`. Renames come as
// "add\tdel\t\x00old\x00new\x00".
func parseNumStat(out string) map[string]Stat {
	res := map[string]Stat{}
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		f := strings.SplitN(parts[i], "\t", 3)
		if len(f) < 3 {
			continue
		}
		st := Stat{-1, -1}
		if a, err := strconv.Atoi(f[0]); err == nil {
			st.Add = a
		}
		if d, err := strconv.Atoi(f[1]); err == nil {
			st.Del = d
		}
		p := f[2]
		if p == "" && i+2 < len(parts) { // rename: old and new path follow
			p = parts[i+2]
			i += 2
		}
		res[p] = st
	}
	return res
}

// CountLines counts the lines of an untracked file (-1 if binary or big).
func (g Git) CountLines(rel string) int {
	abs := filepath.Join(g.Root, filepath.FromSlash(rel))
	fi, err := os.Stat(abs)
	if err != nil || fi.Size() > 4<<20 {
		return -1
	}
	data, err := os.ReadFile(abs)
	if err != nil || IsBinary(data) {
		return -1
	}
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// Commit makes a new commit from the index with msg and returns its short
// hash. lens only ever adds commits: it never amends or rewrites history.
func (g Git) Commit(msg string) (string, error) {
	if _, err := g.run(msg, "commit", "-F", "-"); err != nil {
		return "", err
	}
	h, _ := g.run("", "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(h), nil
}

// LogLine is one line of `git log --graph`: graph art plus, on commit lines,
// the commit.
type LogLine struct {
	Graph    string
	Hash     string // "" for graph-only lines
	Refs     string // e.g. "HEAD -> main, origin/main"
	Subject  string
	Ago      string
	Unpushed bool
}

// Log returns up to n lines of the commit graph from HEAD, marking commits
// that are not pushed yet.
func (g Git) Log(n int, st Status) []LogLine {
	out, err := g.run("", "log", "--graph", "--no-color", "-n", strconv.Itoa(n),
		"--format=%x00%h%x00%D%x00%s%x00%cr", "HEAD")
	if err != nil {
		return nil
	}
	unpushed := map[string]bool{}
	var revs []string
	switch {
	case st.Upstream:
		revs = []string{"@{u}..HEAD"}
	case g.HasRemote:
		revs = []string{"HEAD", "--not", "--remotes"}
	default:
		revs = nil // no remote: nothing counts as unpushed
	}
	if revs != nil {
		if hs, err := g.run("", append([]string{"rev-list", "--abbrev-commit"}, revs...)...); err == nil {
			for _, h := range strings.Fields(hs) {
				unpushed[h] = true
			}
		}
	}
	return parseLog(out, unpushed)
}

func parseLog(out string, unpushed map[string]bool) []LogLine {
	var res []LogLine
	for l := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(l, "\x00")
		ll := LogLine{Graph: strings.TrimRight(f[0], " ")}
		if len(f) >= 5 {
			ll.Graph = f[0]
			ll.Hash, ll.Refs, ll.Subject, ll.Ago = f[1], f[2], f[3], f[4]
			ll.Unpushed = unpushed[ll.Hash]
		}
		res = append(res, ll)
	}
	return res
}

// ShowStat lists the files a commit changed, with line counts.
func (g Git) ShowStat(hash string) []string {
	out, err := g.run("", "-c", "core.quotePath=false", "show", "--numstat", "--format=", "--relative", hash)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

// PushTarget describes what Push will do, e.g. "origin/main" or
// "-u origin main" when the branch has no upstream yet.
func (g Git) PushTarget(st Status) (args []string, label string) {
	if !g.HasRemote {
		return nil, ""
	}
	if st.Upstream {
		up, _ := g.run("", "rev-parse", "--abbrev-ref", "@{u}")
		return []string{"push"}, strings.TrimSpace(up)
	}
	remote := "origin"
	if rems, err := g.run("", "remote"); err == nil {
		list := strings.Fields(rems)
		if len(list) > 0 && !slices.Contains(list, "origin") {
			remote = list[0]
		}
	}
	return []string{"push", "-u", remote, st.Branch}, "-u " + remote + " " + st.Branch
}

// Push runs git push with args from PushTarget.
func (g Git) Push(args []string) (string, error) {
	if !g.OK {
		return "", errors.New("not a git repository")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", g.Root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}
