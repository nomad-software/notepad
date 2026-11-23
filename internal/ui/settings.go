package ui

import (
	"math"
	"slices"
	"strings"

	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"notepad/internal/config"
	"notepad/internal/desktop"
)

// choice is a combo box over a fixed list of setting values.
type choice struct {
	box    *qtwidgets.QComboBox
	values []string
}

func newChoice(labels, values []string, current string) choice {
	c := choice{box: qtwidgets.NewQComboBox(nil), values: values}
	for i, l := range labels {
		c.box.AddItem(l)
		if values[i] == current {
			c.box.SetCurrentIndex(i)
		}
	}
	return c
}

func (c choice) value() string {
	if i := c.box.CurrentIndex(); i >= 0 && i < len(c.values) {
		return c.values[i]
	}
	return ""
}

// settingsSection adds a titled group box to layout, its fields stretching
// to its width.
func settingsSection(layout *qtwidgets.QVBoxLayout, title string) (*qtwidgets.QGroupBox, *qtwidgets.QFormLayout) {
	box := qtwidgets.NewQGroupBoxWithTitle(title, nil)
	form := qtwidgets.NewQFormLayout(box)
	form.SetFieldGrowthPolicy(qtwidgets.QFormLayoutAllNonFixedFieldsGrow)
	layout.AddWidget(box)
	return box, form
}

func checkBox(text string, on bool) *qtwidgets.QCheckBox {
	c := qtwidgets.NewQCheckBoxWithText(text, nil)
	c.SetChecked(on)
	return c
}

// settingsDialog is the Settings dialog: General, User Interface, Colours
// and Keyboard Shortcuts. Its settings are written to the settings file on
// OK or Apply and applied at once.
type settingsDialog struct {
	dlg  *qtwidgets.QDialog
	tabs *qtwidgets.QTabWidget
	// apply saves and applies the settings, reporting whether it could.
	apply func() bool
}

