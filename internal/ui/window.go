// Package ui builds the editor's Qt user interface: its window, with
// tabs of documents, the menus, toolbar, find bar and status bar, and the
// Settings dialog.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"notepad/internal/config"
	"notepad/internal/desktop"
	"notepad/internal/textfile"
)

// largeFile is the size, in bytes, past which opening a file is asked
// about first: it is read and laid out whole.
const largeFile = 64 << 20

// The least window size that is saved and used again, so that a size saved
// wrongly cannot leave the window unusable.
const minWindowWidth, minWindowHeight = 200, 100

// Window is the editor's window: one per process, as New Window starts
// another process. It has a tab for each document.
type Window struct {
	cfg     *config.Config
	cfgPath string

	w       *qtwidgets.QMainWindow
	central *qtwidgets.QWidget // the tabs above the find bar
	tabs    *qtwidgets.QTabWidget
	toolbar *qtwidgets.QToolBar
	find    *findBar
	status  *statusBar
	acts    map[string]*qtgui.QAction
	recent  *qtwidgets.QMenu // File → Open Recent
	docs    []*Doc

	// watcher tells the window when an open file changes on disk, which
	// is looked at a moment later (check), when a program saving the file
	// has finished writing it.
	watcher *qtcore.QFileSystemWatcher
	check   *qtcore.QTimer
	changed map[string]bool
}

// mainWindow is the window's QMainWindow.
type mainWindow struct {
	qtwidgets.QMainWindowBase
	win *Window
}

// CloseEvent overrides QWidget::closeEvent: documents with changes are
// saved or let go first, or the window stays open.
func (m *mainWindow) CloseEvent(ev *qtgui.QCloseEvent) {
	if !m.win.confirmCloseAll() {
		ev.Ignore()
		return
	}
	m.win.saveGeometry()
	m.Super().CloseEvent(ev)
}

// ChangeEvent overrides QWidget::changeEvent: when the window becomes the
// active one, the open files are looked at for changes made on disk while
// it was not, which the watcher can miss (a file deleted and made again).
func (m *mainWindow) ChangeEvent(ev *qtcore.QEvent) {
	m.Super().ChangeEvent(ev)
	if ev.Type() == qtcore.QEventActivationChange && m.IsActiveWindow() {
		for _, d := range m.win.docs {
			d.diskChanged()
			// A file made again is watched again only here: the watcher let
			// it go when it was deleted.
			m.win.watch(d.path)
		}
	}
}

// NewApplication creates the QApplication.
func NewApplication() {
	qtwidgets.NewQApplication(os.Args)
}

// NewWindow opens the window, with a tab for each of the files, or one
// empty one. loadErr is the problem the settings file had, if any, which
// the status bar shows.
func NewWindow(cfg *config.Config, cfgPath string, loadErr error, files []string) *Window {
	w := &Window{cfg: cfg, cfgPath: cfgPath, acts: map[string]*qtgui.QAction{}, changed: map[string]bool{}}
	applyWindowTheme(cfg.WindowTheme)
	w.w = qtwidgets.NewQMainWindowWith(&mainWindow{win: w}, nil)
	w.w.SetObjectName("notepadWindow")
	w.w.SetWindowIcon(AppIcon())
	width, height := cfg.WindowWidth, cfg.WindowHeight
	if width < minWindowWidth || height < minWindowHeight {
		d := config.Default()
		width, height = d.WindowWidth, d.WindowHeight
	}
	w.w.Resize(width, height)

	w.makeActions()
	w.makeMenus()

	central := qtwidgets.NewQWidget(nil)
	w.central = central
	lay := qtwidgets.NewQVBoxLayout(central)
	lay.SetContentsMargins(qtcore.QMargins{})
	lay.SetSpacing(0)
	w.tabs = newTabWidget(central)
	w.tabs.SetMovable(true)
	w.tabs.SetTabBarAutoHide(true)
	w.tabs.SetFocusPolicy(qtcore.NoFocus)
	w.tabs.TabBar().SetFocusPolicy(qtcore.NoFocus)
	w.tabs.OnCurrentChanged(func(int) { w.currentChanged() })
	// The toolbar is above the tabs, not in the main window's toolbar area,
	// as in Termite: there the desktop's style (Breeze) gives it the
	// header's colour, not the window's.
	w.makeToolbar(central)
	lay.AddWidget(w.toolbar)
	lay.AddWidgetFull(w.tabs, 1, 0)
	w.find = newFindBar(w)
	lay.AddWidget(w.find.box)
	w.w.SetCentralWidget(central)

	w.status = newStatusBar(w)
	w.w.SetStatusBar(w.status.bar)

	w.watcher = qtcore.NewQFileSystemWatcher(w.w)
	w.watcher.OnFileChanged(func(path string) {
		w.changed[path] = true
		w.check.Start()
	})
	w.check = qtcore.NewQTimer(w.w)
	w.check.SetSingleShot(true)
	w.check.SetInterval(200)
	w.check.OnTimeout(w.checkChanged)

	escape := qtgui.NewQShortcutWithKeySequence(keySequence("Esc"), w.w)
	escape.OnActivated(func() {
		if w.find.visible() {
			w.find.close()
		}
	})

	w.applyStyle()
	w.applyShortcuts()
	w.updateViewActions()

	if len(files) > 0 {
		w.openFiles(files)
	}
	if len(w.docs) == 0 {
		w.newTab()
	}
	if cfg.WindowMaximized {
		w.w.ShowMaximized()
	} else {
		w.w.Show()
	}
	w.w.ActivateWindow()
	if loadErr != nil {
		fmt.Fprintln(os.Stderr, "notepad:", loadErr)
		w.message("Settings: " + loadErr.Error())
	}
	return w
}

