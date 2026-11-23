package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"notepad/internal/icons"
)

// maxHighlights is how many of a search's matches are highlighted at most,
// so that a search for "e" in a large file stays quick.
const maxHighlights = 2000

// escapesTip tells of the escapes the bar's fields take (unescape).
const escapesTip = `\t is a tab, \n a line break and \\ a backslash`

// findBar is the bar along the bottom of a window that finds text in the
// current document and, opened for replacing, replaces it.
type findBar struct {
	win        *Window
	box        *qtwidgets.QWidget
	find       *qtwidgets.QLineEdit
	replace    *qtwidgets.QLineEdit
	replaceRow *qtwidgets.QWidget
	matchCase  *qtwidgets.QCheckBox
	wholeWords *qtwidgets.QCheckBox
	count      *qtwidgets.QLabel
	// refresh highlights the matches again a moment after the text
	// changes, rather than at every key.
	refresh *qtcore.QTimer
	// highlighted is the document whose matches are highlighted.
	highlighted *Doc
	// more is whether it has more matches than are highlighted.
	more bool
	// spanList are the matches of the search spanKey.
	spanKey  spanSearch
	spanList []span
}

// fieldKeys handles the keys of the bar's text fields: Escape closes the
// bar, and Enter finds the next match (with Shift, the previous one).
type fieldKeys struct {
	qtwidgets.QLineEditBase
	bar     *findBar
	replace bool
}

// KeyPressEvent overrides QWidget::keyPressEvent.
func (f *fieldKeys) KeyPressEvent(ev *qtgui.QKeyEvent) {
	switch ev.Key() {
	case qtcore.Key_Escape:
		f.bar.close()
		return
	case qtcore.Key_Return, qtcore.Key_Enter:
		switch {
		case f.replace && ev.Modifiers()&qtcore.ControlModifier != 0:
			f.bar.replaceAll()
		case f.replace:
			f.bar.replaceOne()
		default:
			f.bar.next(ev.Modifiers()&qtcore.ShiftModifier == 0)
		}
		return
	}
	f.Super().KeyPressEvent(ev)
}

func newFindBar(w *Window) *findBar {
	b := &findBar{win: w}
	b.box = qtwidgets.NewQWidget(nil)
	b.box.SetObjectName("notepadFindBar")
	outer := qtwidgets.NewQVBoxLayout(b.box)
	outer.SetContentsMargins(qtcore.QMargins{Left: 8, Top: 6, Right: 8, Bottom: 6})
	outer.SetSpacing(6)

	field := func(placeholder string, replace bool) *qtwidgets.QLineEdit {
		e := qtwidgets.NewQLineEditWith(&fieldKeys{bar: b, replace: replace}, nil)
		e.SetPlaceholderText(placeholder)
		e.SetClearButtonEnabled(true)
		e.SetMinimumWidth(220)
		return e
	}
	button := func(text string, fn func()) *qtwidgets.QPushButton {
		p := qtwidgets.NewQPushButtonWithText(text, nil)
		p.SetFocusPolicy(qtcore.TabFocus)
		p.OnClicked(func(bool) { fn() })
		return p
	}

	// Find row.
	row := qtwidgets.NewQHBoxLayout(nil)
	row.SetSpacing(6)
	b.find = field("Find", false)
	b.find.SetToolTip(escapesTip)
	b.find.SetObjectName("notepadFindField")
	b.find.OnTextEdited(func(string) { b.incremental() })
	row.AddWidget(b.find)
	row.AddWidget(button("Previous", func() { b.next(false) }))
	row.AddWidget(button("Next", func() { b.next(true) }))
	b.matchCase = qtwidgets.NewQCheckBoxWithText("Match case", nil)
	b.wholeWords = qtwidgets.NewQCheckBoxWithText("Whole words", nil)
	for _, c := range []*qtwidgets.QCheckBox{b.matchCase, b.wholeWords} {
		c.SetFocusPolicy(qtcore.TabFocus)
		c.OnToggled(func(bool) { b.incremental() })
		row.AddWidget(c)
	}
	b.count = qtwidgets.NewQLabel(nil)
	row.AddWidgetFull(b.count, 1, 0)
	closeButton := qtwidgets.NewQToolButton(nil)
	closeButton.SetAutoRaise(true)
	closeButton.SetFocusPolicy(qtcore.NoFocus)
	closeButton.SetIcon(icon(icons.Close))
	closeButton.SetIconSize(qtcore.QSize{Width: 14, Height: 14})
	closeButton.SetToolTip("Close (Escape)")
	closeButton.OnClicked(func(bool) { b.close() })
	row.AddWidget(closeButton)
	outer.AddLayout(row)

	// Replace row.
	b.replaceRow = qtwidgets.NewQWidget(nil)
	rrow := qtwidgets.NewQHBoxLayout(b.replaceRow)
	rrow.SetContentsMargins(qtcore.QMargins{})
	rrow.SetSpacing(6)
	b.replace = field("Replace with", true)
	b.replace.SetToolTip(escapesTip)
	rrow.AddWidget(b.replace)
	rrow.AddWidget(button("Replace", b.replaceOne))
	rrow.AddWidget(button("Replace All", b.replaceAll))
	rrow.AddStretch()
	outer.AddWidget(b.replaceRow)

	b.refresh = qtcore.NewQTimer(b.box)
	b.refresh.SetSingleShot(true)
	b.refresh.SetInterval(250)
	b.refresh.OnTimeout(b.highlight)

	b.box.SetVisible(false)
	return b
}

