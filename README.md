# lens

A minimal, read-only repo viewer for the terminal, made to sit in a split next to Claude Code.
Sidebar file tree, syntax-highlighted viewer, VS Code-style quick open, search, and git diffs.

## Install

```sh
go install github.com/williamknipe/lens@latest   # or: go build -o ~/bin/lens .
```

```sh
lens            # current directory
lens ~/code/app # another directory
lens main.go    # open a file straight away
```

No other tools needed. If [ripgrep](https://github.com/BurntSushi/ripgrep) is installed, Ctrl+F uses it; otherwise a built-in search is used.

## Keys

| Key | Action |
| --- | --- |
| `ctrl+p` | Find file (fuzzy, top 5). Add `:42` to jump to a line |
| `ctrl+f` | Search text in all files, enter jumps to the line |
| `tab` | Switch between sidebar and viewer |
| `ctrl+b` | Hide / show sidebar |
| `j` `k` `↑` `↓` | Scroll the file (move in the sidebar) |
| `J` `K` `⇧↑` `⇧↓` | Move the cursor without scrolling |
| `H` `M` `L` | Cursor to top / middle / bottom of screen |
| `{` `}` | Jump to previous / next block (blank line) |
| `space` `pgdn` / `pgup` | Page down / up |
| `enter` | Open file / toggle folder |
| `l` `space` | Preview file but stay in the sidebar |
| `h` | Collapse folder / go to parent |
| `g` `G` | Top / bottom |
| `ctrl+d` `ctrl+u` | Half page down / up |
| `/` then `n` `N` | Search in file, next / previous match |
| `:` | Go to line |
| `d` | Toggle git diff vs HEAD (enter jumps to that line) |
| `y` / `Y` | Copy path / `path:line` (paste into Claude) |
| `r` | Refresh |
| `esc` | Clear search · leave diff · back to sidebar |
| `?` | Help |
| `q` | Quit |

The mouse works too: click to open, wheel to scroll.

## Cmd+P in Ghostty

Ghostty 1.4 doesn't use Cmd+P itself, and lens turns on the kitty keyboard protocol, so Ghostty should pass
Cmd+P to lens as `super+p` with **no config needed**, without changing anything in your other panes.
To check, press Cmd+P in lens, quit, and run `grep key "$(lens --log)"`. You should see `key "super+p"`.

If it doesn't show up, add this to Ghostty's config (`ghostty +edit-config`):

```
keybind = cmd+p=text:\x10
```

This makes Cmd+P send Ctrl+P in **every** pane: in zsh that recalls the previous command, in Claude Code it
moves up through prompt history, in vim it's completion. That's why it's not the default.

Cmd+F stays Ghostty's own scrollback search; use Ctrl+F for search in lens.

## Live updates

lens checks the open file and `git status` once a second. When Claude edits a file, the viewer reloads
it in place, and the sidebar shows git markers: `M` modified, `A` added, `?` untracked, `D` deleted, `•` folder
contains changes. It runs git with `GIT_OPTIONAL_LOCKS=0`, so it never blocks git commands in the other pane.

## Performance and the debug log

lens keeps a local timing log at `~/Library/Caches/lens/debug.log` (`lens --log` prints the path).
Nothing is sent anywhere. It records how long file opens, highlighting, indexing, search and `git diff`
take, plus anything slow (`SLOW`): `git status` over 100 ms, frames over 16 ms, fuzzy matching over 30 ms.
It also logs modifier key combos (like `ctrl+p`), but never what you type into search boxes. It rotates
at 5 MB. Set `LENS_DEBUG=0` to turn it off.

```sh
tail -f "$(lens --log)"          # watch it live from another pane
grep SLOW "$(lens --log)"        # find slow spots
go test ./... -bench . -run '^$' # benchmarks
```

Built-in safeguards:
- Files over 1,500 lines open instantly as plain text and colour in a moment later. The last 16
  highlighted files are cached, so switching back is instant.
- If `git status` takes over 100 ms, polling slows to every 2 seconds, and over 300 ms to every 5 seconds.
- Files over 1 MB aren't highlighted, and files over 10 MB aren't opened.

## Theme

Highlighting uses [chroma](https://github.com/alecthomas/chroma) (`monokai` on dark terminals, `github` on light).
Set `LENS_THEME` to any chroma style, e.g. `LENS_THEME=dracula lens`.
