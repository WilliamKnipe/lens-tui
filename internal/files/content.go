package files

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const (
	maxFileSize      = 10 << 20 // don't open files above 10 MB
	maxHighlightSize = 1 << 20  // show files above 1 MB as plain text
	tabWidth         = 4
)

// Content is a file ready to display.
type Content struct {
	Lines   []string // plain text, tabs expanded
	Styled  []string // same lines with ANSI colours (same length as Lines)
	Note    string   // set instead of Lines for binary, huge or unreadable files
	ModTime time.Time
	Size    int64

	Pending       bool          // Styled is still plain; call Colour to highlight
	HighlightTime time.Duration // time spent colouring (for the debug log)
	Cached        bool          // colours came from the cache
}

// Load reads and highlights the file at root/rel. Files with more than
// deferLines lines (0 = never) come back plain with Pending set, so they can
// be shown at once and coloured in the background with Colour.
func Load(root, rel string, deferLines int) Content {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Stat(abs)
	if err != nil {
		return Content{Note: "Cannot open: " + err.Error()}
	}
	c := Content{ModTime: fi.ModTime(), Size: fi.Size()}
	if fi.IsDir() {
		c.Note = "This is a directory"
		return c
	}
	if fi.Size() > maxFileSize {
		c.Note = fmt.Sprintf("File too large to show (%.1f MB)", float64(fi.Size())/(1<<20))
		return c
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		c.Note = "Cannot read: " + err.Error()
		return c
	}
	if IsBinary(data) {
		c.Note = fmt.Sprintf("Binary file (%d bytes)", len(data))
		return c
	}
	c.Lines = SplitLines(string(data))
	switch {
	case len(data) > maxHighlightSize:
		c.Styled = c.Lines
	case deferLines > 0 && len(c.Lines) > deferLines:
		if styled, ok := cache.get(root, rel, c); ok {
			c.Styled, c.Cached = styled, true
		} else {
			c.Styled, c.Pending = c.Lines, true
		}
	default:
		c.Styled = Colour(root, rel, &c)
	}
	return c
}

// Colour highlights c (using the cache when the file is unchanged) and
// records the time taken in c.HighlightTime.
func Colour(root, rel string, c *Content) []string {
	if styled, ok := cache.get(root, rel, *c); ok {
		c.Cached = true
		return styled
	}
	start := time.Now()
	styled := Highlight(rel, c.Lines)
	c.HighlightTime = time.Since(start)
	cache.put(root, rel, *c, styled)
	return styled
}

// cache keeps the highlighted lines of recently opened files, keyed by path
// and invalidated by modification time and size. It is cleared on theme change.
var cache = &styleCache{max: 16, m: map[string]*cacheEntry{}}

type cacheEntry struct {
	mod    time.Time
	size   int64
	styled []string
	used   time.Time
}

type styleCache struct {
	mu  sync.Mutex
	max int
	m   map[string]*cacheEntry
}

func (s *styleCache) get(root, rel string, c Content) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[root+"\x00"+rel]
	if !ok || !e.mod.Equal(c.ModTime) || e.size != c.Size || len(e.styled) != len(c.Lines) {
		return nil, false
	}
	e.used = time.Now()
	return e.styled, true
}

func (s *styleCache) put(root, rel string, c Content, styled []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= s.max {
		var oldest string
		for k, e := range s.m {
			if oldest == "" || e.used.Before(s.m[oldest].used) {
				oldest = k
			}
		}
		delete(s.m, oldest)
	}
	s.m[root+"\x00"+rel] = &cacheEntry{mod: c.ModTime, size: c.Size, styled: styled, used: time.Now()}
}

func (s *styleCache) clear() {
	s.mu.Lock()
	s.m = map[string]*cacheEntry{}
	s.mu.Unlock()
}

// IsBinary reports whether data looks binary (a NUL byte in the first 8 KB).
func IsBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// SplitLines splits text into lines, drops \r and expands tabs.
func SplitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = ExpandTabs(l)
	}
	return lines
}

// ExpandTabs replaces tabs with spaces up to the next tab stop.
func ExpandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

var (
	themeMu sync.RWMutex
	theme   = "monokai"
)

// SetTheme picks the chroma style used by Highlight (e.g. "monokai", "github").
func SetTheme(name string) {
	themeMu.Lock()
	theme = name
	themeMu.Unlock()
	cache.clear()
}

// Highlight returns lines coloured with ANSI escapes. Only foreground colour,
// bold, italic and underline are used, so the terminal's background shows
// through. The result always has the same number of lines as the input.
func Highlight(name string, lines []string) []string {
	src := strings.Join(lines, "\n")
	lexer := lexers.Match(filepath.Base(name))
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		return lines
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return lines
	}
	themeMu.RLock()
	style := styles.Get(theme)
	themeMu.RUnlock()

	cache := map[chroma.TokenType]string{}
	out := make([]string, 0, len(lines))
	for _, toks := range chroma.SplitTokensIntoLines(it.Tokens()) {
		var b strings.Builder
		for _, t := range toks {
			v := strings.TrimRight(t.Value, "\n")
			if v == "" {
				continue
			}
			esc, ok := cache[t.Type]
			if !ok {
				esc = sgr(style.Get(t.Type))
				cache[t.Type] = esc
			}
			if esc == "" {
				b.WriteString(v)
				continue
			}
			b.WriteString(esc)
			b.WriteString(v)
			b.WriteString("\x1b[0m")
		}
		out = append(out, b.String())
	}
	for len(out) < len(lines) {
		out = append(out, lines[len(out)])
	}
	return out[:len(lines)]
}

func sgr(e chroma.StyleEntry) string {
	var codes []string
	if e.Colour.IsSet() {
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", e.Colour.Red(), e.Colour.Green(), e.Colour.Blue()))
	}
	if e.Bold == chroma.Yes {
		codes = append(codes, "1")
	}
	if e.Italic == chroma.Yes {
		codes = append(codes, "3")
	}
	if e.Underline == chroma.Yes {
		codes = append(codes, "4")
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}
