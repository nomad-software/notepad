// Package config loads the editor's JSON settings file, writes settings back
// to it, and holds the editor's colour themes and default keyboard
// shortcuts. It has no dependency on Qt.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"notepad/internal/atomicfile"
)

// Config is the user's settings.
type Config struct {
	ShowLineNumbers      bool    `json:"show_line_numbers"`
	WrapText             bool    `json:"wrap_text"`
	HighlightCurrentLine bool    `json:"highlight_current_line"`
	TabWidth             int     `json:"tab_width"`
	InsertSpaces         bool    `json:"insert_spaces"`
	FontFamily           string  `json:"font_family"`
	FontSize             float64 `json:"font_size"`
	Padding              int     `json:"padding"`      // pixels between the text and its edges
	WindowTheme          string  `json:"window_theme"` // system, light, dark
	EditorTheme          string  `json:"editor_theme"` // an id of Themes
	Toolbar              bool    `json:"toolbar"`
	// Themes are the editor themes by id; EditorTheme picks one. When the
	// file has a "themes" object it replaces the built-in one entirely.
	Themes map[string]ThemeSpec `json:"themes"`
	// Keybindings maps actions to shortcuts in Qt's portable text
	// ("Ctrl+Shift+S"); "" leaves an action without one.
	Keybindings map[string]string `json:"keybindings"`

	// The window's size, saved when it closes and used for the next one.
	WindowWidth     int  `json:"window_width"`
	WindowHeight    int  `json:"window_height"`
	WindowMaximized bool `json:"window_maximized"`

	// RecentFilesMax is how many files opened lately are remembered, for the
	// File menu's Open Recent; 0 remembers none. RecentFiles are they, the
	// latest first.
	RecentFilesMax int      `json:"recent_files_max"`
	RecentFiles    []string `json:"recent_files"`

	// The Settings dialog's size, saved when it closes; 0 lets it take the
	// size its contents ask for.
	SettingsWidth  int `json:"settings_width"`
	SettingsHeight int `json:"settings_height"`
}

// The ranges of the settings with limits, which the Settings dialog offers
// too. Values outside them are brought within.
const (
	MinFontSize, MaxFontSize = 5.0, 72.0
	MinTabWidth, MaxTabWidth = 1, 16
	MaxRecentFiles           = 50
	MaxPadding               = 50
)

// DefaultKeybindings maps every action that can have a shortcut to its
// default one.
var DefaultKeybindings = map[string]string{
	"new_window":     "Ctrl+Shift+N",
	"new_tab":        "Ctrl+T",
	"open":           "Ctrl+O",
	"save":           "Ctrl+S",
	"save_as":        "Ctrl+Shift+S",
	"close_tab":      "Ctrl+W",
	"quit":           "Ctrl+Q",
	"undo":           "Ctrl+Z",
	"redo":           "Ctrl+Shift+Z",
	"cut":            "Ctrl+X",
	"copy":           "Ctrl+C",
	"paste":          "Ctrl+V",
	"select_all":     "Ctrl+A",
	"find":           "Ctrl+F",
	"find_next":      "F3",
	"find_prev":      "Shift+F3",
	"replace":        "Ctrl+Shift+F",
	"go_to_line":     "Ctrl+L",
	"next_tab":       "Ctrl+PgDown",
	"prev_tab":       "Ctrl+PgUp",
	"settings":       "Ctrl+,",
	"about":          "Ctrl+I",
	"line_numbers":   "",
	"wrap_text":      "",
	"highlight_line": "",
	"toolbar":        "",
}

// Default returns the built-in settings.
func Default() *Config {
	return &Config{
		ShowLineNumbers:      true,
		WrapText:             false,
		HighlightCurrentLine: true,
		TabWidth:             4,
		InsertSpaces:         false,
		FontFamily:           "Iosevka Term Curly",
		FontSize:             16,
		Padding:              2,
		WindowTheme:          "system",
		EditorTheme:          DefaultTheme,
		Themes:               maps.Clone(BuiltinThemes),
		Toolbar:              true,
		Keybindings:          maps.Clone(DefaultKeybindings),
		WindowWidth:          900,
		WindowHeight:         640,
		RecentFilesMax:       10,
		RecentFiles:          []string{},
	}
}

