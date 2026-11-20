package ui

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/williamknipe/lens/internal/files"
)

type match struct{ line, start, end int }

// viewer is the main pane: a read-only file view with a current line,
// in-file search highlights and a git diff mode.
type viewer struct {
	rel    string
	loaded bool
	lines  []string // plain text
	styled []string // highlighted text
	note   string   // message shown instead of content
	mod    time.Time
	size   int64

	diff      bool
	diffLines []string // coloured
	diffPlain []string
	diffNums  []int // new-file line number per diff line (0 = removed/header)
	diffNote  string
	fileCur   int // file position saved while showing the diff
	fileOff   int

	marks map[int]byte // 1-based file line -> change against HEAD (see changeMarks)

	cur, off, xoff int
	width, height  int

	query   string
	matches []match
	mi      int // current match index
}

func (v *viewer) count() int {
	if v.diff {
		return len(v.diffLines)
	}
	return len(v.lines)
}

func (v *viewer) plainLine(i int) string {
	if v.diff {
		return v.diffPlain[i]
	}
	return v.lines[i]
}

// fileLine is the 1-based line in the file under the cursor (0 if none).
func (v *viewer) fileLine() int {
	if v.diff {
		if v.cur < len(v.diffNums) {
			return v.diffNums[v.cur]
		}
		return 0
	}
	if len(v.lines) == 0 {
		return 0
	}
	return v.cur + 1
}

func (v *viewer) clamp() {
	n := v.count()
	v.cur = clamp(v.cur, 0, n-1)
	v.off = clamp(v.off, 0, n-v.height)
}

func (v *viewer) ensureVisible() {
	if v.cur < v.off {
		v.off = v.cur
	}
	if v.height > 0 && v.cur >= v.off+v.height {
		v.off = v.cur - v.height + 1
	}
	v.clamp()
}

func (v *viewer) move(d int) {
	v.cur = clamp(v.cur+d, 0, v.count()-1)
	v.ensureVisible()
}

// moveTo puts the cursor on line i, centring it if it was off screen.
func (v *viewer) moveTo(i int) {
	v.cur = clamp(i, 0, v.count()-1)
	if v.cur < v.off || v.cur >= v.off+v.height {
		v.off = v.cur - v.height/3
	}
	v.clamp()
}

// reveal puts the cursor on line i with some context above it: the line
// ends up about 10 rows below the top unless it is already comfortably on
// screen.
func (v *viewer) reveal(i int) {
	v.cur = clamp(i, 0, v.count()-1)
	ctx := min(10, v.height/3)
	if v.cur < v.off+ctx || v.cur >= v.off+v.height-ctx {
		v.off = v.cur - ctx
	}
	v.clamp()
}

// scroll moves the view (mouse wheel / page keys) and drags the cursor along.
func (v *viewer) scroll(d int) {
	v.off = clamp(v.off+d, 0, v.count()-v.height)
	v.cur = clamp(v.cur+d, v.off, v.off+v.height-1)
	v.clamp()
}

// cursorToScreen puts the cursor at the top (0), middle (1) or bottom (2)
// of the visible lines.
func (v *viewer) cursorToScreen(where int) {
	last := min(v.off+v.height, v.count()) - 1
	switch where {
	case 0:
		v.cur = v.off
	case 1:
		v.cur = v.off + (last-v.off)/2
	default:
		v.cur = last
	}
	v.clamp()
}

// jumpBlock moves the cursor to the next (dir=1) or previous (dir=-1)
// blank line, i.e. between blocks of code, like vim's { and }.
func (v *viewer) jumpBlock(dir int) {
	blank := func(i int) bool { return strings.TrimSpace(v.plainLine(i)) == "" }
	i := v.cur + dir
	for i > 0 && i < v.count()-1 && blank(i) { // skip the current gap
		i += dir
	}
	for i > 0 && i < v.count()-1 && !blank(i) {
		i += dir
	}
	v.cur = clamp(i, 0, v.count()-1)
	v.ensureVisible()
}