func (w *Window) newSettingsDialog() *settingsDialog {
	cfg := w.cfg
	s := &settingsDialog{}
	dlg := qtwidgets.NewQDialog(w.w)
	s.dlg = dlg
	dlg.SetObjectName("notepadSettings")
	dlg.SetWindowTitle("Settings")
	if cfg.SettingsWidth >= minDialogWidth && cfg.SettingsHeight >= minDialogHeight {
		dlg.Resize(cfg.SettingsWidth, cfg.SettingsHeight)
	}
	root := qtwidgets.NewQVBoxLayout(dlg)
	s.tabs = qtwidgets.NewQTabWidget(dlg)

	// ---- General
	general := qtwidgets.NewQWidget(nil)
	gl := qtwidgets.NewQVBoxLayout(general)
	_, ef := settingsSection(gl, "Editor")
	lineNumbers := checkBox("Show line numbers", cfg.ShowLineNumbers)
	ef.AddRowText("", lineNumbers)
	highlight := checkBox("Highlight the current line", cfg.HighlightCurrentLine)
	ef.AddRowText("", highlight)
	wrap := checkBox("Wrap long lines", cfg.WrapText)
	ef.AddRowText("", wrap)

	_, tf := settingsSection(gl, "Tabs")
	tabWidth := qtwidgets.NewQSpinBox(nil)
	tabWidth.SetObjectName("tabWidth")
	tabWidth.SetRange(config.MinTabWidth, config.MaxTabWidth)
	tabWidth.SetSuffix(" spaces")
	tabWidth.SetValue(cfg.TabWidth)
	tf.AddRowText("Tab width:", tabWidth)
	spaces := checkBox("Insert spaces instead of tabs", cfg.InsertSpaces)
	tf.AddRowText("", spaces)

	_, rf := settingsSection(gl, "Recent files")
	recentMax := qtwidgets.NewQSpinBox(nil)
	recentMax.SetObjectName("recentFilesMax")
	recentMax.SetRange(0, config.MaxRecentFiles)
	recentMax.SetSuffix(" files")
	recentMax.SetSpecialValueText("None")
	recentMax.SetValue(cfg.RecentFilesMax)
	recentMax.SetToolTip("How many files opened lately the File menu's Open Recent lists")
	clearRecent := qtwidgets.NewQPushButtonWithText("Clear the List", nil)
	clearRecent.OnClicked(func(bool) { w.setRecentFiles([]string{}) })
	recentRow := qtwidgets.NewQHBoxLayout(nil)
	recentRow.AddWidgetFull(recentMax, 1, 0)
	recentRow.AddWidget(clearRecent)
	rf.AddRowTextLayout("Remember:", recentRow)
	gl.AddStretch()

	// ---- User Interface
	look := qtwidgets.NewQWidget(nil)
	ll := qtwidgets.NewQVBoxLayout(look)
	_, wf := settingsSection(ll, "Window")
	windowTheme := newChoice([]string{"Follow the system", "Light", "Dark"},
		[]string{"system", "light", "dark"}, cfg.WindowTheme)
	wf.AddRowText("Theme:", windowTheme.box)
	toolbar := checkBox("Show the toolbar", cfg.Toolbar)
	wf.AddRowText("", toolbar)

	_, ff := settingsSection(ll, "Appearance")
	font := qtwidgets.NewQFontComboBox(nil)
	// Every font is offered, monospaced or not.
	font.SetFontFilters(qtwidgets.QFontComboBoxAllFonts)
	font.SetCurrentFont(qtgui.NewQFontWithFamily(cfg.FontFamily))
	size := qtwidgets.NewQDoubleSpinBox(nil)
	size.SetRange(config.MinFontSize, config.MaxFontSize)
	size.SetDecimals(1)
	size.SetSingleStep(0.5)
	size.SetSuffix(" pt")
	size.SetValue(cfg.FontSize)
	fontRow := qtwidgets.NewQHBoxLayout(nil)
	fontRow.AddWidgetFull(font, 1, 0)
	fontRow.AddWidget(size)
	ff.AddRowTextLayout("Font:", fontRow)
	padding := qtwidgets.NewQSpinBox(nil)
	padding.SetObjectName("padding")
	padding.SetRange(0, config.MaxPadding)
	padding.SetSuffix(" px")
	padding.SetValue(cfg.Padding)
	ff.AddRowText("Padding:", padding)

	ll.AddStretch()

	coloursPage, readThemes := w.themesTab(dlg)
	keysPage, readKeys := w.shortcutsTab()

	s.tabs.AddTab(general, "General")
	s.tabs.AddTab(look, "User Interface")
	s.tabs.AddTab(coloursPage, "Colours")
	s.tabs.AddTab(keysPage, "Keyboard Shortcuts")
	root.AddWidget(s.tabs)

	buttons := qtwidgets.NewQDialogButtonBoxWithStandardButton(
		qtwidgets.QDialogButtonBoxOk|qtwidgets.QDialogButtonBoxCancel|qtwidgets.QDialogButtonBoxApply, nil)
	root.AddWidget(buttons)

	s.apply = func() bool {
		bindings, clashes := readKeys()
		if len(clashes) > 0 {
			s.tabs.SetCurrentWidget(keysPage)
			qtwidgets.QMessageBoxWarning(dlg, desktop.DisplayName, "These shortcuts are used by more than one action:\n\n"+
				strings.Join(clashes, "\n")+"\n\nChange or clear them, then try again.")
			return false
		}
		values := map[string]any{
			"show_line_numbers":      lineNumbers.IsChecked(),
			"highlight_current_line": highlight.IsChecked(),
			"wrap_text":              wrap.IsChecked(),
			"tab_width":              tabWidth.Value(),
			"insert_spaces":          spaces.IsChecked(),
			"font_family":            font.CurrentFont().Family(),
			"font_size":              math.Round(size.Value()*10) / 10,
			"padding":                padding.Value(),
			"window_theme":           windowTheme.value(),
			"toolbar":                toolbar.IsChecked(),
			"keybindings":            bindings,
			"recent_files_max":       recentMax.Value(),
			// Fewer remembered drops the oldest from the list now.
			"recent_files": config.TrimRecent(w.recentFiles(), recentMax.Value()),
		}
		values["themes"], values["editor_theme"] = readThemes()
		if err := config.SetValues(w.cfgPath, values); err != nil {
			qtwidgets.QMessageBoxWarning(dlg, desktop.DisplayName, "The settings could not be saved:\n"+err.Error())
			return false
		}
		c, err := config.Load(w.cfgPath)
		if err != nil {
			w.message("Settings: " + err.Error())
		}
		w.applyConfig(c)
		return true
	}
	buttons.OnAccepted(func() {
		if s.apply() {
			dlg.Accept()
		}
	})
	buttons.OnRejected(dlg.Reject)
	buttons.Button(qtwidgets.QDialogButtonBoxApply).OnClicked(func(bool) { s.apply() })
	return s
}