// Dir returns the folder of the settings file: $XDG_CONFIG_HOME/notepad, or
// ~/.config/notepad. A relative XDG_CONFIG_HOME is ignored, as the XDG
// specification says.
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "notepad")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "notepad")
}

// Path returns the settings file's path.
func Path() string { return filepath.Join(Dir(), "config.json") }

// Load reads the settings file, taking the defaults for anything it does not
// set. A missing file is not an error. A value of the wrong type is reported
// and the rest of the file still applies; only a file that is not JSON at
// all leaves every setting at its default. The problems are reported on
// one line: values out of range and unknown keys all of them, values of the
// wrong type only the first (encoding/json stops counting there).
func Load(path string) (*Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	out := *c
	out.Keybindings = maps.Clone(c.Keybindings) // merged with the file's
	out.Themes = nil                            // a themes object in the file replaces the built-in one
	var problems []string
	if err := json.Unmarshal(data, &out); err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			return c, fmt.Errorf("%s: %w", path, describeJSONError(data, err))
		}
		problems = append(problems, fmt.Sprintf("%s: %v", path, err))
	}
	if out.Themes == nil {
		out.Themes = c.Themes
	}
	if out.Keybindings == nil { // "keybindings": null
		out.Keybindings = maps.Clone(DefaultKeybindings)
	}
	// Misspelt keys are reported without rejecting the file.
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) == nil {
		var unknown []string
		for k := range doc {
			if !knownKeys[k] {
				unknown = append(unknown, strconv.Quote(k))
			}
		}
		if len(unknown) > 0 {
			slices.Sort(unknown)
			problems = append(problems, fmt.Sprintf("%s: unknown settings %s", path, strings.Join(unknown, ", ")))
		}
	}
	problems = append(problems, out.normalize()...)
	if len(problems) == 0 {
		return &out, nil
	}
	return &out, errors.New(strings.Join(problems, "; "))
}

// knownKeys are the JSON keys of Config.
var knownKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeFor[Config]()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		keys[name] = true
	}
	return keys
}()

// describeJSONError adds a line number to a JSON syntax error.
func describeJSONError(data []byte, err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line := 1 + bytes.Count(data[:min(int(se.Offset), len(data))], []byte("\n"))
		return fmt.Errorf("line %d: %w", line, err)
	}
	return err
}

