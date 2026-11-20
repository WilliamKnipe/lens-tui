// Package files holds everything lens reads from disk: the file index,
// file contents, git state, ripgrep results and syntax highlighting.
package files

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// MaxIndex caps how many files the Ctrl+P index holds.
const MaxIndex = 200_000

// skipDirs are never walked when the root is not a git repo.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	".venv": true, "__pycache__": true, ".idea": true, ".next": true,
}

// ListFiles returns every file under root as a slash-separated path relative
// to root. Inside a git repo it asks git, so .gitignore is respected.
func ListFiles(root string) []string {
	out, err := gitCmd(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return walk(root)
	}
	seen := make(map[string]bool)
	var res []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		// --cached still lists files deleted from the working tree, and
		// submodules show up as directories.
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() {
			continue
		}
		res = append(res, p)
		if len(res) >= MaxIndex {
			break
		}
	}
	sort.Strings(res)
	return res
}

func walk(root string) []string {
	var res []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		res = append(res, filepath.ToSlash(rel))
		if len(res) >= MaxIndex {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(res)
	return res
}

// gitCmd runs git in root. GIT_OPTIONAL_LOCKS=0 stops our polling from
// taking the index lock while Claude (or you) run git in the other pane.
func gitCmd(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}
