# lens

A minimal repo viewer for the terminal, made to sit in a split next to Claude Code: file tree,
syntax-highlighted viewer, quick open, search, git diffs, a follow mode that jumps to whatever Claude just
edited, and hunk-by-hunk review with staging, commits and push. It never edits your files; it only writes
to git.

<img width="983" height="960" alt="Screenshot 2026-11-20 at 12 25 13" src="https://github.com/user-attachments/assets/9fd0dcc4-51cc-4ffc-bfe8-f2fe6e815cce" />

## Install

```sh
go install github.com/williamknipe/lens-tui/cmd/lens@latest   # or, from a checkout: go install ./cmd/lens
```

```sh
lens            # current directory
lens ~/code/app # another directory
lens main.go    # open a file straight away
lens --log      # print the debug log path
```

Nothing else is required. Ctrl+F uses [ripgrep](https://github.com/BurntSushi/ripgrep) if it's installed,
and a built-in search otherwise.

## Keys

| Key | Action |
| --- | --- |
| `ctrl+p` | Find file. Add `:42` to jump to a line. Empty query: recent files, `1`–`9` `0` to open |
| `ctrl+f` | Search all files |
| `tab` / `shift+tab` | Next / previous file (only changed files in diff view or review) |
| `cmd+←` / `cmd+→` | Focus sidebar / viewer |
| `ctrl+b` | Hide / show sidebar |
| `j` `k` `↑` `↓` | Scroll (move in the sidebar) |
| `J` `K` `⇧↑` `⇧↓` | Move the cursor without scrolling |
| `H` `M` `L` | Cursor to top / middle / bottom of screen |
| `{` `}` | Previous / next block; in diff view, previous / next hunk |
| `space` `pgdn` / `pgup` | Page down / up |
| `cmd+↓` `cmd+↑` | Half page down / up (also `ctrl+d` `ctrl+u`) |
| `g` `G` | Top / bottom |
| `enter` | Open file / toggle folder |
| `l` `space` | In the sidebar: preview file |
| `h` `l` `←` `→` | In the sidebar: collapse / expand. In the viewer: scroll sideways (`0` resets) |
| `/` then `n` `N` | Search in file, next / previous match |
| `:` | Go to line |
| `d` | Toggle git diff (enter jumps to that line). In the sidebar: review changed files |
| `s` / `S` | Stage hunk / file |
| `u` / `U` | Unstage hunk / file |
| `c` | Commit |
| `P` | Unpushed commits and push |
| `y` / `Y` | Copy path / `path:line` (to paste into Claude) |
| `f` | Follow mode |
| `r` | Refresh |
| `esc` | Clear search · leave diff · back to sidebar |
| `?` / `q` | Help / quit |

The mouse works too: click to open, wheel to scroll.

<img width="985" height="424" alt="Screenshot 2026-11-20 at 12 27 53" src="https://github.com/user-attachments/assets/843462a8-5db6-4b52-b015-e5c2d1eadcef" />

## Review, commit, push

Three separate steps, so you can build up small commits and push when you're ready.

**Review.** Press `d` in the sidebar. It lists **Changes** (to review) above **Staged** (reviewed). The title
shows `REVIEW 4/9`: the number of files with nothing left to review, out of all changed files. The diff shows
unstaged hunks first, then the staged ones (marked `✓`) below a `── staged ──` line.

- `{` `}` move between hunks. `s` stages the current hunk and moves to the next. After a file's last hunk,
  it opens the next file that still has changes.
- `S` stages the whole file and moves on. `u` / `U` unstage. In the sidebar, `s` / `u` work on whole files.

Staging is how you mark something reviewed; lens keeps no state of its own. If Claude edits a file after you
staged it, only the new edit shows under Changes.

**Commit.** Press `c`. The message's first line is the subject (the counter turns yellow past 50 characters,
red past 72); `shift+enter` adds body lines. Below the message are the staged files with line counts, the
files that won't be committed, and a diff of the selected file.

- `enter` commits. If nothing is staged, `enter` commits every change (`git add -A && git commit`).
- `tab` moves to the file list: `j` `k` select, `space` stages or unstages.
- `esc` closes and keeps the message as a draft. If a commit hook fails, its output shows and the message stays.
- lens only ever adds commits. It never amends or rewrites history.

