package files

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Git wraps git commands for one root directory. OK is false when the root
// is not inside a git repository; every method then returns empty results.
type Git struct {
	Root      string
	Prefix    string // root's path inside the repo, e.g. "sub/dir/" ("" at the top)
	OK        bool
	HasRemote bool // the repo has at least one remote
}

// Status is a snapshot of `git status`.
type Status struct {
	Branch   string
	Files    map[string]string // root-relative path -> M, A, D, R, U or ? (index and worktree combined)
	Staged   map[string]string // changes in the index: M, A, D or R
	Unstaged map[string]string // changes in the worktree: M, D, U or ? (untracked)
	Upstream bool              // the branch tracks a remote branch
	Ahead    int               // commits not pushed (with no upstream: not on any remote)
	Behind   int
}

func emptyStatus() Status {
	return Status{Files: map[string]string{}, Staged: map[string]string{}, Unstaged: map[string]string{}}
}

func OpenGit(root string) Git {
	out, err := gitCmd(root, "rev-parse", "--show-prefix")
	if err != nil {
		return Git{Root: root}
	}
	g := Git{Root: root, Prefix: strings.TrimSpace(string(out)), OK: true}
	if rem, err := gitCmd(root, "remote"); err == nil && strings.TrimSpace(string(rem)) != "" {
		g.HasRemote = true
	}
	return g
}

func (g Git) Status() Status {
	if !g.OK {
		return emptyStatus()
	}
	out, err := gitCmd(g.Root, "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return emptyStatus()
	}
	st := parseStatus(out, g.Prefix)
	if !st.Upstream && g.HasRemote {
		// no upstream yet: count the commits that no remote has
		if n, err := gitCmd(g.Root, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
			st.Ahead, _ = strconv.Atoi(strings.TrimSpace(string(n)))
		}
	}
	return st
}

// parseStatus reads `git status --porcelain=v1 -z --branch`. Porcelain paths
// are relative to the repo top, so they are trimmed to be relative to root.
func parseStatus(out []byte, prefix string) Status {
	st := emptyStatus()
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if strings.HasPrefix(e, "## ") {
			st.Branch = parseBranch(e[3:])
			st.Upstream, st.Ahead, st.Behind = parseTracking(e[3:])
			continue
		}
		if len(e) < 4 {
			continue
		}
		x, y, p := e[0], e[1], e[3:]
		if x == 'R' || x == 'C' {
			i++ // the original path follows as its own entry
		}
		rel, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}
		st.Files[rel] = statusCode(x, y)
		switch code := st.Files[rel]; {
		case code == "?" || code == "U":
			st.Unstaged[rel] = code
		default:
			if c := sideCode(x); c != "" {
				st.Staged[rel] = c
			}
			if c := sideCode(y); c != "" {
				st.Unstaged[rel] = c
			}
		}
	}
	return st
}

// sideCode maps one porcelain column (index or worktree) to a status letter,
// "" when that side has no change.
func sideCode(c byte) string {
	switch c {
	case 'M', 'T':
		return "M"
	case 'A', 'C':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	}
	return ""
}

var trackRe = regexp.MustCompile(`(ahead|behind) (\d+)`)

// parseTracking reads the upstream part of the porcelain branch line,
// e.g. "main...origin/main [ahead 3, behind 1]".
func parseTracking(s string) (upstream bool, ahead, behind int) {
	if !strings.Contains(s, "...") {
		return false, 0, 0
	}
	if strings.Contains(s, "[gone]") {
		return false, 0, 0
	}
	for _, m := range trackRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[2])
		if m[1] == "ahead" {
			ahead = n
		} else {
			behind = n
		}
	}
	return true, ahead, behind
}

func parseBranch(s string) string {
	if b, ok := strings.CutPrefix(s, "No commits yet on "); ok {
		return b
	}
	if strings.HasPrefix(s, "HEAD (no branch)") {
		return "detached"
	}
	if i := strings.Index(s, "..."); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	return s
}

func statusCode(x, y byte) string {
	switch {
	case x == '?':
		return "?"
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return "U"
	}
	c := y
	if c == ' ' {
		c = x
	}
	switch c {
	case 'M', 'T':
		return "M"
	case 'A', 'C':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	}
	return string(c)
}

// Diff returns the unified diff of rel against HEAD. Untracked files are
// diffed against /dev/null so they show as entirely added.
func (g Git) Diff(rel string, untracked bool) (string, error) {
	if !g.OK {
		return "", errors.New("not a git repository")
	}
	if untracked {
		// --no-index exits 1 when files differ; the output is still valid.
		out, _ := gitCmd(g.Root, "diff", "--no-color", "--no-index", "--", "/dev/null", rel)
		return string(out), nil
	}
	out, err := gitCmd(g.Root, "diff", "--no-color", "HEAD", "--", rel)
	if err != nil {
		// A repo with no commits has no HEAD; fall back to the index.
		out, err = gitCmd(g.Root, "diff", "--no-color", "--", rel)
	}
	return string(out), err
}

// Committed is a file changed by a recent commit.
type Committed struct {
	Path string
	Time time.Time // commit time
}

// RecentCommitted lists up to n files changed by the latest commits, newest
// first, each path once. Paths are root-relative; files that no longer exist
// are left out.
func (g Git) RecentCommitted(n int) []Committed {
	if !g.OK || n <= 0 {
		return nil
	}
	// %x00 marks the commit lines, so a file named "1700000000" can't be
	// mistaken for a timestamp. --relative limits to root and trims the prefix.
	out, err := gitCmd(g.Root, "-c", "core.quotePath=false", "log", "-n", "100", "--format=%x00%ct",
		"--name-only", "--relative", "--diff-filter=d", "--no-merges")
	if err != nil {
		return nil
	}
	return parseCommitted(string(out), n, func(p string) bool {
		fi, err := os.Stat(filepath.Join(g.Root, filepath.FromSlash(p)))
		return err == nil && !fi.IsDir()
	})
}

func parseCommitted(out string, n int, exists func(string) bool) []Committed {
	var res []Committed
	seen := map[string]bool{}
	var t time.Time
	for _, l := range strings.Split(out, "\n") {
		if ts, ok := strings.CutPrefix(l, "\x00"); ok {
			if sec, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64); err == nil {
				t = time.Unix(sec, 0)
			}
			continue
		}
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		if exists(l) {
			res = append(res, Committed{l, t})
			if len(res) >= n {
				break
			}
		}
	}
	return res
}
