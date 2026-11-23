package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// The helpers in this file work on Go strings alone, so that they can be
// tested without Qt.

// utf16Prefix returns how many bytes of s hold its first n UTF-16 code
// units: Qt counts positions in a line in code units.
func utf16Prefix(s string, n int) int {
	units := 0
	for i, r := range s {
		if units >= n {
			return i
		}
		units += utf16.RuneLen(r) // an invalid byte is U+FFFD here, one unit
	}
	return len(s)
}

// visualColumn returns the column, counting from 0, at which the character
// n UTF-16 code units into line is shown: tabs reach the next tab stop.
func visualColumn(line string, n, tabWidth int) int {
	col := 0
	for _, r := range line[:utf16Prefix(line, n)] {
		if r == '\t' && tabWidth > 0 {
			col += tabWidth - col%tabWidth
		} else {
			col++
		}
	}
	return col
}

// spacesToTabStop returns how many spaces take the cursor from the visual
// column col to the next tab stop.
func spacesToTabStop(col, tabWidth int) int {
	if tabWidth <= 0 {
		return 1
	}
	return tabWidth - col%tabWidth
}

// indentUnit returns what one level of indentation is: a tab, or tabWidth
// spaces.
func indentUnit(insertSpaces bool, tabWidth int) string {
	if insertSpaces {
		return strings.Repeat(" ", max(tabWidth, 1))
	}
	return "\t"
}

// unindentLength returns how many characters (all ASCII, so also UTF-16
// code units) to remove from the start of line to take away one level of
// indentation: a tab, or up to tabWidth spaces, or the spaces before a tab.
func unindentLength(line string, tabWidth int) int {
	n := 0
	for n < len(line) && n < max(tabWidth, 1) {
		switch line[n] {
		case ' ':
			n++
			continue
		case '\t':
			return n + 1
		}
		break
	}
	return n
}

// positionText is the status bar's line and column, with what is selected.
// lines is how many lines the selection touches and chars how many
// characters it holds (0 for none).
func positionText(line, col, chars, lines int) string {
	s := fmt.Sprintf("Ln %d, Col %d", line, col)
	switch {
	case chars == 0:
	case lines > 1:
		s += fmt.Sprintf(" (%s, %s selected)", plural(lines, "line", "lines"), plural(chars, "char", "chars"))
	default:
		s += fmt.Sprintf(" (%s selected)", plural(chars, "char", "chars"))
	}
	return s
}

// plural returns n and the word for one or for many, as n is.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// matchCount is the find bar's count of a search's matches: n of them,
// more than that if more, and which is selected (from 1; 0 for none).
func matchCount(current, n int, more bool) string {
	plus := ""
	if more {
		plus = "+"
	}
	switch {
	case n == 0:
		return "No matches"
	case current > 0:
		return fmt.Sprintf("%d of %d%s", current, n, plus)
	case n == 1 && !more:
		return "1 match"
	}
	return fmt.Sprintf("%d%s matches", n, plus)
}

// countChars counts the characters of text Qt gives: runes, with its
// paragraph and line separators each one character, as a newline is.
func countChars(text string) int { return utf8.RuneCountInString(text) }

// plainText turns the text Qt holds (its raw text, which keeps
// non-breaking spaces where toPlainText would not) into the text of a file:
// paragraph separators and line separators become "\n".
func plainText(raw string) string {
	if !strings.ContainsAny(raw, "  ") {
		return raw
	}
	return strings.NewReplacer(" ", "\n", " ", "\n").Replace(raw)
}

// displayPath shortens a path in the home folder to one starting with ~.
func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}

// absPath makes a path given on the command line absolute.
func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// unescape turns the escapes typed in the find bar's fields into what they
// stand for: \t a tab, \n a line break and \\ a backslash. A backslash
// before anything else is left as it is.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escape is unescape's opposite, for text put in the find bar's field:
// unescape gives s back.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`).Replace(s)
}

// span is a stretch of text, from start up to end, in UTF-16 code units
// as Qt counts positions.
type span struct{ start, end int }

// findAll finds needle in text, as Qt's search does but also across line
// breaks, which Qt's can't. It returns the matches in order, none
// overlapping another.
func findAll(text, needle string, matchCase, wholeWords bool) []span {
	if needle == "" {
		return nil
	}
	// index finds the first match in s, in bytes.
	index := func(s string) (int, int) {
		i := strings.Index(s, needle)
		return i, i + len(needle)
	}
	switch {
	case matchCase:
	case asciiFolds(text, needle):
		// Letters are told apart from their other case only in ASCII, so
		// the search is made in lower case, where every character is as
		// long as it was: much quicker than the general one below.
		text, needle = asciiLower(text), asciiLower(needle)
	default:
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(needle))
		index = func(s string) (int, int) {
			if m := re.FindStringIndex(s); m != nil {
				return m[0], m[1]
			}
			return -1, -1
		}
	}
	var out []span
	units, counted := 0, 0 // text[:counted] is units code units long
	for from := 0; from <= len(text); {
		i, j := index(text[from:])
		if i < 0 {
			break
		}
		start, end := from+i, from+j
		if wholeWords && (wordBefore(text[:start]) || wordAfter(text[end:])) {
			_, size := utf8.DecodeRuneInString(text[start:])
			from = start + max(size, 1)
			continue
		}
		units += utf16Len(text[counted:start])
		s := span{start: units}
		units += utf16Len(text[start:end])
		s.end = units
		counted = end
		out = append(out, s)
		from = end
	}
	return out
}

// asciiFolds reports whether needle can be found in text whatever its case
// by putting both in ASCII's lower case: needle is ASCII, and text has
// neither of the characters beyond ASCII that are other cases of ASCII
// letters, the Kelvin sign (of k) and the long s.
func asciiFolds(text, needle string) bool {
	for i := 0; i < len(needle); i++ {
		if needle[i] >= utf8.RuneSelf {
			return false
		}
	}
	return !strings.Contains(text, "K") && !strings.Contains(text, "ſ")
}

// asciiLower returns s with its ASCII capitals in lower case, and so as
// long as it was.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// wordBefore reports whether s ends with a letter or a digit, and
// wordAfter whether it starts with one.
func wordBefore(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return s != "" && (unicode.IsLetter(r) || unicode.IsNumber(r))
}

func wordAfter(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return s != "" && (unicode.IsLetter(r) || unicode.IsNumber(r))
}

// utf16Len returns how many UTF-16 code units s is.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i]&0xC0 != 0x80 {
			n++
		}
		if s[i] >= 0xF0 {
			n++
		}
	}
	return n
}
