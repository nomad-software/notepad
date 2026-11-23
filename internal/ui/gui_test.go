package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"notepad/internal/config"
	"notepad/internal/textfile"
)

func TestMain(m *testing.M) { qutetest.TestMain(m) }

// newTestWindow opens a window with a settings file of its own, which is
// closed when the test ends, its changes let go.
func newTestWindow(t testing.TB, set func(*config.Config), files ...string) *Window {
	t.Helper()
	t.Setenv("NOTEPAD_NO_DESKTOP_ENTRY", "1")
	cfg := config.Default()
	cfg.FontFamily = "DejaVu Sans Mono"
	if set != nil {
		set(cfg)
	}
	w := NewWindow(cfg, filepath.Join(t.TempDir(), "config.json"), nil, files)
	t.Cleanup(func() {
		qute.PostSync(func() {
			for _, d := range w.docs {
				d.edit.Document().SetModifiedFull(false)
			}
			if w.w.Alive() {
				w.w.Close()
				w.w.DeleteLater()
			}
		})
	})
	return w
}

// answer answers the next message box with a button.
func answer(t *testing.T, button qtwidgets.QMessageBoxStandardButtons) {
	qutetest.WhenModal(t, func(m *qtwidgets.QWidget) {
		box := qtwidgets.QMessageBoxCast(m)
		if box == nil {
			t.Errorf("a %s is open, not a message box", m.ObjectName())
			return
		}
		qutetest.Click(box.Button(button))
	})
}

// findChild returns the object under o with the name.
func findChild(o qtcore.QObjectLike, name string) *qtcore.QObject {
	for _, c := range o.AsQObject().Children() {
		if c.ObjectName() == name {
			return c
		}
		if found := findChild(c, name); found != nil {
			return found
		}
	}
	return nil
}

func writeFile(t testing.TB, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenEditSaveKeepsEncodingAndLineEndings(t *testing.T) {
	path := writeFile(t, "latin.txt", []byte("caf\xe9\r\nsecond\r\n"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, path)
		d := w.current()
		if d.text() != "café\nsecond\n" {
			t.Fatalf("text = %q", d.text())
		}
		if w.status.enc.Text() != "ISO-8859-1" || w.status.eol.Text() != "Windows" {
			t.Errorf("status shows %q, %q", w.status.enc.Text(), w.status.eol.Text())
		}
		if !strings.HasSuffix(w.w.WindowTitle(), "latin.txt - Notepad") {
			t.Errorf("title = %q", w.w.WindowTitle())
		}
		qutetest.Key(d.edit, qtcore.Key_End, qtcore.NoModifier)
		qutetest.Type(d.edit, "!")
		if !d.modified() || w.tabs.TabText(0) != "*latin.txt" || !strings.HasPrefix(w.w.WindowTitle(), "*") {
			t.Errorf("modified %v, tab %q, title %q", d.modified(), w.tabs.TabText(0), w.w.WindowTitle())
		}
		w.run("save", false)
		got, _ := os.ReadFile(path)
		if want := []byte("caf\xe9!\r\nsecond\r\n"); !bytes.Equal(got, want) {
			t.Errorf("saved %q, want %q", got, want)
		}
		if d.modified() || w.tabs.TabText(0) != "latin.txt" {
			t.Errorf("after saving: modified %v, tab %q", d.modified(), w.tabs.TabText(0))
		}
	})
}

func TestChangeEncodingAndLineEndings(t *testing.T) {
	path := writeFile(t, "a.txt", []byte("one\ntwo\n"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, path)
		d := w.current()
		w.setEncoding(d, textfile.UTF16LE)
		w.setLineEnding(d, textfile.CRLF)
		if !d.modified() || w.status.enc.Text() != "UTF-16 LE" || w.status.eol.Text() != "Windows" {
			t.Errorf("modified %v, status %q %q", d.modified(), w.status.enc.Text(), w.status.eol.Text())
		}
		w.run("save", false)
		got, _ := os.ReadFile(path)
		want, _ := textfile.Encode("one\r\ntwo\r\n", textfile.UTF16LE)
		if !bytes.Equal(got, want) {
			t.Errorf("saved %q, want %q", got, want)
		}

		// Reopening as another encoding reads the bytes again.
		w.reopen(d, textfile.Latin1)
		if d.encoding != textfile.Latin1 || !strings.HasPrefix(d.text(), "ÿþo\x00") {
			t.Errorf("reopened as %s: %q", d.encoding, d.text())
		}
	})
}

