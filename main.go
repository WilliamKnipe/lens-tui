// lens is a minimal read-only repository viewer for the terminal.
//
//	lens            # browse the current directory
//	lens ~/code/app # browse another directory
//	lens main.go    # open a file straight away
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/williamknipe/lens/internal/debug"
	"github.com/williamknipe/lens/internal/ui"
)

func main() {
	target := "."
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help":
			fmt.Println("usage: lens [dir | file]\n       lens --log    print the debug log path\n\n" +
				"Press ? inside lens for keys. Set LENS_THEME to any chroma style (e.g. dracula).\n" +
				"A timing log is kept at " + debug.Path() + " (LENS_DEBUG=0 turns it off).")
			return
		case "--log":
			fmt.Println(debug.Path())
			return
		}
		target = os.Args[1]
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fail(err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		fail(err)
	}
	root, open := abs, ""
	if !fi.IsDir() {
		// a file was given: browse the current directory if the file is
		// inside it, otherwise the file's own directory
		cwd, _ := os.Getwd()
		root = filepath.Dir(abs)
		if rel, err := filepath.Rel(cwd, abs); err == nil && filepath.IsLocal(rel) {
			root = cwd
		}
		rel, _ := filepath.Rel(root, abs)
		open = filepath.ToSlash(rel)
	}

	debug.Init()
	defer debug.Close()
	_, err = tea.NewProgram(ui.New(root, open)).Run()
	debug.Logf("exit err=%v", err)
	if err != nil {
		debug.Close()
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lens:", err)
	os.Exit(1)
}