// applyStyle styles the editor's own widgets, by their object names, so
// that menus and dialogs keep the look the window theme gives them: the
// tabs, find bar and notice in the window theme's colours, the text in the
// editor theme's. The rules are set on the window's middle only: on the
// window they would reach the menu bar, which some styles (Breeze) then
// draw wrongly.
//
// It is run again whenever a theme changes. The colours are written out,
// not left to palette(…), which a stylesheet resolves only when it is set;
// and the text's colours are rules too, not its palette, as Qt's
// stylesheet style puts back a widget's palette as it was when first
// styled, the old theme's, when the style or palette changes.
func (w *Window) applyStyle() {
	pal := qtgui.QGuiApplicationPalette()
	role := func(r qtgui.QPaletteColorRole) string { return pal.Color(r).Name() }
	th := w.cfg.Theme()
	w.central.SetStyleSheet(strings.NewReplacer(
		"$window", role(qtgui.QPaletteWindow),
		"$text", role(qtgui.QPaletteWindowText),
		"$button", role(qtgui.QPaletteButton),
		"$base", role(qtgui.QPaletteBase),
		"$mid", role(qtgui.QPaletteMid),
		"$highlight", role(qtgui.QPaletteHighlight),
		"$hlText", role(qtgui.QPaletteHighlightedText),
		"$edBg", th.Background,
		"$edFg", th.Foreground,
		"$edSel", th.Selection,
		"$edSelText", th.SelectionText,
	).Replace(`
		QTabBar#notepadTabBar { background: $window; }
		QTabBar#notepadTabBar::tab {
			background: $window; color: $text;
			border: none; border-bottom: 2px solid $mid;
			padding: 6px 8px; margin: 0;
		}
		QTabBar#notepadTabBar::tab:hover:!selected { background: $button; }
		QTabBar#notepadTabBar::tab:selected { background: $base; border-bottom-color: $highlight; }
		QFrame#notepadNotice { background: $highlight; }
		QFrame#notepadNotice QLabel { color: $hlText; }
		QLineEdit#notepadFindField[notFound="true"] { color: #dc322f; }
		QWidget#notepadFindBar { border-top: 1px solid $mid; }
		QPlainTextEdit#notepadText {
			background: $edBg; color: $edFg; border: none;
			selection-background-color: $edSel; selection-color: $edSelText;
		}
	`))
}

// ---------------------------------------------------------------- actions