<img width="1115" height="585" alt="Screenshot 2026-11-20 at 20 21 10" src="https://github.com/user-attachments/assets/406d7423-3fa3-4312-ab0d-ce56a48ea110" />

**Push.** The status bar shows `↑3 P push` when commits aren't pushed. `P` shows the commit graph with
unpushed commits highlighted and the files each one changed; `enter` pushes (with `-u` if the branch has no
upstream). Git can't prompt for a password inside lens, so if it needs credentials, push once from a shell.

<img width="1114" height="478" alt="Screenshot 2026-11-20 at 20 21 23" src="https://github.com/user-attachments/assets/bbb62fa1-f21c-4f3c-b60a-b70cb8c4ea66" />

## Live updates and follow mode

lens checks the open file and `git status` once a second, reloading the file when it changes. The sidebar
marks files `M` modified, `A` added, `?` untracked, `D` deleted, and folders holding changes with `•`. Changed
lines are marked in the gutter: green added, yellow modified, red `▔` where lines were removed. Git runs with
`GIT_OPTIONAL_LOCKS=0`, so it never blocks git in another pane.

Press `f` for follow mode (`FOLLOW` badge). When a file is edited, lens opens it with the edited line about
10 lines from the top. If the open file changed, it jumps to the first line that differs from what was on
screen. In diff view, files open in their diff at the edited hunk. While a popup is open, lens holds off and
follows the latest edit once you close it. Follow mode needs git and only sees files that differ from HEAD.

## Recent files

Ctrl+P with an empty query shows two numbered lists:

```
  Recently changed
› 1  model.go  internal/ui/          M  now
  2  finder.go  internal/ui/         M   3m
  Recently viewed
  3  README.md                       M
  4  git.go  internal/files/
```

**Recently changed** shows up to 5 files, newest first: changed files from `git status`, then files from
recent commits. **Recently viewed** shows up to 5 files you've opened (not counting the open one). Press
`1`–`9` or `0` to open one. Once you've typed something, digits go into the search.

## Ghostty keys

lens turns on the kitty keyboard protocol, so Ghostty sends it Cmd shortcuts that Ghostty doesn't use
itself, with no config:

- **Cmd+P** reaches lens as `super+p`. To check, press it in lens, quit, and run
  `grep key "$(lens --log)"`. If it's missing, `keybind = cmd+p=text:\x10` makes Cmd+P send Ctrl+P, but it
  does so in *every* pane (zsh history, Claude Code prompt history, vim completion).
- **Cmd+↑ / Cmd+↓** (half page) and **Cmd+D** (push) are taken by Ghostty. To free them, add the lines below
  and reload with Cmd+Shift+,. This changes every pane. Without it, use `ctrl+d` / `ctrl+u` and `P`.
- **Cmd+F** stays Ghostty's scrollback search. Use Ctrl+F in lens (Cmd+Shift+F also works).

```
keybind = cmd+up=unbind
keybind = cmd+down=unbind
keybind = cmd+d=unbind
```

## Debug log and performance

lens keeps a local timing log at `~/Library/Caches/lens/debug.log` (`lens --log` prints the path); nothing
is sent anywhere. It records file opens, highlighting, indexing, search and `git diff`, and marks anything
slow with `SLOW`: `git status` over 100 ms, frames over 16 ms, fuzzy matching over 30 ms. It logs modifier
key combos but never what you type. The log rotates at 5 MB; set `LENS_DEBUG=0` to turn it off.

```sh
tail -f "$(lens --log)"          # watch it live
grep SLOW "$(lens --log)"        # find slow spots
go test ./... -bench . -run '^$' # benchmarks
```

Safeguards:

- Files over 1,500 lines open as plain text and get colour a moment later. The last 16 highlighted files
  are cached.
- If `git status` takes over 100 ms, polling slows to every 2 s; over 300 ms, to every 5 s.
- Files over 1 MB aren't highlighted. Files over 10 MB aren't opened.

## Theme

Highlighting uses [chroma](https://github.com/alecthomas/chroma) (`monokai` on dark terminals, `github` on
light). Set `LENS_THEME` to any chroma style, e.g. `LENS_THEME=dracula lens`.