func TestUnrepresentableTextIsNotSaved(t *testing.T) {
	path := writeFile(t, "a.txt", []byte("caf\xe9"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, path)
		d := w.current()
		qutetest.Type(d.edit, "λ")
		answer(t, qtwidgets.QMessageBoxOk)
		if w.save(d) {
			t.Error("text Latin-1 cannot hold was saved")
		}
		if got, _ := os.ReadFile(path); string(got) != "caf\xe9" {
			t.Errorf("the file was changed: %q", got)
		}
	})
}

func TestTabsAndUntitledDocuments(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		if len(w.docs) != 1 || w.current().name() != "Untitled 1" {
			t.Fatalf("docs %d, name %q", len(w.docs), w.current().name())
		}
		w.run("new_tab", false)
		w.run("new_tab", false)
		if w.current().name() != "Untitled 3" || w.tabs.Count() != 3 {
			t.Errorf("name %q, tabs %d", w.current().name(), w.tabs.Count())
		}
		// Closing one frees its number.
		w.closeDoc(w.docs[1])
		w.run("new_tab", false)
		if w.current().name() != "Untitled 2" {
			t.Errorf("name %q", w.current().name())
		}
		w.run("next_tab", false)
		if w.tabs.CurrentIndex() != 0 {
			t.Errorf("next tab from the last is %d", w.tabs.CurrentIndex())
		}
		// Closing the last leaves a new one.
		for len(w.docs) > 1 {
			w.closeDoc(w.docs[0])
		}
		w.closeDoc(w.docs[0])
		if len(w.docs) != 1 || w.tabs.Count() != 1 {
			t.Errorf("after closing them all: %d docs, %d tabs", len(w.docs), w.tabs.Count())
		}
	})
}

func TestClosingModifiedAsks(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		w.run("new_tab", false)
		d := w.current()
		qutetest.Type(d.edit, "keep me")

		answer(t, qtwidgets.QMessageBoxCancel)
		w.closeDoc(d)
		if len(w.docs) != 2 {
			t.Fatal("Cancel closed the document")
		}
		answer(t, qtwidgets.QMessageBoxDiscard)
		w.closeDoc(d)
		if len(w.docs) != 1 {
			t.Fatal("Discard did not close the document")
		}

		// The window does not close while a document's changes are
		// unanswered.
		qutetest.Type(w.current().edit, "more")
		answer(t, qtwidgets.QMessageBoxCancel)
		w.w.Close()
		if !w.w.IsVisible() {
			t.Error("the window closed although Cancel was chosen")
		}
	})
}