func (w *Window) makeActions() {
	for _, spec := range actionSpecs {
		act := qtgui.NewQActionWithText(spec.label, w.w)
		if spec.short != "" {
			act.SetIconText(spec.short)
		}
		if spec.icon != "" {
			act.SetIcon(icon(spec.icon))
		}
		act.SetCheckable(spec.checkable)
		// Shortcuts work wherever the focus is in the window, menus and
		// toolbar shown or not.
		act.SetShortcutContext(qtcore.WindowShortcut)
		w.w.AddAction(act)
		act.OnTriggered(func(checked bool) { w.run(spec.id, checked) })
		w.acts[spec.id] = act
	}
}

func (w *Window) makeMenus() {
	bar := w.w.MenuBar()
	for _, m := range menuSpecs {
		menu := bar.AddMenuTitle(m.title)
		for _, id := range m.actions {
			switch id {
			case "":
				menu.AddSeparator()
			case recentMenuID:
				w.makeRecentMenu(menu)
			default:
				menu.AddAction(w.acts[id])
			}
		}
	}
}

// toolButtonWidth is the least width of a toolbar button, so that one with
// a short name is not thinner than the rest.
const toolButtonWidth = 80

func (w *Window) makeToolbar(parent qtwidgets.QWidgetLike) {
	tb := qtwidgets.NewQToolBarWithTitleWith(&toolbarWidget{}, "Toolbar", parent)
	tb.SetObjectName("notepadToolbar")
	tb.SetMovable(false)
	tb.SetFloatable(false)
	tb.SetToolButtonStyle(qtcore.ToolButtonTextUnderIcon)
	tb.SetIconSize(qtcore.QSize{Width: 32, Height: 32})
	tb.SetFocusPolicy(qtcore.NoFocus)
	// The tool bar's own menu (hide it) is left out: the View menu has it.
	tb.SetContextMenuPolicy(qtcore.PreventContextMenu)
	noTips := &tipFilter{}
	qtcore.NewQObjectWith(noTips, tb)
	for _, id := range toolbarActions {
		if id == "" {
			tb.AddSeparator()
			continue
		}
		act := w.acts[id]
		tb.AddAction(act)
		// The buttons never take the focus from the text, and have no
		// tooltips: Qt would give each its action's name as one.
		button := tb.WidgetForAction(act)
		button.SetFocusPolicy(qtcore.NoFocus)
		button.SetMinimumWidth(toolButtonWidth)
		button.InstallEventFilter(noTips)
	}
	w.toolbar = tb
	w.updateRecentButton()
}

// toolbarLine is the opacity, out of 255, of the line along the toolbar's
// bottom, in the colour of the text so that it shows in every theme.
const toolbarLine = 60

// toolbarWidget is the window's QToolBar, which draws a line along its
// bottom, as Termite's does: the desktop's style draws none for a toolbar
// outside a main window's toolbar area.
type toolbarWidget struct {
	qtwidgets.QToolBarBase
}

// PaintEvent overrides QWidget::paintEvent.
func (b *toolbarWidget) PaintEvent(ev *qtgui.QPaintEvent) {
	b.Super().PaintEvent(ev)
	qtgui.PaintOn(b, func(p *qtgui.QPainter) {
		c := b.Palette().Color(qtgui.QPaletteWindowText)
		c.A = toolbarLine
		p.FillRect(qtcore.QRect{Y: b.Height() - 1, Width: b.Width(), Height: 1}, c)
	})
}

// updateRecentButton gives the toolbar's Open button the Open Recent menu,
// on an arrow beside it, while recent files are remembered.
func (w *Window) updateRecentButton() {
	button := qtwidgets.QToolButtonCast(w.toolbar.WidgetForAction(w.acts["open"]))
	if w.cfg.RecentFilesMax > 0 {
		button.SetMenu(w.recent)
		button.SetPopupMode(qtwidgets.QToolButtonMenuButtonPopup)
	} else {
		button.SetMenu(nil)
		button.SetPopupMode(qtwidgets.QToolButtonDelayedPopup)
	}
}

// applyShortcuts gives the actions the settings' shortcuts.
func (w *Window) applyShortcuts() {
	for id, act := range w.acts {
		act.SetShortcut(keySequence(w.cfg.Keybindings[id]))
	}
}

