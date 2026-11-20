package ui

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// hiddenNames never appear in the sidebar.
var hiddenNames = map[string]bool{".git": true, ".DS_Store": true}

type node struct {
	name   string
	rel    string // slash path relative to the root
	isDir  bool
	open   bool
	loaded bool
	depth  int
	parent *node
	kids   []*node
}

// tree is the expandable sidebar. Directories are read lazily on expand.
type tree struct {
	root   string
	top    *node
	rows   []*node // visible rows, in display order
	cursor int
	offset int
	height int
	marks  map[string]string // git status by path, including parent dirs

	changed     bool    // show only changed files, as a flat list
	changedRows []*node // flat nodes for the changed list
}

func newTree(root string) *tree {
	t := &tree{root: root, top: &node{isDir: true, open: true, depth: -1}, marks: map[string]string{}}
	t.load(t.top)
	t.rebuild()
	return t
}

// load (re)reads a directory, keeping existing child nodes so their
// expanded state survives a refresh.
func (t *tree) load(n *node) {
	entries, err := os.ReadDir(filepath.Join(t.root, filepath.FromSlash(n.rel)))
	if err != nil {
		n.kids, n.loaded = nil, true
		return
	}
	old := make(map[string]*node, len(n.kids))
	for _, k := range n.kids {
		old[k.name] = k
	}
	kids := make([]*node, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if hiddenNames[name] {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(t.root, filepath.FromSlash(n.rel), name)); err == nil {
				isDir = fi.IsDir()
			}
		}
		if k, ok := old[name]; ok && k.isDir == isDir {
			kids = append(kids, k)
			continue
		}
		kids = append(kids, &node{name: name, rel: path.Join(n.rel, name), isDir: isDir, depth: n.depth + 1, parent: n})
	}
	sort.SliceStable(kids, func(i, j int) bool {
		if kids[i].isDir != kids[j].isDir {
			return kids[i].isDir
		}
		return strings.ToLower(kids[i].name) < strings.ToLower(kids[j].name)
	})
	n.kids, n.loaded = kids, true
}

func (t *tree) rebuild() {
	t.rows = t.rows[:0]
	if t.changed {
		old := make(map[string]*node, len(t.changedRows))
		for _, n := range t.changedRows {
			old[n.rel] = n
		}
		t.changedRows = t.changedRows[:0]
		for p, c := range t.marks {
			if c == "•" { // a folder holding changes
				continue
			}
			n := old[p]
			if n == nil {
				n = &node{name: p, rel: p}
			}
			t.changedRows = append(t.changedRows, n)
		}
		sort.Slice(t.changedRows, func(i, j int) bool { return t.changedRows[i].rel < t.changedRows[j].rel })
		t.rows = append(t.rows, t.changedRows...)
		t.cursor = clamp(t.cursor, 0, len(t.rows)-1)
		t.ensureVisible()
		return
	}
	var walk func(n *node)
	walk = func(n *node) {
		for _, k := range n.kids {
			t.rows = append(t.rows, k)
			if k.isDir && k.open {
				walk(k)
			}
		}
	}
	walk(t.top)
	t.cursor = clamp(t.cursor, 0, len(t.rows)-1)
	t.ensureVisible()
}

// refresh re-reads every open directory, keeping the cursor on the same path.
func (t *tree) refresh() {
	sel := ""
	if n := t.selected(); n != nil {
		sel = n.rel
	}
	var walk func(n *node)
	walk = func(n *node) {
		t.load(n)
		for _, k := range n.kids {
			if k.isDir && k.open {
				walk(k)
			}
		}
	}
	walk(t.top)
	t.rebuild()
	if sel != "" {
		t.selectPath(sel)
	}
}

func (t *tree) selected() *node {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return nil
	}
	return t.rows[t.cursor]
}

func (t *tree) selectPath(rel string) bool {
	for i, r := range t.rows {
		if r.rel == rel {
			t.cursor = i
			t.ensureVisible()
			return true
		}
	}
	return false
}

