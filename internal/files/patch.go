package files

import "strings"

// Hunk is one "@@ … @@" block of a unified diff, lines kept byte for byte
// (tabs and all) so it can be applied as a patch.
type Hunk struct {
	Header string   // the @@ line
	Lines  []string // the lines after it, each starting with ' ', '+', '-' or '\'
}

// FileDiff is the diff of one file split into its header and hunks.
type FileDiff struct {
	Header []string // "diff --git", "index", "---", "+++" … lines
	Hunks  []Hunk
}

// ParseDiff splits the `git diff` output of a single file.
func ParseDiff(out string) FileDiff {
	var d FileDiff
	if out == "" {
		return d
	}
	lines := strings.Split(out, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"):
			d.Hunks = append(d.Hunks, Hunk{Header: l})
		case len(d.Hunks) == 0:
			d.Header = append(d.Header, l)
		default:
			h := &d.Hunks[len(d.Hunks)-1]
			h.Lines = append(h.Lines, l)
		}
	}
	return d
}

// Patch is a patch holding only hunk i, for `git apply --cached`. Each hunk's
// line numbers are relative to the same base, so a single hunk applies on
// its own.
func (d FileDiff) Patch(i int) string {
	var b strings.Builder
	for _, l := range d.Header {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	h := d.Hunks[i]
	b.WriteString(h.Header)
	b.WriteByte('\n')
	for _, l := range h.Lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}