// visible reports whether the bar is open.
func (b *findBar) visible() bool { return b.box.IsVisible() }

// open opens the bar, with the replace row if replace, for the current
// document, starting from the text selected in it if that is on one line.
func (b *findBar) open(replace bool) {
	b.show(replace)
	if d := b.win.current(); d != nil {
		if c := d.edit.TextCursor(); c.HasSelection() && !d.editor.selectionSpansLines(c) {
			b.find.SetText(escape(c.SelectedText()))
		}
	}
	b.find.SetFocus()
	b.find.SelectAll()
	b.highlight()
}

// show shows the bar, with the replace row if replace, leaving the focus
// where it is.
func (b *findBar) show(replace bool) {
	b.box.SetVisible(true)
	b.replaceRow.SetVisible(replace)
}

// close closes the bar, takes the highlights off, and gives the focus back
// to the document.
func (b *findBar) close() {
	b.box.SetVisible(false)
	b.refresh.Stop()
	b.clearHighlights()
	b.spanKey, b.spanList = spanSearch{}, nil
	b.setNotFound(false)
	if d := b.win.current(); d != nil {
		d.edit.SetFocus()
	}
}

// forget lets go of what is kept of a document that is closing.
func (b *findBar) forget(d *Doc) {
	if b.highlighted == d {
		b.highlighted = nil
	}
	if b.spanKey.doc == d {
		b.spanKey, b.spanList = spanSearch{}, nil
	}
}

func (b *findBar) clearHighlights() {
	if b.highlighted != nil {
		b.highlighted.editor.setMatches(nil)
		b.highlighted = nil
	}
	b.count.SetText("")
}

// flags are the search's options as Qt takes them.
func (b *findBar) flags() qtgui.QTextDocumentFindFlags {
	var f qtgui.QTextDocumentFindFlags
	if b.matchCase.IsChecked() {
		f |= qtgui.QTextDocumentFindCaseSensitively
	}
	if b.wholeWords.IsChecked() {
		f |= qtgui.QTextDocumentFindWholeWords
	}
	return f
}

// needle is the text searched for: the find field's, with its escapes
// (\t, \n, \\) turned into what they stand for.
func (b *findBar) needle() string { return unescape(b.find.Text()) }

// spanSearch is a search with a line break in it and the text it was made
// in, by which its matches are kept from one use to the next: finding the
// next match and highlighting them all would otherwise each read the whole
// text.
type spanSearch struct {
	doc                   *Doc
	revision              int
	needle                string
	matchCase, wholeWords bool
}

// spans finds every match of a search that has a line break in it, which
// Qt's own search can't: it looks in one line at a time.
func (b *findBar) spans(d *Doc, needle string) []span {
	key := spanSearch{d, d.edit.Document().Revision(), needle, b.matchCase.IsChecked(), b.wholeWords.IsChecked()}
	if key != b.spanKey {
		b.spanKey, b.spanList = key, findAll(d.text(), needle, key.matchCase, key.wholeWords)
	}
	return b.spanList
}

// multiline reports whether a search has a line break in it, and so is
// made with spans rather than by Qt.
func multiline(needle string) bool { return strings.Contains(needle, "\n") }