// updateViewActions shows the View menu's settings and the toolbar as the
// settings have them.
func (w *Window) updateViewActions() {
	w.acts["toolbar"].SetChecked(w.cfg.Toolbar)
	w.acts["line_numbers"].SetChecked(w.cfg.ShowLineNumbers)
	w.acts["wrap_text"].SetChecked(w.cfg.WrapText)
	w.acts["highlight_line"].SetChecked(w.cfg.HighlightCurrentLine)
	w.toolbar.SetVisible(w.cfg.Toolbar)
}

// updateActions enables the actions that need something to work on: undo
// and redo a change to undo or redo, cut and copy a selection.
func (w *Window) updateActions() {
	d := w.current()
	if d == nil {
		return
	}
	doc := d.edit.Document()
	w.acts["undo"].SetEnabled(doc.IsUndoAvailable())
	w.acts["redo"].SetEnabled(doc.IsRedoAvailable())
	sel := d.edit.TextCursor().HasSelection()
	w.acts["cut"].SetEnabled(sel)
	w.acts["copy"].SetEnabled(sel)
}

// run runs an action on the current document.
func (w *Window) run(id string, checked bool) {
	d := w.current()
	switch id {
	case "new_window":
		w.newWindow()
	case "new_tab":
		w.newTab()
	case "open":
		w.open()
	case "save":
		if d != nil {
			w.save(d)
		}
	case "save_as":
		if d != nil {
			w.saveAs(d)
		}
	case "close_tab":
		if d != nil {
			w.closeDoc(d)
		}
	case "quit":
		w.w.Close()
	case "settings":
		w.showSettings()
	case "about":
		w.about()
	case "toolbar":
		w.setOption("toolbar", checked, func(c *config.Config) { c.Toolbar = checked })
	case "line_numbers":
		w.setOption("show_line_numbers", checked, func(c *config.Config) { c.ShowLineNumbers = checked })
	case "wrap_text":
		w.setOption("wrap_text", checked, func(c *config.Config) { c.WrapText = checked })
	case "highlight_line":
		w.setOption("highlight_current_line", checked, func(c *config.Config) { c.HighlightCurrentLine = checked })
	case "next_tab":
		w.cycleTab(1)
	case "prev_tab":
		w.cycleTab(-1)
	case "find":
		w.find.open(false)
	case "replace":
		w.find.open(true)
	case "find_next", "find_prev":
		if w.find.find.Text() == "" {
			w.find.open(false)
			return
		}
		if !w.find.visible() {
			w.find.show(false)
			w.find.highlight()
		}
		w.find.next(id == "find_next")
	}
	if d == nil {
		return
	}
	switch id {
	case "undo":
		d.edit.Undo()
	case "redo":
		d.edit.Redo()
	case "cut":
		d.edit.Cut()
	case "copy":
		d.edit.Copy()
	case "paste":
		d.edit.Paste()
	case "select_all":
		d.edit.SelectAll()
	case "go_to_line":
		w.goToLine(d)
	}
}

// setOption changes one setting from the View menu, saving it to the
// settings file and applying it.
func (w *Window) setOption(key string, value bool, set func(*config.Config)) {
	if err := config.SetValues(w.cfgPath, map[string]any{key: value}); err != nil {
		w.warn("The setting could not be saved:\n" + err.Error())
	}
	c := *w.cfg
	set(&c)
	w.applyConfig(&c)
}

// applyConfig applies new settings to the window and every document.
func (w *Window) applyConfig(c *config.Config) {
	old := w.cfg
	w.cfg = c
	if old.WindowTheme != c.WindowTheme {
		applyWindowTheme(c.WindowTheme)
	}
	w.applyStyle()
	for _, d := range w.docs {
		d.editor.apply(c)
	}
	w.applyShortcuts()
	w.updateViewActions()
	w.fillRecentMenu()
	w.updateRecentButton()
	if d := w.current(); d != nil {
		w.status.update(d)
	}
}

// ---------------------------------------------------------------- documents

// current returns the document of the current tab, or nil.
func (w *Window) current() *Doc {
	page := w.tabs.CurrentWidget()
	if page == nil {
		return nil
	}
	for _, d := range w.docs {
		if d.page.Ptr() == page.Ptr() {
			return d
		}
	}
	return nil
}