func (v *viewer) setFile(rel string, c files.Content, target int) {
	same := rel == v.rel && v.loaded
	v.rel, v.loaded = rel, true
	v.lines, v.styled, v.note, v.mod, v.size = c.Lines, c.Styled, c.Note, c.ModTime, c.Size
	if !same {
		v.cur, v.off, v.xoff, v.diff = 0, 0, 0, false
		v.marks = nil
	}
	v.findMatches()
	if target > 0 {
		v.moveTo(target - 1)
		v.syncMatch()
	} else {
		v.clamp()
	}
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)

// Change marks for lines of the open file.
const (
	markAdd = '+' // new line
	markMod = '~' // replaces a removed line
	markDel = '_' // lines were removed just above this one
)

// changeMarks reads a unified diff and marks the changed lines of the new
// file, keyed by 1-based line number.
func changeMarks(diff string) map[int]byte {
	marks := map[int]byte{}
	n, del, inHunk := 0, 0, false
	for _, l := range files.SplitLines(diff) {
		if mm := hunkRe.FindStringSubmatch(l); mm != nil {
			n, _ = strconv.Atoi(mm[1])
			del, inHunk = 0, true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(l, "-"):
			del++
		case strings.HasPrefix(l, "+"):
			if del > 0 {
				marks[n] = markMod
				del--
			} else {
				marks[n] = markAdd
			}
			n++
		case strings.HasPrefix(l, `\`):
		default:
			if del > 0 {
				marks[n] = markDel
			}
			del = 0
			n++
		}
	}
	return marks
}

// highlightDiff syntax-colours the code part of each diff line (everything
// after the +/-/space marker), so comments and strings stand out.
func (v *viewer) highlightDiff() {
	var idx []int
	var code []string
	for i, l := range v.diffPlain {
		if v.diffNums[i] == 0 && !strings.HasPrefix(l, "-") || strings.HasPrefix(l, `\`) {
			continue // hunk header / "no newline" marker
		}
		idx = append(idx, i)
		code = append(code, files.ExpandTabs(l[1:]))
	}
	if len(code) == 0 {
		return
	}
	hl := files.Highlight(v.rel, code)
	for k, i := range idx {
		l := v.diffPlain[i]
		v.diffLines[i] = l[:1] + hl[k]
		switch l[0] {
		case '+':
			v.diffLines[i] = sAdd.Render("+") + hl[k]
		case '-':
			v.diffLines[i] = sDel.Render("-") + hl[k]
		}
	}
}

// setDiff loads `git diff` output. Header lines before the first hunk are dropped.
func (v *viewer) setDiff(out string, err error, first bool) {
	v.diffLines, v.diffPlain, v.diffNums, v.diffNote = nil, nil, nil, ""
	if err != nil {
		v.diffNote = "git diff failed: " + err.Error()
	}
	inHunk, n := false, 0
	for _, l := range files.SplitLines(out) {
		if m := hunkRe.FindStringSubmatch(l); m != nil {
			inHunk = true
			n, _ = strconv.Atoi(m[1])
			v.diffPlain = append(v.diffPlain, l)
			v.diffLines = append(v.diffLines, sHunk.Render(l))
			v.diffNums = append(v.diffNums, 0)
			continue
		}
		if !inHunk {
			continue
		}
		num := 0
		styled := l
		switch {
		case strings.HasPrefix(l, "+"):
			num, styled = n, sAdd.Render(l)
			n++
		case strings.HasPrefix(l, "-"):
			styled = sDel.Render(l)
		case strings.HasPrefix(l, `\`):
			styled = sDim.Render(l)
		default:
			num = n
			n++
		}
		v.diffPlain = append(v.diffPlain, l)
		v.diffLines = append(v.diffLines, styled)
		v.diffNums = append(v.diffNums, num)
	}
	v.highlightDiff()
	if len(v.diffLines) == 0 && v.diffNote == "" {
		v.diffNote = "No changes against HEAD"
	}
	if first {
		// start on the first change near where we were in the file
		v.cur, v.off = 0, 0
		for i, l := range v.diffPlain {
			if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && (v.diffNums[i] == 0 || v.diffNums[i] >= v.fileCur) {
				v.cur = i
				break
			}
		}
		v.reveal(v.cur)
	}
	v.findMatches()
	v.clamp()
}

// showFileLine moves the diff cursor to the first diff line at or after
// 1-based file line n.
func (v *viewer) showFileLine(n int) {
	for i, num := range v.diffNums {
		if num >= n {
			v.reveal(i)
			return
		}
	}
}

func (v *viewer) enterDiff() {
	v.fileCur, v.fileOff = v.cur, v.off
	v.diff = true
	v.diffLines, v.diffPlain, v.diffNums = nil, nil, nil
	v.diffNote = "Loading diff…"
	v.cur, v.off = 0, 0
	v.findMatches()
}

// leaveDiff returns to the file, optionally at a specific 1-based line.
func (v *viewer) leaveDiff(line int) {
	v.diff = false
	v.cur, v.off = v.fileCur, v.fileOff
	v.findMatches()
	if line > 0 {
		v.moveTo(line - 1)
	}
	v.clamp()
}

// setQuery sets the in-file search. Smart case: case-sensitive only if the
// query contains an upper-case letter.
func (v *viewer) setQuery(q string) {
	v.query = q
	v.findMatches()
}

func (v *viewer) findMatches() {
	v.matches, v.mi = nil, 0
	if v.query == "" {
		return
	}
	q := v.query
	fold := asciiLower(q) == q
	for i := 0; i < v.count(); i++ {
		l := v.plainLine(i)
		if fold {
			l = asciiLower(l)
		}
		for at := 0; ; {
			j := strings.Index(l[at:], q)
			if j < 0 {
				break
			}
			v.matches = append(v.matches, match{i, at + j, at + j + len(q)})
			at += j + len(q)
		}
		if len(v.matches) > 50_000 {
			break
		}
	}
}

// syncMatch makes the current match the first one at or after the cursor.
func (v *viewer) syncMatch() {
	for i, m := range v.matches {
		if m.line >= v.cur {
			v.mi = i
			return
		}
	}
	v.mi = 0
}

// jumpMatch moves to the next (dir=1) or previous (dir=-1) match.
func (v *viewer) jumpMatch(dir int) bool {
	if len(v.matches) == 0 {
		return false
	}
	v.mi = (v.mi + dir + len(v.matches)) % len(v.matches)
	m := v.matches[v.mi]
	v.moveTo(m.line)
	v.showCol(m.start)
	return true
}

// jumpFirstFrom moves to the first match at or after line (for incremental search).
func (v *viewer) jumpFirstFrom(line int) {
	if len(v.matches) == 0 {
		v.moveTo(line)
		return
	}
	v.mi = len(v.matches) - 1
	for i, m := range v.matches {
		if m.line >= line {
			v.mi = i
			break
		}
	}
	m := v.matches[v.mi]
	v.moveTo(m.line)
	v.showCol(m.start)
}

// showCol scrolls horizontally so byte column c of the current line is visible.
func (v *viewer) showCol(c int) {
	avail := v.width - v.gutterWidth()
	col := ansi.StringWidth(v.plainLine(v.cur)[:min(c, len(v.plainLine(v.cur)))])
	if col < v.xoff || col >= v.xoff+avail-4 {
		v.xoff = max(0, col-avail/2)
	}
}

func (v *viewer) gutterWidth() int {
	if v.diff {
		return 7
	}
	return len(strconv.Itoa(max(len(v.lines), 1))) + 3
}

// title is the header line above the content.
func (v *viewer) title(w int, gitMark string, focused, follow bool) string {
	b := newBar(focused)
	var right string
	if follow {
		right = b.chip.Render("FOLLOW") + b.base.Render(" ")
	}
	if v.rel == "" {
		return b.fit(b.dim.Render(" no file open"), right, w)
	}
	dir, name := path.Split(v.rel)
	t := b.base.Render(" ") + b.dim.Render(dir) + b.bold.Render(name)
	if gitMark != "" {
		t += b.base.Render(" ") + b.mark(gitMark)
	}
	if v.diff {
		t += b.base.Render("  ") + b.chip.Render("DIFF")
	}
	if v.xoff > 0 {
		t += b.dim.Render(fmt.Sprintf("  →%d", v.xoff))
	}
	return b.fit(t, right, w)
}

// render returns exactly v.height lines of width v.width.
func (v *viewer) render(focused bool) []string {
	out := make([]string, 0, v.height)
	note := v.note
	if v.diff && v.diffNote != "" {
		note = v.diffNote
	}
	if !v.loaded || note != "" || v.count() == 0 {
		msg := note
		switch {
		case !v.loaded:
			return v.welcome()
		case msg == "":
			msg = "Empty file"
		}
		for i := 0; i < v.height; i++ {
			l := ""
			if i == v.height/3 {
				l = strings.Repeat(" ", max(0, (v.width-len(msg))/2)) + sDim.Render(msg)
			}
			out = append(out, fit(l, v.width))
		}
		return out
	}

	// matches grouped by line for the visible window
	hits := map[int][][2]int{}
	curHit := map[int]int{}
	for i, m := range v.matches {
		if m.line < v.off || m.line >= v.off+v.height {
			continue
		}
		if i == v.mi {
			curHit[m.line] = len(hits[m.line])
		}
		hits[m.line] = append(hits[m.line], [2]int{m.start, m.end})
	}

	gw := v.gutterWidth()
	avail := max(0, v.width-gw)
	numW := gw - 3
	for i := v.off; i < v.off+v.height; i++ {
		if i >= v.count() {
			out = append(out, fit(sFaint.Render(" ~"), v.width))
			continue
		}
		marker := " "
		if i == v.cur && focused {
			marker = sAccent.Render("▎")
		}
		var num string
		if v.diff {
			numW = 4
			if n := v.diffNums[i]; n > 0 {
				num = strconv.Itoa(n)
			}
		} else {
			num = strconv.Itoa(i + 1)
		}
		num = fmt.Sprintf("%*s", numW, num)
		if i == v.cur {
			num = sCurNum.Render(num)
		} else {
			num = sFaint.Render(num)
		}
		bar, tint := " ", ""
		if !v.diff {
			switch v.marks[i+1] {
			case markAdd:
				bar, tint = sAdd.Render("▎"), sAddBg
			case markMod:
				bar, tint = sModBar.Render("▎"), sAddBg
			case markDel:
				bar = sDel.Render("▔")
			}
		}
		gutter := marker + num + bar + " "

		var body string
		if h, ok := hits[i]; ok {
			c, isCur := curHit[i]
			if !isCur {
				c = -1
			}
			body = highlightRanges(v.plainLine(i), h, c)
		} else if v.diff {
			body = v.diffLines[i]
			if st, ok := diffBg(v.diffPlain[i]); ok {
				// full-width tinted row; cut the plain text so the tint spans the pane
				bg := st
				pl := ansi.Cut(v.diffLines[i], v.xoff, v.xoff+avail)
				pl = fit(pl, avail)
				pl = bg + strings.ReplaceAll(pl, "\x1b[0m", "\x1b[0m"+bg) + "\x1b[0m"
				out = append(out, fit(gutter+pl, v.width))
				continue
			}
		} else {
			body = v.styled[i]
		}
		if v.xoff > 0 {
			body = ansi.Cut(body, v.xoff, v.xoff+avail)
		}
		if tint != "" {
			out = append(out, fit(gutter+tintRow(fit(body, avail), tint), v.width))
			continue
		}
		out = append(out, fit(gutter+fit(body, avail), v.width))
	}
	return out
}

func (v *viewer) welcome() []string {
	lines := []string{
		sAccent.Bold(true).Render("lens"),
		"",
		sDim.Render("ctrl+p   find file"),
		sDim.Render("ctrl+f   search in files"),
		sDim.Render("enter    open from sidebar"),
		sDim.Render("?        all keys"),
	}
	out := make([]string, v.height)
	top := max(0, v.height/3-2)
	for i := range out {
		l := ""
		if j := i - top; j >= 0 && j < len(lines) {
			l = strings.Repeat(" ", max(0, v.width/2-12)) + lines[j]
		}
		out[i] = fit(l, v.width)
	}
	return out
}