func (t *tree) move(d int) {
	t.cursor = clamp(t.cursor+d, 0, len(t.rows)-1)
	t.ensureVisible()
}

// scroll moves the viewport (mouse wheel), dragging the cursor along.
func (t *tree) scroll(d int) {
	t.offset = clamp(t.offset+d, 0, len(t.rows)-t.height)
	t.cursor = clamp(t.cursor, t.offset, t.offset+t.height-1)
	t.cursor = clamp(t.cursor, 0, len(t.rows)-1)
}

func (t *tree) ensureVisible() {
	if t.height <= 0 {
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	t.offset = clamp(t.offset, 0, len(t.rows)-t.height)
}

// toggle expands or collapses the selected directory.
func (t *tree) toggle() {
	n := t.selected()
	if n == nil || !n.isDir {
		return
	}
	n.open = !n.open
	if n.open {
		t.load(n)
	}
	t.rebuild()
}

// collapse closes the selected dir, or jumps to the parent dir.
func (t *tree) collapse() {
	n := t.selected()
	if n == nil {
		return
	}
	if n.isDir && n.open {
		n.open = false
		t.rebuild()
		return
	}
	if n.parent != nil && n.parent != t.top {
		t.selectPath(n.parent.rel)
	}
}

// reveal expands every parent of rel and selects it.
func (t *tree) reveal(rel string) {
	n := t.top
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		var next *node
		for _, k := range n.kids {
			if k.name == p && k.isDir {
				next = k
				break
			}
		}
		if next == nil {
			break
		}
		if !next.loaded {
			t.load(next)
		}
		next.open = true
		n = next
	}
	t.rebuild()
	t.selectPath(rel)
}

// setStatus stores git markers. Parent directories of changed files get a dot.
func (t *tree) setStatus(files map[string]string) {
	marks := make(map[string]string, len(files))
	for p, c := range files {
		marks[p] = c
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if _, ok := marks[d]; ok {
				break
			}
			marks[d] = "•"
		}
	}
	t.marks = marks
	if t.changed {
		sel := ""
		if n := t.selected(); n != nil {
			sel = n.rel
		}
		t.rebuild()
		if sel != "" {
			t.selectPath(sel)
		}
	}
}

// toggleChanged switches between the full tree and a flat list of changed
// files, keeping the cursor on the same file when it is in both.
func (t *tree) toggleChanged() {
	sel := ""
	if n := t.selected(); n != nil {
		sel = n.rel
	}
	t.changed = !t.changed
	t.cursor, t.offset = 0, 0
	t.rebuild()
	if sel != "" {
		t.selectPath(sel)
	}
}

// render returns exactly t.height lines of width w.
func (t *tree) render(w int, focused bool) []string {
	out := make([]string, 0, t.height)
	for i := t.offset; i < t.offset+t.height; i++ {
		if i >= len(t.rows) {
			out = append(out, strings.Repeat(" ", w))
			continue
		}
		n := t.rows[i]
		indent := strings.Repeat("  ", n.depth)
		icon := "  "
		if n.isDir {
			icon = "▸ "
			if n.open {
				icon = "▾ "
			}
		}
		mark := t.marks[n.rel]
		markW := 0
		if mark != "" {
			markW = 2
		}
		label := indent + icon + n.name
		if i == t.cursor {
			st := sSelBlur
			if focused {
				st = sSel
			}
			row := fit(" "+label, w-markW)
			if mark != "" {
				row += " " + mark
			}
			out = append(out, st.Render(fit(row, w)))
			continue
		}
		name := n.name
		if n.isDir {
			name = sDir.Render(name)
		} else if mark != "" {
			name = gitStyle(mark).Render(name)
		}
		row := fit(" "+indent+sDim.Render(icon)+name, w-markW)
		if mark != "" {
			row += " " + gitStyle(mark).Render(mark)
		}
		out = append(out, row)
	}
	return out
}