func TestOpenFiles(t *testing.T) {
	a := writeFile(t, "a.txt", []byte("a"))
	b := writeFile(t, "b.txt", []byte("b"))
	missing := filepath.Join(t.TempDir(), "new.txt")
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		w.openFiles([]string{a})
		if len(w.docs) != 1 || w.current().path != a {
			t.Fatalf("the new document was not replaced: %d docs", len(w.docs))
		}
		// An untitled tab that had text typed and deleted is empty: it is
		// used too, rather than a new tab opened beside it.
		w.run("new_tab", false)
		qutetest.Type(w.current().edit, "x")
		qutetest.Key(w.current().edit, qtcore.Key_Backspace, qtcore.NoModifier)
		w.openFiles([]string{b})
		if len(w.docs) != 2 || w.current().path != b {
			t.Fatalf("an emptied tab was not used: %d docs, current %q", len(w.docs), w.current().path)
		}
		w.openFiles([]string{b, a})
		if len(w.docs) != 2 || w.current().path != a {
			t.Errorf("opening an open file: %d docs, current %q", len(w.docs), w.current().path)
		}
		w.openFiles([]string{missing})
		d := w.current()
		if d.path != missing || d.text() != "" {
			t.Fatalf("a missing file: path %q", d.path)
		}
		qutetest.Type(d.edit, "made")
		w.run("save", false)
		if got, _ := os.ReadFile(missing); string(got) != "made" {
			t.Errorf("saving a new file wrote %q", got)
		}
		// A folder is not opened.
		answer(t, qtwidgets.QMessageBoxOk)
		w.openFiles([]string{filepath.Dir(a)})
		if len(w.docs) != 3 {
			t.Errorf("%d docs after opening a folder", len(w.docs))
		}
	})
}

func TestBinaryFileAsks(t *testing.T) {
	path := writeFile(t, "a.bin", []byte("ELF\x00\x01\x02"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		answer(t, qtwidgets.QMessageBoxNo)
		w.openFiles([]string{path})
		if w.current().path != "" {
			t.Error("a binary file was opened when No was answered")
		}
	})
}

func TestTabKeyIndents(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, func(c *config.Config) { c.InsertSpaces = true; c.TabWidth = 4 })
		d := w.current()
		qutetest.Type(d.edit, "ab")
		qutetest.Key(d.edit, qtcore.Key_Tab, qtcore.NoModifier)
		if d.text() != "ab  " {
			t.Errorf("Tab after two characters: %q", d.text())
		}
		d.edit.SetPlainText("one\ntwo\n\nthree")
		d.edit.SelectAll()
		qutetest.Key(d.edit, qtcore.Key_Tab, qtcore.NoModifier)
		if d.text() != "    one\n    two\n\n    three" {
			t.Errorf("indented: %q", d.text())
		}
		qutetest.Key(d.edit, qtcore.Key_Backtab, qtcore.ShiftModifier)
		if d.text() != "one\ntwo\n\nthree" {
			t.Errorf("unindented: %q", d.text())
		}
		// Indenting is one change to undo.
		qutetest.Key(d.edit, qtcore.Key_Tab, qtcore.NoModifier)
		d.edit.Undo()
		if d.text() != "one\ntwo\n\nthree" {
			t.Errorf("after undo: %q", d.text())
		}

		w.applyConfig(func() *config.Config { c := *w.cfg; c.InsertSpaces = false; return &c }())
		d.edit.SetPlainText("")
		qutetest.Key(d.edit, qtcore.Key_Tab, qtcore.NoModifier)
		if d.text() != "\t" {
			t.Errorf("Tab without spaces: %q", d.text())
		}
	})
}

func TestStatusPosition(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, func(c *config.Config) { c.TabWidth = 8 })
		d := w.current()
		d.edit.SetPlainText("first\n\tsecond")
		d.goTo(1, 1)
		if got := w.status.pos.Text(); got != "Ln 2, Col 9" {
			t.Errorf("after a tab: %q", got)
		}
		c := d.edit.TextCursor()
		c.SetPosition(2)
		c.SetPositionFull(10, qtgui.QTextCursorKeepAnchor)
		d.edit.SetTextCursor(c)
		if got := w.status.pos.Text(); got != "Ln 2, Col 12 (2 lines, 8 chars selected)" {
			t.Errorf("with a selection: %q", got)
		}
	})
}

