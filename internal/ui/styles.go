package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	colAccent = lipgloss.Color("#7aa2f7")
	colDim    = lipgloss.Color("#6b7089")
	colFaint  = lipgloss.Color("#3b3f51")
	colSelBg  = lipgloss.Color("#2a2f45")
	colGreen  = lipgloss.Color("#9ece6a")
	colRed    = lipgloss.Color("#f7768e")
	colYellow = lipgloss.Color("#e0af68")
	colCyan   = lipgloss.Color("#7dcfff")

	sDim      = lipgloss.NewStyle().Foreground(colDim)
	sFaint    = lipgloss.NewStyle().Foreground(colFaint)
	sAccent   = lipgloss.NewStyle().Foreground(colAccent)
	sBold     = lipgloss.NewStyle().Bold(true)
	sDir      = lipgloss.NewStyle().Foreground(colAccent)
	sSel      = lipgloss.NewStyle().Background(colSelBg).Bold(true)
	sSelBlur  = lipgloss.NewStyle().Background(lipgloss.Color("#23263a"))
	sCurNum   = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	sMatch    = lipgloss.NewStyle().Background(lipgloss.Color("#4a4220")).Foreground(lipgloss.Color("#ffffff"))
	sCurMatch = lipgloss.NewStyle().Background(colYellow).Foreground(lipgloss.Color("#000000")).Bold(true)
	sFuzzyHit = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	sAdd      = lipgloss.NewStyle().Foreground(colGreen)
	sDel      = lipgloss.NewStyle().Foreground(colRed)
	sHunk     = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	sAddBg    = "\x1b[48;2;31;51;38m"
	sDelBg    = "\x1b[48;2;58;33;41m"
	sModBar   = lipgloss.NewStyle().Foreground(colYellow)
	sChip     = lipgloss.NewStyle().Background(colAccent).Foreground(lipgloss.Color("#1a1b26")).Bold(true).Padding(0, 1)
	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
)

// bar holds the styles for a panel's title bar. Every segment carries the
// bar's background, so colours inside the bar don't punch holes in it.
type bar struct {
	base, dim, bold, chip lipgloss.Style
	focused               bool
}

func newBar(focused bool) bar {
	bg, fg, dim := lipgloss.Color("#23263a"), lipgloss.Color("#c0caf5"), colDim
	chipBg, chipFg := colAccent, lipgloss.Color("#1a1b26")
	if focused {
		bg, fg, dim = colAccent, lipgloss.Color("#1a1b26"), lipgloss.Color("#2e3a5c")
		chipBg, chipFg = lipgloss.Color("#1a1b26"), colAccent
	}
	base := lipgloss.NewStyle().Background(bg).Foreground(fg)
	return bar{
		base:    base,
		dim:     base.Foreground(dim),
		bold:    base.Bold(true),
		chip:    lipgloss.NewStyle().Background(chipBg).Foreground(chipFg).Bold(true).Padding(0, 1),
		focused: focused,
	}
}

// mark renders a git status letter on the bar. On the blue bar the usual
// colours are hard to read, so it uses the bar's text colour.
func (b bar) mark(code string) string {
	if b.focused {
		return b.bold.Render(code)
	}
	return gitStyle(code).Background(b.base.GetBackground()).Render(code)
}

// fit lays out left and right (right-aligned) across exactly w cells,
// filling the gap with the bar's background.
func (b bar) fit(left, right string, w int) string {
	if w <= 0 {
		return ""
	}
	rw := ansi.StringWidth(right)
	if rw >= w {
		right, rw = "", 0
	}
	left = ansi.Truncate(left, w-rw, b.base.Render("…"))
	pad := w - rw - ansi.StringWidth(left)
	return left + b.base.Render(strings.Repeat(" ", max(0, pad))) + right
}

// gitStyle colours a git status letter.
func gitStyle(code string) lipgloss.Style {
	switch code {
	case "A", "?":
		return sAdd
	case "D", "U":
		return sDel
	default:
		return lipgloss.NewStyle().Foreground(colYellow)
	}
}

// fit truncates or pads s (which may contain ANSI codes) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// box draws lines inside a rounded border. Each line is fitted to inner width w.
func box(lines []string, w int) string {
	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	return sBox.Render(strings.Join(lines, "\n"))
}

// highlightRanges styles plain text, marking the byte ranges in hits.
// hits must be sorted and non-overlapping; cur (index into hits) gets the
// stronger style, -1 for none.
func highlightRanges(s string, hits [][2]int, cur int) string {
	var b strings.Builder
	last := 0
	for i, h := range hits {
		if h[0] < last || h[1] > len(s) || h[0] >= h[1] {
			continue
		}
		b.WriteString(s[last:h[0]])
		st := sMatch
		if i == cur {
			st = sCurMatch
		}
		b.WriteString(st.Render(s[h[0]:h[1]]))
		last = h[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

func ansiWidth(s string) int { return ansi.StringWidth(s) }

// asciiLower lower-cases ASCII letters only, so byte offsets stay valid for
// highlighting the original string.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// tintRow gives s (which may contain ANSI codes) the background bg, keeping
// it through any resets inside s.
func tintRow(s, bg string) string {
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bg)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+bg)
	return bg + s + "\x1b[0m"
}

// diffBg returns the row tint for an added or removed diff line.
func diffBg(l string) (string, bool) {
	switch {
	case strings.HasPrefix(l, "+"):
		return sAddBg, true
	case strings.HasPrefix(l, "-"):
		return sDelBg, true
	}
	return "", false
}
