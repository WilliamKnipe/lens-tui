package files

import (
	"errors"
	"strings"
)

// Git wraps git commands for one root directory. OK is false when the root
// is not inside a git repository; every method then returns empty results.
type Git struct {
	Root   string
	Prefix string // root's path inside the repo, e.g. "sub/dir/" ("" at the top)
	OK     bool
}

// Status is a snapshot of `git status`.
type Status struct {
	Branch string
	Files  map[string]string // root-relative path -> M, A, D, R, U or ?
}

func OpenGit(root string) Git {
	out, err := gitCmd(root, "rev-parse", "--show-prefix")
	if err != nil {
		return Git{Root: root}
	}
	return Git{Root: root, Prefix: strings.TrimSpace(string(out)), OK: true}
}

func (g Git) Status() Status {
	if !g.OK {
		return Status{Files: map[string]string{}}
	}
	out, err := gitCmd(g.Root, "status", "--porcelain=v1", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return Status{Files: map[string]string{}}
	}
	return parseStatus(out, g.Prefix)
}

// parseStatus reads `git status --porcelain=v1 -z --branch`. Porcelain paths
// are relative to the repo top, so they are trimmed to be relative to root.
func parseStatus(out []byte, prefix string) Status {
	st := Status{Files: map[string]string{}}
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if strings.HasPrefix(e, "## ") {
			st.Branch = parseBranch(e[3:])
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
	}
	return st
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
