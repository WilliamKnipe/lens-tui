package files

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with: go test ./... -bench . -benchmem -run '^$'

func goSource(lines int) []string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"fmt\"\n\n")
	for i := 0; b.Len() < lines*40; i++ {
		fmt.Fprintf(&b, "// F%d does a thing.\nfunc F%d(x int) string {\n\tif x > %d {\n\t\treturn fmt.Sprintf(\"%%d\", x)\n\t}\n\treturn \"\"\n}\n\n", i, i, i)
	}
	return SplitLines(b.String())
}

func BenchmarkHighlight3kLines(b *testing.B) {
	src := goSource(3000)
	b.ResetTimer()
	for range b.N {
		Highlight("big.go", src)
	}
}

// repo makes a directory with n files spread over folders.
func repo(b *testing.B, n int, git bool) string {
	dir := b.TempDir()
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("pkg%d/sub%d/file%d.go", i%50, i%7, i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(fmt.Sprintf("package p\n// needle %d\nfunc F() {}\n", i)), 0o644)
	}
	if git {
		exec.Command("git", "-C", dir, "init", "-q").Run()
	}
	return dir
}

func BenchmarkListFilesWalk5k(b *testing.B) {
	dir := repo(b, 5000, false)
	b.ResetTimer()
	for range b.N {
		ListFiles(dir)
	}
}

func BenchmarkListFilesGit5k(b *testing.B) {
	dir := repo(b, 5000, true)
	b.ResetTimer()
	for range b.N {
		ListFiles(dir)
	}
}

func BenchmarkGitStatus5k(b *testing.B) {
	g := OpenGit(repo(b, 5000, true))
	b.ResetTimer()
	for range b.N {
		g.Status()
	}
}

func BenchmarkBuiltinGrep5k(b *testing.B) {
	dir := repo(b, 5000, true)
	b.ResetTimer()
	for range b.N {
		grep(b.Context(), dir, "needle 4999", 500)
	}
}