// normalize brings values within their ranges and puts back the defaults of
// those that are not valid, returning what was wrong.
func (c *Config) normalize() []string {
	var problems []string
	d := Default()
	if c.FontSize <= 0 {
		c.FontSize = d.FontSize
	}
	c.FontSize = min(max(c.FontSize, MinFontSize), MaxFontSize)
	c.Padding = min(max(c.Padding, 0), MaxPadding)
	if c.TabWidth <= 0 {
		c.TabWidth = d.TabWidth
	}
	c.TabWidth = min(max(c.TabWidth, MinTabWidth), MaxTabWidth)
	c.RecentFilesMax = min(max(c.RecentFilesMax, 0), MaxRecentFiles)
	c.RecentFiles = TrimRecent(c.RecentFiles, c.RecentFilesMax)
	if strings.TrimSpace(c.FontFamily) == "" {
		c.FontFamily = d.FontFamily
	}
	switch c.WindowTheme {
	case "system", "light", "dark":
	case "":
		c.WindowTheme = d.WindowTheme
	default:
		problems = append(problems, fmt.Sprintf("window_theme must be system, light or dark, not %q", c.WindowTheme))
		c.WindowTheme = d.WindowTheme
	}
	if len(c.Themes) == 0 {
		problems = append(problems, "themes is empty; the built-in themes are used")
		c.Themes = maps.Clone(BuiltinThemes)
	}
	for _, id := range c.ThemeIDs() {
		spec := c.Themes[id]
		if spec.Name == "" {
			spec.Name = id
			c.Themes[id] = spec
		}
		if _, err := spec.Resolve(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if _, ok := c.Themes[c.EditorTheme]; !ok {
		if c.EditorTheme != "" {
			problems = append(problems, fmt.Sprintf("unknown editor_theme %q (have %s)", c.EditorTheme, strings.Join(c.ThemeIDs(), ", ")))
		}
		c.EditorTheme = c.ThemeIDs()[0]
		if _, ok := c.Themes[DefaultTheme]; ok {
			c.EditorTheme = DefaultTheme
		}
	}
	for action := range c.Keybindings {
		if _, ok := DefaultKeybindings[action]; !ok {
			problems = append(problems, fmt.Sprintf("unknown keybinding action %q", action))
			delete(c.Keybindings, action)
		}
	}
	if clashes := Clashes(c.Keybindings); len(clashes) > 0 {
		var shown []string
		for _, cl := range clashes {
			shown = append(shown, cl.String())
		}
		problems = append(problems, "keybindings used by more than one action: "+strings.Join(shown, ", "))
	}
	slices.Sort(problems)
	return problems
}

// AddRecent returns the list of recent files with path first, once, and at
// most n long.
func AddRecent(list []string, path string, n int) []string {
	out := []string{path}
	for _, p := range list {
		if p != path {
			out = append(out, p)
		}
	}
	return TrimRecent(out, n)
}

// RemoveRecent returns the list of recent files without path.
func RemoveRecent(list []string, path string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(p string) bool { return p == path })
}

// TrimRecent returns the list of recent files without empty or repeated
// paths, at most n long.
func TrimRecent(list []string, n int) []string {
	out := []string{}
	for _, p := range list {
		if p != "" && !slices.Contains(out, p) && len(out) < n {
			out = append(out, p)
		}
	}
	return out
}

// normalizeShortcut puts a shortcut in the form shortcuts are compared in:
// without spaces, in lower case.
func normalizeShortcut(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", ""))
}

// Clash is a shortcut that more than one action has.
type Clash struct {
	Key     string   // as the first of the actions spells it
	Actions []string // sorted
}

// String returns the clash as "Ctrl+S (save, save_as)".
func (c Clash) String() string {
	return fmt.Sprintf("%s (%s)", c.Key, strings.Join(c.Actions, ", "))
}

// Clashes returns each shortcut that more than one action has, with those
// actions, sorted by the shortcut.
func Clashes(bindings map[string]string) []Clash {
	byKey := map[string][]string{}
	for action, key := range bindings {
		if n := normalizeShortcut(key); n != "" {
			byKey[n] = append(byKey[n], action)
		}
	}
	var out []Clash
	for _, actions := range byKey {
		if len(actions) > 1 {
			slices.Sort(actions)
			out = append(out, Clash{Key: bindings[actions[0]], Actions: actions})
		}
	}
	slices.SortFunc(out, func(a, b Clash) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// SetValues writes the values, by their JSON keys, into the settings file,
// keeping the rest of what it holds. The file and its folder are made if
// they are not there; a file that is a symbolic link has its target written,
// and an existing file keeps its permissions. It is written through a
// temporary file beside it, so that a failure leaves the old file whole.
func SetValues(path string, values map[string]any) error {
	path = atomicfile.Resolve(path)
	doc := map[string]json.RawMessage{}
	mode := os.FileMode(0o644)
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf("%s: %w", path, describeJSONError(data, err))
			}
			if doc == nil { // the file is null, which Load takes as no settings
				doc = map[string]json.RawMessage{}
			}
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for k, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		doc[k] = raw
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), mode)
}