// The least size of the Settings dialog that is saved and used again, so
// that a size saved wrongly cannot leave it unusable.
const minDialogWidth, minDialogHeight = 200, 150

// saveSettingsSize stores the Settings dialog's size in the settings file,
// for it to open the same size next time.
func (w *Window) saveSettingsSize(width, height int) {
	c := w.cfg
	if width < minDialogWidth || height < minDialogHeight || (c.SettingsWidth == width && c.SettingsHeight == height) {
		return
	}
	if err := config.SetValues(w.cfgPath, map[string]any{"settings_width": width, "settings_height": height}); err != nil {
		w.message("The Settings dialog's size could not be saved: " + err.Error())
		return
	}
	c.SettingsWidth, c.SettingsHeight = width, height
}

// showSettings opens the Settings dialog and waits for it to close.
func (w *Window) showSettings() {
	s := w.newSettingsDialog()
	s.dlg.Exec()
	if s.dlg.Alive() {
		w.saveSettingsSize(s.dlg.Width(), s.dlg.Height())
		s.dlg.DeleteLater()
	}
	if d := w.current(); d != nil {
		d.edit.SetFocus()
	}
}

// shortcutsTab builds the Settings dialog's Keyboard Shortcuts page. read
// returns the shortcuts as edited, in Qt's portable text, with any that
// more than one action has.
func (w *Window) shortcutsTab() (page *qtwidgets.QWidget, read func() (map[string]string, []string)) {
	page = qtwidgets.NewQWidget(nil)
	outer := qtwidgets.NewQVBoxLayout(page)
	note := qtwidgets.NewQLabelWithText("Click a shortcut and press the new keys. Clear it to leave the action without one.", nil)
	note.SetWordWrap(true)
	outer.AddWidget(note)

	scroll := qtwidgets.NewQScrollArea(page)
	scroll.SetWidgetResizable(true)
	inner := qtwidgets.NewQWidget(nil)
	form := qtwidgets.NewQFormLayout(inner)
	edits := make([]*qtwidgets.QKeySequenceEdit, len(actionSpecs))
	for i, spec := range actionSpecs {
		e := qtwidgets.NewQKeySequenceEdit(inner)
		e.SetObjectName("shortcut_" + spec.id)
		e.SetClearButtonEnabled(true)
		e.SetKeySequence(keySequence(w.cfg.Keybindings[spec.id]))
		// One key combination per action: anything typed after the
		// first is dropped.
		e.SetMaximumSequenceLength(1)
		edits[i] = e
		form.AddRowText(plainLabel(spec.label)+":", e)
	}
	scroll.SetWidget(inner)
	outer.AddWidgetFull(scroll, 1, 0)

	defaults := qtwidgets.NewQPushButtonWithText("Restore Defaults", nil)
	defaults.OnClicked(func(bool) {
		for i, spec := range actionSpecs {
			edits[i].SetKeySequence(keySequence(config.DefaultKeybindings[spec.id]))
		}
	})
	row := qtwidgets.NewQHBoxLayout(nil)
	row.AddStretch()
	row.AddWidget(defaults)
	outer.AddLayout(row)

	read = func() (map[string]string, []string) {
		out := map[string]string{}
		for i, spec := range actionSpecs {
			out[spec.id] = edits[i].KeySequence().ToString()
		}
		var clashes []string
		for _, c := range config.Clashes(out) {
			clashes = append(clashes, labelClash(c))
		}
		return out, clashes
	}
	return page, read
}

// labelClash describes a clash with the actions' labels: "Ctrl+S: Save,
// Save As".
func labelClash(clash config.Clash) string {
	var labels []string
	for _, id := range clash.Actions {
		label := id
		if i := slices.IndexFunc(actionSpecs, func(s actionSpec) bool { return s.id == id }); i >= 0 {
			label = plainLabel(actionSpecs[i].label)
		}
		labels = append(labels, label)
	}
	return clash.Key + ": " + strings.Join(labels, ", ")
}