func TestFindAndReplace(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		d.edit.SetPlainText("cat dog cat\nCat catalog")
		d.goTo(0, 0)
		w.run("find", false)
		if !w.find.visible() {
			t.Fatal("the find bar did not open")
		}
		qutetest.Type(w.find.find, "cat")
		if got := d.edit.TextCursor().SelectedText(); got != "cat" || d.edit.TextCursor().SelectionStart() != 0 {
			t.Errorf("typing found %q at %d", got, d.edit.TextCursor().SelectionStart())
		}
		if got := w.find.count.Text(); got != "1 of 4" {
			t.Errorf("count %q", got)
		}
		qutetest.Key(w.find.find, qtcore.Key_Return, qtcore.NoModifier)
		if d.edit.TextCursor().SelectionStart() != 8 {
			t.Errorf("Enter went to %d", d.edit.TextCursor().SelectionStart())
		}
		w.find.matchCase.SetChecked(true)
		w.find.wholeWords.SetChecked(true)
		if got := w.find.count.Text(); !strings.HasSuffix(got, "2") {
			t.Errorf("whole words with case: count %q", got)
		}
		w.find.wholeWords.SetChecked(false)
		w.find.matchCase.SetChecked(false)

		w.run("replace", false)
		if !w.find.replaceRow.IsVisible() {
			t.Fatal("the replace row is hidden")
		}
		w.find.replace.SetText("cats cat")
		w.find.replaceAll()
		if got := d.text(); got != "cats cat dog cats cat\ncats cat cats catalog" {
			t.Errorf("replaced all: %q", got)
		}
		d.edit.Undo()
		if got := d.text(); got != "cat dog cat\nCat catalog" {
			t.Errorf("Replace All took more than one undo: %q", got)
		}

		qutetest.Key(w.find.find, qtcore.Key_Escape, qtcore.NoModifier)
		if w.find.visible() || len(d.editor.matches) != 0 {
			t.Error("Escape did not close the bar and clear the highlights")
		}
	})
}

func TestFindAndReplaceEscapes(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		d.edit.SetPlainText("a\tb\na\\tb\nc\n\nd")
		d.goTo(0, 0)
		w.run("replace", false)

		// A tab, and a backslash followed by a t.
		qutetest.Type(w.find.find, `a\tb`)
		if c := d.edit.TextCursor(); c.SelectionStart() != 0 || c.SelectionEnd() != 3 {
			t.Errorf(`\t found %d to %d`, c.SelectionStart(), c.SelectionEnd())
		}
		if got := w.find.count.Text(); got != "1 of 1" {
			t.Errorf(`\t: count %q`, got)
		}
		w.find.find.SetText(`a\\tb`)
		w.find.next(true)
		if c := d.edit.TextCursor(); c.SelectionStart() != 4 || c.SelectionEnd() != 8 {
			t.Errorf(`\\t found %d to %d`, c.SelectionStart(), c.SelectionEnd())
		}

		// A line break: found, counted, gone round, and replaced one at a time.
		w.find.find.SetText(`b\na`)
		d.goTo(0, 0)
		w.find.next(true)
		w.find.highlight()
		if c := d.edit.TextCursor(); c.SelectionStart() != 2 || c.SelectionEnd() != 5 {
			t.Errorf(`\n found %d to %d`, c.SelectionStart(), c.SelectionEnd())
		}
		if got := w.find.count.Text(); got != "1 of 1" {
			t.Errorf(`\n: count %q`, got)
		}
		w.find.next(false)
		if c := d.edit.TextCursor(); c.SelectionStart() != 2 {
			t.Errorf("Previous went to %d", c.SelectionStart())
		}
		w.find.replace.SetText(`b\n\ta`)
		w.find.replaceOne()
		if got := d.text(); got != "a\tb\n\ta\\tb\nc\n\nd" {
			t.Errorf("replaced one: %q", got)
		}

		// Every line break, as one change to undo.
		w.find.find.SetText(`\n`)
		w.find.replace.SetText(`\\n\n`)
		w.find.replaceAll()
		if got := d.text(); got != "a\tb\\n\n\ta\\tb\\n\nc\\n\n\\n\nd" {
			t.Errorf("replaced all: %q", got)
		}
		d.edit.Undo()
		if got := d.text(); got != "a\tb\n\ta\\tb\nc\n\nd" {
			t.Errorf("Replace All took more than one undo: %q", got)
		}

		// A selection with a backslash in it is searched for as it is.
		w.find.close()
		c := d.edit.TextCursor()
		c.SetPosition(5)
		c.SetPositionFull(9, qtgui.QTextCursorKeepAnchor)
		d.edit.SetTextCursor(c)
		w.run("find", false)
		if got := w.find.find.Text(); got != `a\\tb` {
			t.Errorf("the field took the selection as %q", got)
		}
		if got := w.find.count.Text(); got != "1 of 1" {
			t.Errorf("selection: count %q", got)
		}

		// The matches kept of a search across lines are found again once
		// the text has changed, and let go of when the tab closes.
		w.find.find.SetText(`c\n`)
		w.find.highlight()
		if got := len(d.editor.matches); got != 1 {
			t.Fatalf(`c\n: %d matches`, got)
		}
		d.edit.AppendPlainText("c\n")
		w.find.highlight()
		if got := len(d.editor.matches); got != 2 {
			t.Errorf(`c\n after an edit: %d matches`, got)
		}
		d.edit.Document().SetModifiedFull(false)
		w.closeDoc(d)
		if w.find.spanKey.doc == d || w.find.highlighted == d {
			t.Error("the find bar kept a closed document")
		}
	})
}

