package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func BenchmarkFuzzy50k(b *testing.B) {
	list := make([]string, 50_000)
	for i := range list {
		list[i] = fmt.Sprintf("packages/mod%d/internal/feature%d/component_%d.tsx", i%97, i%13, i)
	}
	f := newFinder()
	f.setFiles(list)
	f.input.SetValue("featcomp42")
	b.ResetTimer()
	for range b.N {
		f.refilter()
	}
}

func BenchmarkRenderFrame(b *testing.B) {
	m := New(b.TempDir(), "")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	lines := make([]string, 5000)
	for i := range lines {
		lines[i] = fmt.Sprintf("\x1b[38;2;200;100;50mfunc\x1b[0m F%d() { return %d }", i, i)
	}
	m.view.rel, m.view.loaded, m.view.lines, m.view.styled = "x.go", true, lines, lines
	b.ResetTimer()
	for range b.N {
		m.render()
	}
}

func TestPollInterval(t *testing.T) {
	for took, want := range map[time.Duration]time.Duration{
		20 * time.Millisecond:  pollFast,
		150 * time.Millisecond: pollMid,
		time.Second:            pollSlow,
	} {
		if got := pollInterval(took); got != want {
			t.Errorf("pollInterval(%s) = %s, want %s", took, got, want)
		}
	}
}