// addDoc adds a tab for a new document and makes it the current one.
// untitled is its number while it has no file.
func (w *Window) addDoc(untitled int) *Doc {
	d := newDoc(w)
	d.untitled = untitled
	w.docs = append(w.docs, d)
	i := w.tabs.AddTab(d.page, "")
	w.tabs.TabBar().SetTabButton(i, qtwidgets.QTabBarRightSide, tabCloseButton(func() { w.closeDoc(d) }))
	w.docChanged(d)
	w.tabs.SetCurrentIndex(i)
	d.edit.SetFocus()
	return d
}

// newTab opens a tab with a new, untitled document, numbered with the
// least number no other untitled document has.
func (w *Window) newTab() *Doc {
	used := map[int]bool{}
	for _, d := range w.docs {
		if d.path == "" {
			used[d.untitled] = true
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return w.addDoc(n)
}

// cycleTab makes the tab d places along (round past the ends) the current
// one.
func (w *Window) cycleTab(d int) {
	if n := w.tabs.Count(); n > 1 {
		w.tabs.SetCurrentIndex((w.tabs.CurrentIndex() + d + n) % n)
	}
}

// currentChanged is told that another tab is the current one.
func (w *Window) currentChanged() {
	d := w.current()
	if d == nil {
		return
	}
	w.updateTitle()
	w.status.update(d)
	w.updateActions()
	w.find.currentChanged()
	d.edit.SetFocus()
}

// docChanged shows a document's name, and whether it has changes, in its
// tab, and with the current one in the window's title and status bar.
func (w *Window) docChanged(d *Doc) {
	i := w.tabs.IndexOf(d.page)
	if i < 0 {
		return
	}
	w.tabs.SetTabText(i, d.title())
	tip := d.name()
	if d.path != "" {
		tip = displayPath(d.path)
	}
	w.tabs.SetTabToolTip(i, tip)
	if d == w.current() {
		w.updateTitle()
		w.status.update(d)
	}
}

// cursorMoved is told that a document's cursor or selection has moved.
func (w *Window) cursorMoved(d *Doc) {
	if d == w.current() {
		w.status.updatePosition(d)
		w.find.showCount(d)
	}
}

func (w *Window) updateTitle() {
	d := w.current()
	if d == nil {
		return
	}
	w.w.SetWindowTitle(d.title() + " - " + desktop.DisplayName)
}

// message shows a message in the status bar for a while.
func (w *Window) message(text string) { w.status.message(text) }

// warn shows a problem in a dialog.
func (w *Window) warn(text string) {
	qtwidgets.QMessageBoxWarning(w.w, desktop.DisplayName, text)
}

// ask asks a yes or no question.
func (w *Window) ask(text string) bool {
	return qtwidgets.QMessageBoxQuestionFull(w.w, desktop.DisplayName, text,
		qtwidgets.QMessageBoxYes|qtwidgets.QMessageBoxNo, qtwidgets.QMessageBoxNo) == qtwidgets.QMessageBoxYes
}

// ---------------------------------------------------------------- files

// openFiles opens each file in a tab of its own, or goes to its tab if it
// is open already. The first goes in place of a new document nothing has
// been done to. A file that is not there is opened empty, and made when it
// is saved.
func (w *Window) openFiles(paths []string) {
	for _, path := range paths {
		w.openFile(absPath(path))
	}
}

func (w *Window) openFile(path string) {
	for _, d := range w.docs {
		if d.path == path {
			w.tabs.SetCurrentWidget(d.page)
			return
		}
	}
	var f textfile.File
	var disk fileState // taken before the file is read, as reload does
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		w.message(displayPath(path) + " is a new file")
	case err != nil:
		w.warn(err.Error())
		return
	case info.IsDir():
		w.warn(displayPath(path) + " is a folder.")
		return
	case info.Size() > largeFile:
		if !w.ask(fmt.Sprintf("%s is %d MB. Large files are slow to open and edit. Open it anyway?",
			filepath.Base(path), info.Size()>>20)) {
			return
		}
	}
	if err == nil {
		disk = stateOf(info)
		data, err := os.ReadFile(path)
		if err != nil {
			w.warn(err.Error())
			return
		}
		f = textfile.Parse(data)
		if textfile.LooksBinary(data, f.Encoding) && !w.ask(filepath.Base(path)+" does not look like a text file. Open it anyway?") {
			return
		}
	}
	d := w.current()
	if d == nil || !d.pristine() {
		d = w.addDoc(0)
	}
	d.setPath(path, f, disk)
	w.watch(path)
	if err == nil { // a file that is there, not a new one
		w.addRecent(path)
	}
	w.docChanged(d)
	w.updateActions()
	w.find.currentChanged()
}

// open asks for files to open, in the folder of the current document.
func (w *Window) open() {
	paths, _ := qtwidgets.QFileDialogGetOpenFileNamesFull(w.w, "Open", w.dialogDir(), "", "", 0)
	w.openFiles(paths)
}

// dialogDir is the folder file dialogs start in: the current document's.
func (w *Window) dialogDir() string {
	if d := w.current(); d != nil && d.path != "" {
		return filepath.Dir(d.path)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// save saves a document to its file, or asks for one. It reports whether
// the document was saved.
func (w *Window) save(d *Doc) bool {
	if d.path == "" {
		return w.saveAs(d)
	}
	return w.saveTo(d, d.path)
}

// saveAs asks for a file to save a document to, and saves it there. It
// reports whether the document was saved.
func (w *Window) saveAs(d *Doc) bool {
	start := w.dialogDir()
	if d.path != "" {
		start = d.path
	} else if start != "" {
		start = filepath.Join(start, d.name()+".txt")
	}
	path, _ := qtwidgets.QFileDialogGetSaveFileNameFull(w.w, "Save As", start, "", "", 0)
	if path == "" {
		return false
	}
	return w.saveTo(d, absPath(path))
}

func (w *Window) saveTo(d *Doc, path string) bool {
	for _, other := range w.docs {
		if other != d && other.path == path {
			w.warn(fmt.Sprintf("%s is open in another tab. Close that tab first to save over it.", filepath.Base(path)))
			return false
		}
	}
	old, existed := d.path, d.disk.exists
	if err := d.saveTo(path); err != nil {
		w.warn(fmt.Sprintf("%s could not be saved:\n%v", filepath.Base(path), err))
		return false
	}
	if old != path {
		w.unwatch(old)
	}
	if old != path || !existed { // a new file, whether opened as one or saved as
		w.addRecent(path)
	}
	w.watch(path)
	w.docChanged(d)
	w.message("Saved " + displayPath(path))
	return true
}

// askSave asks whether to save a document's changes before it closes. It
// reports whether to go on closing it: it was saved, or its changes are to
// be lost.
func (w *Window) askSave(d *Doc) bool {
	if !d.modified() || d.pristine() {
		return true
	}
	w.tabs.SetCurrentWidget(d.page)
	answer := qtwidgets.QMessageBoxWarningFull(w.w, desktop.DisplayName,
		fmt.Sprintf("Save the changes to %s before closing?", d.name()),
		qtwidgets.QMessageBoxSave|qtwidgets.QMessageBoxDiscard|qtwidgets.QMessageBoxCancel, qtwidgets.QMessageBoxSave)
	switch answer {
	case qtwidgets.QMessageBoxSave:
		return w.save(d)
	case qtwidgets.QMessageBoxDiscard:
		return true
	}
	return false
}

// confirmCloseAll asks about each document with changes. It reports
// whether the window can close.
func (w *Window) confirmCloseAll() bool {
	for _, d := range slices.Clone(w.docs) {
		if !w.askSave(d) {
			return false
		}
	}
	return true
}

// closeDoc closes a document's tab, asking first if it has changes. The
// last one closed is replaced by a new document, so the window is never
// empty.
func (w *Window) closeDoc(d *Doc) {
	if !w.askSave(d) {
		return
	}
	if i := w.tabs.IndexOf(d.page); i >= 0 {
		w.tabs.RemoveTab(i)
	}
	w.find.forget(d)
	w.docs = slices.DeleteFunc(w.docs, func(x *Doc) bool { return x == d })
	w.unwatch(d.path)
	d.page.DeleteLater()
	if len(w.docs) == 0 {
		w.newTab()
	}
	if c := w.current(); c != nil {
		c.edit.SetFocus()
	}
}

// watch watches a file for changes made on disk.
func (w *Window) watch(path string) {
	if path != "" && !slices.Contains(w.watcher.Files(), path) && statFile(path).exists {
		w.watcher.AddPath(path)
	}
}

// unwatch stops watching a file no document has open.
func (w *Window) unwatch(path string) {
	if path == "" {
		return
	}
	for _, d := range w.docs {
		if d.path == path {
			return
		}
	}
	w.watcher.RemovePath(path)
}

// checkChanged looks at the files the watcher said had changed. A file
// replaced by another (as many programs save) is no longer watched, so it
// is watched again while a document still has it open.
func (w *Window) checkChanged() {
	paths := w.changed
	w.changed = map[string]bool{}
	for path := range paths {
		w.watcher.RemovePath(path)
		for _, d := range w.docs {
			if d.path == path {
				d.diskChanged()
				w.watch(path)
			}
		}
	}
}

// setEncoding changes the encoding a document is saved in.
func (w *Window) setEncoding(d *Doc, enc textfile.Encoding) {
	if d.encoding == enc {
		return
	}
	d.encoding = enc
	d.edit.Document().SetModifiedFull(true)
	w.docChanged(d)
	w.message("The file will be saved as " + enc.Name())
}

// setLineEnding changes the line endings a document is saved with.
func (w *Window) setLineEnding(d *Doc, le textfile.LineEnding) {
	if d.lineEnding == le {
		return
	}
	d.lineEnding = le
	d.edit.Document().SetModifiedFull(true)
	w.docChanged(d)
	w.message("The file will be saved with " + le.Name() + " line endings")
}

// reopen reads a document's file again in another encoding.
func (w *Window) reopen(d *Doc, enc textfile.Encoding) {
	if d.path == "" {
		return
	}
	if d.modified() && !w.ask(fmt.Sprintf("Reopening %s loses the changes made to it. Reopen it anyway?", d.name())) {
		return
	}
	if err := d.reload(enc); err != nil {
		w.warn(err.Error())
		return
	}
	w.message(fmt.Sprintf("Reopened %s as %s", d.name(), enc.Name()))
}

// ---------------------------------------------------------------- other actions

// startProcess starts a program, which is left running on its own. Tests
// replace it.
var startProcess = func(path string) error {
	cmd := exec.Command(path)
	// Its own session, so it is not closed with a terminal this one was
	// started from.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reaps it when it ends
	return nil
}

// newWindow starts another editor, in a process of its own.
func (w *Window) newWindow() {
	exe, err := desktop.Executable()
	if err == nil {
		err = startProcess(exe)
	}
	if err != nil {
		w.warn("A new window could not be opened:\n" + err.Error())
	}
}

// goToLine asks for a line number and puts the cursor on that line.
func (w *Window) goToLine(d *Doc) {
	n := d.edit.BlockCount()
	cur := d.edit.TextCursor().BlockNumber() + 1
	line, ok := qtwidgets.QInputDialogGetIntFull(w.w, "Go to Line", fmt.Sprintf("Line (1 to %d):", n), cur, 1, n, 1, 0)
	if ok {
		d.goTo(line-1, 0)
		d.edit.CenterCursor()
	}
	d.edit.SetFocus()
}

func (w *Window) about() { showAbout(w.w) }

// saveGeometry stores the window's size in the settings file, for the next
// window to open the same size. A maximized window keeps the size it had
// before it was maximized.
func (w *Window) saveGeometry() {
	maximized := w.w.IsMaximized()
	width, height := w.w.Width(), w.w.Height()
	if maximized {
		g := w.w.NormalGeometry()
		width, height = g.Width, g.Height
	}
	if width < minWindowWidth || height < minWindowHeight {
		return
	}
	c := w.cfg
	if c.WindowWidth == width && c.WindowHeight == height && c.WindowMaximized == maximized {
		return
	}
	err := config.SetValues(w.cfgPath, map[string]any{
		"window_width": width, "window_height": height, "window_maximized": maximized,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "notepad: the window size could not be saved:", err)
		return
	}
	c.WindowWidth, c.WindowHeight, c.WindowMaximized = width, height, maximized
}