// cursorOver returns a cursor with s selected.
func cursorOver(d *Doc, s span) *qtgui.QTextCursor {
	c := d.edit.TextCursor()
	c.SetPosition(s.start)
	c.SetPositionFull(s.end, qtgui.QTextCursorKeepAnchor)
	return c
}

// nextSpan returns the match of a search with a line break in it that
// comes after the selection, or before it, and whether the search went
// round the end of the document. The cursor is null if there is no match.
func (b *findBar) nextSpan(d *Doc, needle string, forward bool) (found *qtgui.QTextCursor, wrapped bool) {
	all := b.spans(d, needle)
	if len(all) == 0 {
		return qtgui.NewQTextCursor(), false
	}
	sel := d.edit.TextCursor()
	var i int
	if forward {
		i = sort.Search(len(all), func(i int) bool { return all[i].start >= sel.SelectionEnd() })
		if wrapped = i == len(all); wrapped {
			i = 0
		}
	} else {
		i = sort.Search(len(all), func(i int) bool { return all[i].end > sel.SelectionStart() }) - 1
		if wrapped = i < 0; wrapped {
			i = len(all) - 1
		}
	}
	return cursorOver(d, all[i]), wrapped
}

// nextInLine is nextSpan for a search within a line, which Qt makes.
func (b *findBar) nextInLine(d *Doc, needle string, forward bool) (found *qtgui.QTextCursor, wrapped bool) {
	flags := b.flags()
	if !forward {
		flags |= qtgui.QTextDocumentFindBackward
	}
	doc := d.edit.Document()
	sel := d.edit.TextCursor()
	found = doc.FindWithTextCursorFull(needle, sel, flags)
	if !found.IsNull() {
		return found, false
	}
	// The search goes round from the other end, unless it began there and
	// so has been through the whole text already.
	from, began := 0, sel.SelectionEnd()
	if !forward {
		from, began = doc.CharacterCount()-1, sel.SelectionStart()
	}
	if began == from {
		return found, false
	}
	found = doc.FindFull(needle, from, flags)
	return found, !found.IsNull()
}

// setNotFound shows the search field's text in red when there is no match.
func (b *findBar) setNotFound(on bool) {
	setBoolProperty(b.find, "notFound", on)
	b.find.Style().Unpolish(b.find)
	b.find.Style().Polish(b.find)
}

// incremental searches as the text is typed: from the start of the
// selection, so that the match grows as the word is typed.
func (b *findBar) incremental() {
	d := b.win.current()
	if d == nil {
		return
	}
	c := d.edit.TextCursor()
	c.SetPosition(c.SelectionStart())
	d.edit.SetTextCursor(c)
	b.nextAndHighlight()
}

// next selects the next match after the selection, or before it, going
// round past the end (or start) of the document. It reports whether there
// is a match.
func (b *findBar) next(forward bool) bool {
	d := b.win.current()
	text := b.needle()
	if d == nil {
		return false
	}
	if text == "" {
		b.setNotFound(false)
		return false
	}
	search := b.nextInLine
	if multiline(text) {
		search = b.nextSpan
	}
	found, wrapped := search(d, text, forward)
	b.setNotFound(found.IsNull())
	if found.IsNull() {
		b.win.message(fmt.Sprintf("%q not found", text))
		return false
	}
	if wrapped {
		b.win.message("Search wrapped around")
	}
	d.edit.SetTextCursor(found)
	d.edit.CenterCursor()
	b.showCount(d)
	return true
}

// nextAndHighlight selects the next match and highlights them all. A
// search that finds no next match has been through the whole text, which
// is not then searched again for matches to highlight.
func (b *findBar) nextAndHighlight() {
	d := b.win.current()
	if b.next(true) || d == nil || b.needle() == "" || !b.visible() {
		b.highlight()
		return
	}
	b.clearHighlights()
	b.more = false
	b.highlighted = d
	b.setNotFound(true)
	b.showCount(d)
}

