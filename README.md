# lens

A minimal, read-only repo viewer for the terminal, made to sit in a split next to Claude Code.
Sidebar file tree, syntax-highlighted viewer, VS Code-style quick open, search, git diffs, and a follow
mode that jumps to whatever Claude just edited.

<img width="983" height="960" alt="Screenshot 2026-11-20 at 12 25 13" src="https://github.com/user-attachments/assets/9fd0dcc4-51cc-4ffc-bfe8-f2fe6e815cce" />

## Install

```sh
go install github.com/williamknipe/lens@latest   # or: go build -o ~/bin/lens .
```

```sh
lens            # current directory
lens ~/code/app # another directory
lens main.go    # open a file straight away
lens --log      # print the debug log path
lens --help     # usage
```

No other tools needed. If [ripgrep](https://github.com/BurntSushi/ripgrep) is installed, Ctrl+F uses it; otherwise a built-in search is used.

## Keys

| Key | Action |
| --- | --- |
| `ctrl+p` | Find file (fuzzy, top 5). Add `:42` to jump to a line. With an empty query it lists 5 recently changed and 5 recently viewed files: press `1`–`9`, `0` to open one |
| `ctrl+f` | Search text in all files, enter jumps to the line |
| `tab` / `shift+tab` | Next / previous file, through all folders in sidebar order (only changed files in diff view or the changed-files sidebar) |
| `cmd+←` / `cmd+→` | Focus sidebar / viewer |
| `ctrl+b` | Hide / show sidebar |
| `j` `k` `↑` `↓` | Scroll the file (move in the sidebar) |
| `J` `K` `⇧↑` `⇧↓` | Move the cursor without scrolling |
| `H` `M` `L` | Cursor to top / middle / bottom of screen |
| `{` `}` | Jump to previous / next block (blank line) |
| `space` `pgdn` / `pgup` | Page down / up |
| `enter` | Open file / toggle folder |
| `l` `space` | Preview file but stay in the sidebar |
| `h` | Collapse folder / go to parent (in the viewer: scroll left) |
| `l` `→` | In the viewer: scroll right. `0` resets to the left edge |
| `g` `G` | Top / bottom |
| `cmd+↓` `cmd+↑` | Half page down / up (`ctrl+d` `ctrl+u` also work) |
| `/` then `n` `N` | Search in file, next / previous match |
| `:` | Go to line |
| `d` | Toggle git diff vs HEAD (enter jumps to that line). In the sidebar: list changed files only; enter opens a file's diff |
| `y` / `Y` | Copy path / `path:line` (paste into Claude) |
| `f` | Follow mode: when a file changes, open it at the edited line (needs git) |
| `r` | Refresh |
| `esc` | Clear search · leave diff · back to sidebar |
| `?` | Help |
| `q` | Quit |

The mouse works too: click to open, wheel to scroll.

<img width="985" height="424" alt="Screenshot 2026-11-20 at 12 27 53" src="https://github.com/user-attachments/assets/843462a8-5db6-4b52-b015-e5c2d1eadcef" />

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

Cmd+F stays Ghostty's own scrollback search; use Ctrl+F for search in lens (Cmd+Shift+F also works when the
terminal sends it as `super+shift+f`).

## Live updates

lens checks the open file and `git status` once a second. When Claude edits a file, the viewer reloads
it in place, and the sidebar shows git markers: `M` modified, `A` added, `?` untracked, `D` deleted, `•` folder
contains changes. It runs git with `GIT_OPTIONAL_LOCKS=0`, so it never blocks git commands in the other pane.

## Follow mode

Press `f` to turn on follow mode (a green `FOLLOW` badge shows in the status bar). Whenever a file is
edited, lens opens it and puts the cursor on the change, so the viewer keeps up with Claude while you read:

- If the open file changes, the cursor moves to the first line that's different from what was on screen.
- If another file changes, lens switches to it, selects it in the sidebar, and lands on the line that was
  just edited (or its first change against HEAD, the first time lens sees the file change).
- The edited line is shown about 10 lines below the top of the view, so you can see what's above it.
- In diff view (or the changed-files sidebar), files open in diff view at the hunk that was just edited.
- It doesn't jump while Ctrl+P, Ctrl+F or a prompt is open.

Changed lines are highlighted in the normal view too, not just in diff view: a green bar in the gutter
for added lines, yellow for modified lines, and a red `▔` where lines were removed. New files aren't
highlighted, since every line would be.

Follow mode works from `git status`, so it needs a git repository and only sees files that differ from HEAD.
Press `f` again to turn it off.

## Recent files

Press Ctrl+P and don't type anything to see two numbered lists:

```
  Recently changed
› 1  model.go  internal/ui/          M  now
  2  finder.go  internal/ui/         M   3m
  Recently viewed
  3  README.md                       M
  4  git.go  internal/files/
```

- **Recently changed** shows up to 5 files from `git status`, newest edit first, with their git status
  and how long ago they changed.
- **Recently viewed** shows up to 5 files you've opened, newest first, leaving out the file that's open now.

Press `1`–`9` or `0` to open one, or use the arrow keys and enter. Once you've typed something, digits go
into the search as normal.

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