// TestFindGoesRoundAndCounts checks the searches that are cut short: one
// that began at an end of the text is not made again from it, and the
// selected match is looked for among the highlighted ones by halving.
func TestFindGoesRoundAndCounts(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		d.edit.SetPlainText("cat dog cat\nCat catalog")
		at := func() int { return d.edit.TextCursor().SelectionStart() }
		w.run("find", false)
		w.find.find.SetText("cat")
		w.find.highlight()

		// Every match in turn, forward and round, is counted as itself.
		d.goTo(0, 0)
		for i, want := range []int{0, 8, 12, 16, 0} {
			w.find.next(true)
			if count := fmt.Sprintf("%d of 4", i%4+1); at() != want || w.find.count.Text() != count {
				t.Errorf("Next %d: at %d showing %q, want %d and %q", i, at(), w.find.count.Text(), want, count)
			}
		}
		// Backward from the first, round to the last; and from the very end.
		w.find.next(false)
		if at() != 16 || w.find.count.Text() != "4 of 4" {
			t.Errorf("Previous from the first: at %d showing %q", at(), w.find.count.Text())
		}
		d.goTo(1, 11)
		w.find.next(false)
		if at() != 16 {
			t.Errorf("Previous from the end: at %d", at())
		}
		// A cursor that is on no match is counted as none.
		d.goTo(0, 5)
		if got := w.find.count.Text(); got != "4 matches" {
			t.Errorf("off the matches: count %q", got)
		}

		// No match, from the start, the middle and the end, either way.
		for _, pos := range [][2]int{{0, 0}, {0, 5}, {1, 11}} {
			for _, forward := range []bool{true, false} {
				d.goTo(pos[0], pos[1])
				w.find.find.SetText("zzz")
				before := at()
				if w.find.next(forward) || at() != before || w.find.find.Property("notFound").Value() != true {
					t.Errorf("zzz from %v, forward %v: found, or not shown as not found", pos, forward)
				}
			}
		}
		// Typed, it is shown as having no matches without highlights.
		d.goTo(0, 5)
		w.find.find.Clear()
		qutetest.Type(w.find.find, "zzz")
		if got := w.find.count.Text(); got != "No matches" || len(d.editor.matches) != 0 || w.find.highlighted != d {
			t.Errorf("typed zzz: count %q, %d matches", got, len(d.editor.matches))
		}
		// And what is typed next is found and highlighted again.
		w.find.find.Clear()
		qutetest.Type(w.find.find, "dog")
		if got := w.find.count.Text(); got != "1 of 1" || len(d.editor.matches) != 1 {
			t.Errorf("typed dog: count %q, %d matches", got, len(d.editor.matches))
		}
	})
}

