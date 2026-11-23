# Notepad — notes for Claude

A simple text editor for Linux in the style of gedit: Go + Qt 6.12+ via
qute (github.com/nomad-software/qute, the user's own project, checked out
beside this one in ../qute). Termite (../termite), the user's terminal, is
the reference for packaging, toolbar, icons and window themes; ApeMail
(../apemail) is another reference app. Plain `QPlainTextEdit`, no OpenGL.
See README.md for features and settings.

## Build and check
- `make build` (./notepad), `make test` (race), `make check` (vet, gofmt,
  staticcheck in `$GOPATH/bin`, `qute vet` at go.mod's version),
  `make icons` (the app icon's SVG → embedded PNG; the toolbar icons in
  `internal/icons/svg` are embedded as SVG and need no step),
  `make screenshots` (docs/screenshot.png),
  `make appimage`.
- GUI tests (`internal/ui/gui_test.go`) run on Qt's offscreen platform, so
  they need no display. Answer dialogs opened by the call under test with
  `answer(t, button)` / `qutetest.WhenModal`, and wait with
  `qutetest.WaitFor`. Always pass a short `-timeout`.
- `NOTEPAD_SCREENSHOTS=dir go test -run TestScreenshot ./internal/ui` saves
  the README's one screenshot: a note in the dark window theme with
  Solarized Dark at 13 pt, no bars or highlights. Keep it looking like
  plain everyday use.
- `make bench` (`BENCH=regexp`, `COUNT=n`) runs the benchmarks: a 13 MB
  file opened, typed in, saved, searched and replaced in
  (`internal/ui/bench_test.go`, in a window on the offscreen platform), and
  read and written in each encoding (`internal/textfile/bench_test.go`).
  For any performance change save the output before and after
  (`COUNT=10`, to a scratch folder, not the project), compare them with
  `benchstat` (in `$GOPATH/bin`) and report the numbers. A new hot path
  gets a benchmark. A QTextDocument moves every live QTextCursor at each
  edit, and qute frees one only when Go's GC runs: reuse one cursor in
  loops that edit, and in window benchmarks call `collect` between runs or
  the time per run grows with the number of runs.
- Run under Xvfb with `QT_QPA_PLATFORM=xcb`, `XDG_CONFIG_HOME` and
  `XDG_DATA_HOME` in a scratch folder and `NOTEPAD_NO_DESKTOP_ENTRY=1`. Under
  Xvfb on this KDE machine the "system" window theme draws the menu bar and
  toolbar labels dark on dark (Breeze's header colours don't load there);
  use `window_theme` light/dark, or `QT_QPA_PLATFORMTHEME=gtk3`, for
  screenshots.
- `make appimage` works in a temporary folder outside the project and leaves
  only `Notepad-<version>-x86_64.AppImage` here. Never create a `build/`
  folder in the project.

## Layout
- `internal/ui`: `window.go` (Window: one per process, actions, menus,
  toolbar, tabs, opening/saving/closing, file watching), `actions.go` (the
  action, menu and toolbar tables), `doc.go` (Doc: one tab's text and
  file), `editor.go` (textEditor: QPlainTextEdit subclass with the line
  number gutter, extra selections for the current line and search matches,
  Tab/Shift+Tab indenting, file drops), `findbar.go`, `statusbar.go`,
  `settings.go` (dialog: General, User Interface, Colours, Keyboard
  Shortcuts), `themes.go` (Colours page, after Termite's), `about.go`, `recent.go` (Open
  Recent: the list lives in the settings file and is re-read before every
  use, as each window is its own process), `text.go` (pure helpers, tested
  without Qt).
- `internal/textfile`, `internal/config`, `internal/atomicfile`,
  `internal/desktop` have no Qt dependency.

## Rules
- All Qt calls on the main thread; from goroutines use `qute.Post`.
- qute: Owned values (QFont, QTextCursor, QKeySequence…) are freed by GC;
  inside handlers delete QObjects with `DeleteLater`, never `Delete`. Pass
  widgets themselves where a `…Like` is wanted, never an embedded struct
  field. Event arguments are dead once the handler returns.
- Stylesheets go on the central widget (`Window.applyStyle`), never on the
  QMainWindow: there they reach the menu bar, which Breeze then draws with
  the wrong text colour. `applyStyle` runs again on every settings change
  and writes colours out (no `palette(…)`, which is resolved only when the
  sheet is set). The text's colours are stylesheet rules too, never
  `SetPalette`: the stylesheet style restores a widget's first palette when
  the style or palette changes, which left old theme colours behind
  (`TestThemeSwitchRepaintsEverything` checks pixels).
- An untitled, empty document is `pristine` even if typed in: opening a
  file reuses its tab, and closing it asks nothing.
- Save As refuses a file another tab has open. `ChangeEvent` re-arms the
  watcher for each file, as a file deleted and made again is not watched.
- `atomicfile.Save` keeps the file's group and setuid/setgid/sticky bits,
  and writes in place when it can't (EPERM, EBUSY, EXDEV). A lone "\r"
  becomes a line break: QTextDocument can't hold one.
- `desktop.Install` does nothing for a binary in the temporary folder
  (`go run`). `.github/workflows/appimage.yml` builds releases on v* tags.
- Text is held with "\n" line endings; `Doc.text()` is
  `plainText(ToRawText())` (toPlainText would turn non-breaking spaces into
  spaces). Encoding and line endings are applied only when saving.
- Qt positions are UTF-16 code units; convert with `utf16Prefix` /
  `visualColumn` before indexing Go strings.
- A document's own saves are told from others' changes by `fileState`
  (size, mtime, inode) recorded after each read and write; the watcher is
  re-armed after every change while a tab still has the file open, as a
  file replaced by rename stops being watched.
- The find bar's fields are read through `unescape` (`\t`, `\n`, `\\`), and
  text put in them goes through `escape`. QTextDocument's find looks in one
  line at a time, so a search with a line break in it is made by `findAll`
  (text.go) over `Doc.text()` instead, its matches kept (`findBar.spans`)
  until the document's revision or the search changes.
- Idle must stay at ~0% CPU: no polling timers. The find highlights refresh
  on a single-shot timer after edits only while the bar is open.
- Binary size: never call reflect's `Method`/`MethodByName` with a computed
  name, nor link `text/template` or `html/template`; `linksize_test.go`
  checks that no qute `Override` setter is linked (use Go subclasses).
- The About box (`about.go`) follows ApeMail's: icon, name and version
  (from Go's build info, `plainVersion`), then the third-party licence
  notices. A new module in go.mod must be named there too
  (`TestAboutNamesEveryLibrary`).
- Toolbar buttons show no tooltips (`tipFilter`, `TestToolbarHasNoTooltips`).
- The toolbar is in the central widget above the tabs, not the main
  window's toolbar area, as in Termite: Breeze paints main-window toolbars
  in the colour scheme's Header colour, Termite's in the Window colour.
  `toolbarWidget` draws the line along its bottom that styles leave out
  there.
- QPlainTextEdit's layout ignores block line height and margins, and qute
  doesn't bind QPlainTextDocumentLayout, so there is no line height setting
  yet. Padding is the viewport's margins (the gutter paints the strip
  beside it), never the document margin: Qt records a change of that as
  an edit to undo. The document margin is set to 0 once, at creation.
- The user handles git; don't commit or suggest commits.
