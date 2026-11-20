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
	sHunk     = lipgloss.NewStyle().Foreground(colCyan)
	sChip     = lipgloss.NewStyle().Background(colAccent).Foreground(lipgloss.Color("#1a1b26")).Bold(true).Padding(0, 1)
	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
)

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