func TestNotFound(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		w.current().edit.SetPlainText("text")
		w.run("find", false)
		qutetest.Type(w.find.find, "zzz")
		if w.find.find.Property("notFound").Value() != true || w.find.count.Text() != "No matches" {
			t.Errorf("notFound %v, count %q", w.find.find.Property("notFound").Value(), w.find.count.Text())
		}
	})
}

func TestFileChangedOnDisk(t *testing.T) {
	path := writeFile(t, "a.txt", []byte("one\n"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, path)
		d := w.current()

		// Unchanged here: read again at once.
		time.Sleep(10 * time.Millisecond) // a later modification time
		os.WriteFile(path, []byte("two\n"), 0o644)
		if !qutetest.WaitFor(func() bool { return d.text() == "two\n" }, 3*time.Second) {
			t.Fatalf("not reloaded: %q", d.text())
		}
		if d.modified() {
			t.Error("reloading left it modified")
		}

		// Changed here as well: asked about.
		qutetest.Type(d.edit, "mine ")
		os.WriteFile(path, []byte("three\n"), 0o644)
		if !qutetest.WaitFor(func() bool { return d.notice.IsVisible() }, 3*time.Second) {
			t.Fatal("no notice for a change on disk")
		}
		if !strings.Contains(d.text(), "mine") {
			t.Error("the text was replaced although it had changes")
		}
		qutetest.Click(d.noticeReload)
		if d.text() != "three\n" || d.notice.IsVisible() {
			t.Errorf("after Reload: %q, notice %v", d.text(), d.notice.IsVisible())
		}

		// Its own saves are not changes made by others.
		qutetest.Type(d.edit, "saved ")
		w.run("save", false)
		qutetest.Wait(400)
		if d.notice.IsVisible() || d.modified() {
			t.Error("its own save was taken for a change on disk")
		}

		// Deleted: said so, and saving writes it again.
		os.Remove(path)
		if !qutetest.WaitFor(func() bool { return d.notice.IsVisible() }, 3*time.Second) {
			t.Fatal("no notice for a deleted file")
		}
		if !d.modified() {
			t.Error("a deleted file's text is not marked to save")
		}
	})
}

func TestViewOptionsAreSaved(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		if !d.editor.gutter.IsVisible() || d.editor.ViewportMargins().Left == 0 {
			t.Fatal("no line numbers at first")
		}
		// Room for two digits and one more to their left.
		nine := d.editor.gutter.FontMetrics().HorizontalAdvance("9")
		if got := d.editor.gutterWidth(); got != 2*gutterPadding+3*nine {
			t.Errorf("the gutter is %d wide, want %d", got, 2*gutterPadding+3*nine)
		}
		w.acts["line_numbers"].Trigger()
		if d.editor.gutter.IsVisible() || d.editor.ViewportMargins().Left != w.cfg.Padding {
			t.Error("line numbers still shown")
		}
		w.acts["wrap_text"].Trigger()
		if d.edit.LineWrapMode() != qtwidgets.QPlainTextEditWidgetWidth {
			t.Error("not wrapping")
		}
		w.acts["toolbar"].Trigger()
		if w.toolbar.IsVisible() {
			t.Error("toolbar still shown")
		}
		c, err := config.Load(w.cfgPath)
		if err != nil || c.ShowLineNumbers || !c.WrapText || c.Toolbar {
			t.Errorf("saved %+v, %v", c, err)
		}
	})
}