// highlight highlights the current document's matches, up to
// maxHighlights.
func (b *findBar) highlight() {
	b.clearHighlights()
	d := b.win.current()
	text := b.needle()
	if d == nil || text == "" || !b.visible() {
		b.setNotFound(false)
		return
	}
	doc := d.edit.Document()
	flags := b.flags()
	var cursors []*qtgui.QTextCursor
	// One more than are shown is looked for, to tell whether there are more.
	if multiline(text) {
		all := b.spans(d, text)
		for _, s := range all[:min(len(all), maxHighlights+1)] {
			cursors = append(cursors, cursorOver(d, s))
		}
	} else {
		for c := doc.FindFull(text, 0, flags); !c.IsNull() && len(cursors) <= maxHighlights; c = doc.FindWithTextCursorFull(text, c, flags) {
			cursors = append(cursors, c)
		}
	}
	b.more = len(cursors) > maxHighlights
	if b.more {
		cursors = cursors[:maxHighlights]
	}
	d.editor.setMatches(cursors)
	b.highlighted = d
	b.setNotFound(len(cursors) == 0)
	b.showCount(d)
}

// showCount shows how many matches there are, and which the selection is.
func (b *findBar) showCount(d *Doc) {
	if b.highlighted != d {
		return
	}
	// The matches are in the order they are in the text, and it is asked
	// at every move of the cursor, so the selection is looked for by
	// halving rather than through them all.
	matches := d.editor.matches
	sel := d.edit.TextCursor()
	current := 0
	i := sort.Search(len(matches), func(i int) bool { return matches[i].Cursor().SelectionStart() >= sel.SelectionStart() })
	if i < len(matches) {
		if c := matches[i].Cursor(); c.SelectionStart() == sel.SelectionStart() && c.SelectionEnd() == sel.SelectionEnd() {
			current = i + 1
		}
	}
	b.count.SetText(matchCount(current, len(matches), b.more))
}

// textChanged is told that a document's text has changed: its highlights
// are found again shortly.
func (b *findBar) textChanged(d *Doc) {
	if b.visible() && d == b.win.current() && b.find.Text() != "" {
		b.refresh.Start()
	}
}

// currentChanged is told that another document is the current one.
func (b *findBar) currentChanged() {
	if b.visible() {
		b.highlight()
	} else {
		b.clearHighlights()
	}
}

// selectionMatches reports whether the selection is a match of the search,
// as Replace replaces it only then.
func (b *findBar) selectionMatches(d *Doc) bool {
	c := d.edit.TextCursor()
	if !c.HasSelection() {
		return false
	}
	// The search itself says, so that Match case and Whole words are
	// heeded as they are when finding.
	text := b.needle()
	if multiline(text) {
		return slices.Contains(b.spans(d, text), span{c.SelectionStart(), c.SelectionEnd()})
	}
	m := d.edit.Document().FindFull(text, c.SelectionStart(), b.flags())
	return !m.IsNull() && m.SelectionStart() == c.SelectionStart() && m.SelectionEnd() == c.SelectionEnd()
}

// replaceOne replaces the selected match and selects the next one.
func (b *findBar) replaceOne() {
	d := b.win.current()
	if d == nil || b.find.Text() == "" {
		return
	}
	if b.selectionMatches(d) {
		c := d.edit.TextCursor()
		c.InsertText(unescape(b.replace.Text()))
		d.edit.SetTextCursor(c)
	}
	b.nextAndHighlight()
}

// replaceAll replaces every match, as one change to undo.
func (b *findBar) replaceAll() {
	d := b.win.current()
	text, with := b.needle(), unescape(b.replace.Text())
	if d == nil || text == "" {
		return
	}
	doc := d.edit.Document()
	flags := b.flags()
	edit := d.edit.TextCursor()
	edit.BeginEditBlock()
	n := 0
	// Each search starts after the text put in, so that a replacement that
	// contains what is searched for is not replaced again.
	if multiline(text) {
		// From the last match back, so that the matches before it stay
		// where they were found; and with the one cursor, as the document
		// moves every cursor there is at each change.
		all := b.spans(d, text)
		for _, s := range slices.Backward(all) {
			edit.SetPosition(s.start)
			edit.SetPositionFull(s.end, qtgui.QTextCursorKeepAnchor)
			edit.InsertText(with)
		}
		n = len(all)
	} else {
		for c := doc.FindFull(text, 0, flags); !c.IsNull(); c = doc.FindWithTextCursorFull(text, c, flags) {
			c.InsertText(with)
			n++
		}
	}
	edit.EndEditBlock()
	b.highlight()
	if n == 0 {
		b.win.message(fmt.Sprintf("%q not found", text))
		return
	}
	b.win.message(fmt.Sprintf("Replaced %s", plural(n, "occurrence", "occurrences")))
}
