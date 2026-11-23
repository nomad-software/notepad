# <img src="internal/appicon/appicon.png" alt="" width="40" height="40"> Notepad

A simple, quick text editor for Linux, written in Go
with Qt 6 (via [qute](https://github.com/nomad-software/qute)). It is for
plain text and quick edits: tabs, line numbers, find and replace, and
nothing that gets in the way. There is no syntax highlighting.

![Notepad](docs/screenshot.png)

The screenshot is drawn by `make screenshots`.

## Features

- **Tabs**, each with its own file. New Window starts another process, so
  one window that hangs never takes the others with it.
- **Line numbers** and **highlighting of the current line**, both optional,
  and optional **wrapping** of long lines.
- **Find and replace** in a bar below the text, with Match case and Whole words.
  Matches are highlighted while the bar is open (the first 2000, so that
  a search in a large file stays quick), and the bar counts them. Both
  fields take `\t` for a tab, `\n` for a line break and `\\` for a
  backslash.
- **Go to line** (Ctrl+L).
- **Tab and Shift+Tab** indent and unindent the selected lines, with tabs
  or spaces of the chosen width.
- **Open Recent** in the File menu, and files can be dropped on the window
  to open them.
- **Encodings**: UTF-8 (with or without a byte order mark), UTF-16 LE/BE,
  UTF-32 LE/BE and ISO-8859-1. The encoding is detected when a file is
  read and can be changed from the status bar: click it to save in another
  one, or to reopen the file in another.
- **Line endings**: Unix (LF), Windows (CRLF) and classic Mac (CR) are
  detected and kept, and can be changed from the status bar.
- **Files changed on disk**: a file changed by another program is read
  again at once if it has no unsaved changes; otherwise a bar above the
  text asks whether to reload it or keep your version. A deleted file is
  reported too. Saving is atomic (write to a temporary file, then rename)
  and follows symbolic links.
- **Themes**: the window can follow the desktop or be light or dark, and
  the text has colour themes of its own (Freya, Solarized Light, Solarized
  Dark), which can be edited and added to in Settings.
- **Keyboard shortcuts** for every action, changeable in Settings.
- The status bar shows the line and column, the size of the selection,
  the encoding and the line endings.
- Uses no CPU when idle: there are no polling timers.

## Build and run

The easiest way to get Notepad is the AppImage from a release (see
[AppImage](#appimage)). To build it yourself you need Go 1.27+, Qt 6.12 or
newer development files (Widgets, Svg), `pkg-config` and a C++17 compiler.

```sh
make build                  # or: go build -o notepad .
./notepad                   # an empty tab
./notepad a.txt b.txt       # a tab for each file
./notepad --config FILE     # another settings file
./notepad --version
make appimage               # Notepad-<version>-x86_64.AppImage
```

The first build compiles qute's C++ shims and takes a few minutes; later
builds are cached.

On every start, Notepad writes a desktop entry
(`~/.local/share/applications/notepad.desktop`) and its icon
(`~/.local/share/icons/hicolor/256x256/apps/notepad.png`). They point at
the copy that is running, so it appears in the desktop's menus and can be
pinned to a dock. Set `NOTEPAD_NO_DESKTOP_ENTRY=1` to skip this.
`packaging/notepad.desktop` is a static entry for packagers. The icon is
drawn from `packaging/notepad.svg` (`make icons`) and embedded in the
binary.

`--cpuprofile FILE` writes a CPU profile.

## Settings

Settings are kept in `~/.config/notepad/config.json` (or
`$XDG_CONFIG_HOME/notepad/config.json`) and are changed in the Settings
dialog (Ctrl+,), which saves them on OK or Apply. The file can also be edited by
hand; a missing setting takes its default, and a wrong one is reported and
the rest still apply.

| Setting | Default | Meaning |
| --- | --- | --- |
| `show_line_numbers` | `true` | The line number gutter |
| `wrap_text` | `false` | Wrap long lines at the window's edge |
| `highlight_current_line` | `true` | Highlight the line with the cursor |
| `tab_width` | `4` | Width of a tab, in spaces (1–16) |
| `insert_spaces` | `false` | Tab inserts spaces rather than a tab |
| `font_family`, `font_size` | `Iosevka Term Curly`, `16` | The text's font (size 5–72) |
| `padding` | `2` | Pixels between the text and its edges (0–50) |
| `window_theme` | `system` | `system`, `light` or `dark` |
| `editor_theme` | `freya` | The id of one of `themes` |
| `themes` | the built-in ones | Text colour themes by id; when present it replaces the built-in set |
| `toolbar` | `true` | Show the toolbar |
| `keybindings` | see below | Action id to shortcut, in Qt's text form (`Ctrl+Shift+S`); `""` for none |
| `recent_files_max` | `10` | How many files Open Recent remembers (0–50) |

The window's and the Settings dialog's sizes, and the recent files, are
kept in the same file.

### Default keyboard shortcuts

| Action | Shortcut | Action | Shortcut |
| --- | --- | --- | --- |
| New Window | Ctrl+Shift+N | Find | Ctrl+F |
| New Tab | Ctrl+T | Find and Replace | Ctrl+Shift+F |
| Open | Ctrl+O | Find Next | F3 |
| Save | Ctrl+S | Find Previous | Shift+F3 |
| Save As | Ctrl+Shift+S | Go to Line | Ctrl+L |
| Close Tab | Ctrl+W | Next Tab | Ctrl+PgDown |
| Quit | Ctrl+Q | Previous Tab | Ctrl+PgUp |
| Undo | Ctrl+Z | Settings | Ctrl+, |
| Redo | Ctrl+Shift+Z | About | Ctrl+I |
| Cut, Copy, Paste | Ctrl+X, C, V | Select All | Ctrl+A |

Toolbar, Line Numbers, Wrap Text and Highlight Current Line in the View
menu have no shortcut by default.

## AppImage

Releases for Linux are an AppImage, `Notepad-*-x86_64.AppImage`: one file
with Qt inside it, which runs without installing anything. Make it
executable and run it. On its first start it adds itself to the desktop's
menus (see above).

It is built by a GitHub workflow (`.github/workflows/appimage.yml`), which
runs the tests and then `packaging/appimage.sh` on Ubuntu 22.04 with Qt's
own build of Qt 6.12:

- Pushing a tag starting with `v` builds it and attaches it to a release of
  that name: `git tag v1.0.0 && git push origin v1.0.0`.
- It can also be run by hand from the Actions tab (AppImage > Run
  workflow), which leaves the AppImage as an artifact of the run.

It runs under X11 and Wayland, and on GTK desktops takes its light or dark
look from the desktop through Qt's GTK theme plugin. It runs on any
distribution with glibc 2.35 or later. Before it is made, the script checks
that the X11, Wayland and theme plugins are in it and that no OpenGL
library is; afterwards it starts it in a virtual X server, so a build that
can't open a window is never released.

`make appimage` runs the same script locally. It works in a temporary
folder outside the project and leaves only the AppImage in the project
folder. The tools it downloads (linuxdeploy and its Qt plugin) are kept in
`~/.cache/notepad-appimage`; delete that folder to fetch their latest
build. An AppImage built on a newer system only runs on systems at least
as new.

## Testing

```sh
make test     # all tests, with the race detector
make check    # go vet, gofmt, staticcheck and qute vet
make bench    # benchmarks: a 13 MB file opened, typed in, saved and searched
```

The GUI tests and benchmarks run on Qt's offscreen platform, so they need
no display. To see what a change does to the editor's speed, run the
benchmarks before and after it and compare them with
[benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```sh
make bench COUNT=10 > old.txt
# ...make the change...
make bench COUNT=10 > new.txt
benchstat old.txt new.txt
```

`make bench BENCH=Find` runs only the benchmarks whose names match.

## Licence

Notepad is licensed under the GNU General Public License v3 (see
`LICENSE`). The About box lists the licences of the libraries it is built
with.