func TestSettingsDialog(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		s := w.newSettingsDialog()
		defer s.dlg.DeleteLater()
		spin := qtwidgets.QSpinBoxCast(findChild(s.dlg, "tabWidth"))
		spin.SetValue(2)
		qtwidgets.QSpinBoxCast(findChild(s.dlg, "padding")).SetValue(12)
		save := qtwidgets.QKeySequenceEditCast(findChild(s.dlg, "shortcut_save"))
		save.SetKeySequence(keySequence("Ctrl+Alt+S"))
		if !s.apply() {
			t.Fatal("apply failed")
		}
		if w.cfg.TabWidth != 2 || w.cfg.Keybindings["save"] != "Ctrl+Alt+S" {
			t.Errorf("applied tab width %d, save %q", w.cfg.TabWidth, w.cfg.Keybindings["save"])
		}
		if d := w.current(); d.edit.Viewport().Y() != 12 || d.modified() {
			t.Errorf("padding %v, modified %v", d.edit.Viewport().Y(), d.modified())
		}
		if got := w.acts["save"].Shortcut().ToString(); got != "Ctrl+Alt+S" {
			t.Errorf("the action's shortcut is %q", got)
		}
		var saved map[string]any
		data, _ := os.ReadFile(w.cfgPath)
		json.Unmarshal(data, &saved)
		if saved["tab_width"] != 2.0 {
			t.Errorf("the file has %s", data)
		}

		// Two actions with one shortcut are refused.
		qtwidgets.QKeySequenceEditCast(findChild(s.dlg, "shortcut_open")).SetKeySequence(keySequence("Ctrl+Alt+S"))
		var said string
		qutetest.WhenModal(t, func(m *qtwidgets.QWidget) {
			box := qtwidgets.QMessageBoxCast(m)
			said = box.Text()
			qutetest.Click(box.Button(qtwidgets.QMessageBoxOk))
		})
		if s.apply() {
			t.Error("clashing shortcuts were applied")
		}
		// The actions are named by their labels, as the dialog lists them.
		if !strings.Contains(said, "Ctrl+Alt+S: Open, Save\n") {
			t.Errorf("the clash was reported as %q", said)
		}
	})
}

func TestSettingsDialogSizeIsSaved(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		qutetest.WhenModal(t, func(m *qtwidgets.QWidget) {
			m.Resize(900, 700)
			qtwidgets.QDialogCast(m).Reject() // Cancel keeps the size too
		})
		w.showSettings()
		c, err := config.Load(w.cfgPath)
		if err != nil || c.SettingsWidth != 900 || c.SettingsHeight != 700 {
			t.Fatalf("saved %dx%d, %v", c.SettingsWidth, c.SettingsHeight, err)
		}
		s := w.newSettingsDialog()
		defer s.dlg.DeleteLater()
		if s.dlg.Width() != 900 || s.dlg.Height() != 700 {
			t.Errorf("opened at %dx%d", s.dlg.Width(), s.dlg.Height())
		}
	})
}

func TestNewWindowStartsAProcess(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		var started string
		old := startProcess
		startProcess = func(path string) error { started = path; return nil }
		defer func() { startProcess = old }()
		w.run("new_window", false)
		if started == "" {
			t.Error("no process started")
		}
	})
}

func TestUndoRedoActionsFollowTheDocument(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		if w.acts["undo"].IsEnabled() || w.acts["copy"].IsEnabled() {
			t.Error("undo or copy enabled with nothing to do")
		}
		qutetest.Type(d.edit, "x")
		if !w.acts["undo"].IsEnabled() {
			t.Error("undo disabled after typing")
		}
		d.edit.SelectAll()
		if !w.acts["copy"].IsEnabled() {
			t.Error("copy disabled with a selection")
		}
		w.run("undo", false)
		if d.text() != "" || !w.acts["redo"].IsEnabled() {
			t.Errorf("after undo: %q, redo %v", d.text(), w.acts["redo"].IsEnabled())
		}
	})
}

// TestScreenshot saves the README's screenshot, docs/screenshot.png: a
// note open as it looks in everyday use.
func TestScreenshot(t *testing.T) {
	dir := os.Getenv("NOTEPAD_SCREENSHOTS")
	if dir == "" {
		t.Skip("set NOTEPAD_SCREENSHOTS to a folder to save the screenshot in")
	}
	// The file is in a home folder of its own, so the status bar shows a
	// path like a user's.
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "Documents", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(screenshotText), 0o644); err != nil {
		t.Fatal(err)
	}
	qutetest.Run(t, func() {
		// The window and the text in matching themes: the dark window
		// theme with Solarized Dark.
		w := newTestWindow(t, func(c *config.Config) {
			c.WindowTheme = "dark"
			c.EditorTheme = "solarized-dark"
			c.FontSize = 13
		}, path)
		w.w.Resize(900, 600)
		w.current().goTo(9, 26)
		qutetest.Wait(100)
		if err := qute.SaveScreenshot(w.w, filepath.Join(dir, "screenshot.png")); err != nil {
			t.Error(err)
		}
	})
}

const screenshotText = `Weekend plans

Saturday
	- Farmers' market first thing, before it gets busy
	- Pick up the bike from the repair shop (closes at 4)
	- Finish reading the last chapter of the book club novel

Sunday
	- Long walk along the river if the weather holds
	- Call Mum and Dad in the afternoon
	- Batch cook for the week: soup, curry and flapjacks

Shopping list
	- Oats, golden syrup, butter
	- Lentils, coconut milk, spinach
	- Coffee beans
`

// A change the watcher reports for a file whose tab has closed since does
// not watch the file again.
func TestClosedFileIsNotWatchedAgain(t *testing.T) {
	path := writeFile(t, "a.txt", []byte("one\n"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, path)
		if !slices.Contains(w.watcher.Files(), path) {
			t.Fatal("the open file is not watched")
		}
		w.changed[path] = true
		w.closeDoc(w.current())
		w.checkChanged()
		if slices.Contains(w.watcher.Files(), path) {
			t.Error("the closed file is watched again")
		}
	})
}

func TestSaveAsRefusesAFileOpenInAnotherTab(t *testing.T) {
	a := writeFile(t, "a.txt", []byte("a\n"))
	b := writeFile(t, "b.txt", []byte("b\n"))
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil, a, b)
		d := w.current()
		if d.path != b {
			t.Fatalf("current is %q", d.path)
		}
		answer(t, qtwidgets.QMessageBoxOk)
		if w.saveTo(d, a) {
			t.Error("saved over a file another tab has open")
		}
		if got, _ := os.ReadFile(a); string(got) != "a\n" {
			t.Errorf("a.txt was changed: %q", got)
		}
		if d.path != b {
			t.Errorf("the document's file became %q", d.path)
		}
	})
}

func TestFindWrapsAround(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		d := w.current()
		d.edit.SetPlainText("cat dog cat")
		w.run("find", false)
		qutetest.Type(w.find.find, "cat")
		w.run("find_next", false)
		if start := d.edit.TextCursor().SelectionStart(); start != 8 {
			t.Fatalf("Find Next went to %d", start)
		}
		w.run("find_next", false)
		if start := d.edit.TextCursor().SelectionStart(); start != 0 {
			t.Errorf("Find Next past the end went to %d", start)
		}
		if got := w.status.bar.CurrentMessage(); got != "Search wrapped around" {
			t.Errorf("status message %q", got)
		}
		w.run("find_prev", false)
		if start := d.edit.TextCursor().SelectionStart(); start != 8 {
			t.Errorf("Find Previous past the start went to %d", start)
		}
		if got := w.find.count.Text(); got != "2 of 2" {
			t.Errorf("count %q", got)
		}
	})
}

func TestCycleTabWrapsAround(t *testing.T) {
	qutetest.Run(t, func() {
		w := newTestWindow(t, nil)
		w.newTab()
		w.newTab()
		if w.tabs.CurrentIndex() != 2 {
			t.Fatalf("current tab %d", w.tabs.CurrentIndex())
		}
		w.run("next_tab", false)
		if w.tabs.CurrentIndex() != 0 {
			t.Errorf("Next Tab from the last went to %d", w.tabs.CurrentIndex())
		}
		w.run("prev_tab", false)
		if w.tabs.CurrentIndex() != 2 {
			t.Errorf("Previous Tab from the first went to %d", w.tabs.CurrentIndex())
		}
	})
}
