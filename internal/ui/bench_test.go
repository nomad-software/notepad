package ui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qutetest"
)

// The benchmarks time the editor on a large file, to compare one change of
// the code with another: make bench, before and after, and benchstat on the
// two. Those of the window run on Qt's offscreen platform, as its tests do.

// largeText is a text of 200,000 lines, 13 MB: every line has a number, a
// tab and a sentence.
var largeText = func() string {
	var b strings.Builder
	for i := range 200000 {
		fmt.Fprintf(&b, "%06d\tThe quick brown fox jumps over the lazy dog, again and again.\n", i)
	}
	return b.String()
}()

// benchWindow runs f in a window with largeText open in doc, from a file.
func benchWindow(b *testing.B, f func(w *Window, d *Doc)) {
	path := writeFile(b, "big.txt", []byte(largeText))
	qutetest.Run(b, func() {
		w := newTestWindow(b, nil, path)
		qutetest.Wait(1)
		b.SetBytes(int64(len(largeText)))
		b.ResetTimer()
		f(w, w.current())
		b.StopTimer()
	})
}

// collect frees the Qt values Go no longer holds (cursors, above all),
// which wait for Go's garbage collector. The document moves every cursor
// there is at each change, so those left over from one run would slow the
// next, by how long ago the collector happened to run.
func collect() {
	for range 2 {
		runtime.GC()
		qutetest.Wait(10) // the GUI thread frees what the collector found
	}
}

// collectBefore runs collect, untimed, before a benchmark's run i if that
// is one in every: runs too quick to collect before each.
func collectBefore(b *testing.B, i, every int) {
	if i%every == 0 {
		b.StopTimer()
		collect()
		b.StartTimer()
	}
}

// BenchmarkOpen opens the file in a tab, which is closed again untimed.
func BenchmarkOpen(b *testing.B) {
	path := writeFile(b, "other.txt", []byte(largeText))
	benchWindow(b, func(w *Window, _ *Doc) {
		for b.Loop() {
			w.openFiles([]string{path})
			qutetest.Wait(1)
			b.StopTimer()
			w.closeDoc(w.current())
			collect()
			b.StartTimer()
		}
	})
}

// BenchmarkType types a letter at the start of the text and takes it out
// again, so that the line is as long each time.
func BenchmarkType(b *testing.B) {
	benchWindow(b, func(_ *Window, d *Doc) {
		b.SetBytes(0)
		for i := 0; b.Loop(); i++ {
			collectBefore(b, i, 100)
			qutetest.Key(d.edit, qtcore.Key_A, qtcore.NoModifier)
			qutetest.Key(d.edit, qtcore.Key_Backspace, qtcore.NoModifier)
		}
	})
}

// BenchmarkSave writes the text to its file.
func BenchmarkSave(b *testing.B) {
	benchWindow(b, func(w *Window, d *Doc) {
		for b.Loop() {
			if !w.save(d) {
				b.Fatal("the file was not saved")
			}
		}
	})
}

// benchFind searches for what is typed in the find field as the bar does
// at each key: the next match is selected and the matches highlighted.
func benchFind(b *testing.B, typed, count string) {
	benchWindow(b, func(w *Window, d *Doc) {
		w.run("find", false)
		w.find.find.SetText(typed)
		for i := 0; b.Loop(); i++ {
			collectBefore(b, i, 1)
			w.find.spanKey = spanSearch{} // found again, not remembered
			d.goTo(0, 0)
			w.find.incremental()
		}
		if got := w.find.count.Text(); got != count {
			b.Fatalf("count %q, want %q", got, count)
		}
	})
}

func BenchmarkFind(b *testing.B)            { benchFind(b, "fox", "1 of 2000+") }
func BenchmarkFindNoMatch(b *testing.B)     { benchFind(b, "wolf", "No matches") }
func BenchmarkFindAcrossLines(b *testing.B) { benchFind(b, `again.\n000`, "1 of 999") }

// BenchmarkFindNext goes from one match to the next.
func BenchmarkFindNext(b *testing.B) {
	benchWindow(b, func(w *Window, _ *Doc) {
		b.SetBytes(0)
		w.run("find", false)
		w.find.find.SetText("fox")
		w.find.highlight()
		for i := 0; b.Loop(); i++ {
			collectBefore(b, i, 100)
			w.find.next(true)
		}
	})
}

// benchReplaceAll replaces every match, n of them, which is undone untimed.
func benchReplaceAll(b *testing.B, find, with string, n int) {
	benchWindow(b, func(w *Window, d *Doc) {
		w.run("replace", false)
		w.find.find.SetText(find)
		w.find.replace.SetText(with)
		before := d.edit.Document().CharacterCount()
		for b.Loop() {
			w.find.replaceAll()
			b.StopTimer()
			if got := d.edit.Document().CharacterCount() - before; got != n {
				b.Fatalf("the text grew by %d characters, not %d", got, n)
			}
			d.edit.Undo()
			collect()
			b.StartTimer()
		}
	})
}

// Each match is a character longer once replaced.
func BenchmarkReplaceAll(b *testing.B) { benchReplaceAll(b, `000\tThe`, `000\tThey`, 200) }
func BenchmarkReplaceAllAcrossLines(b *testing.B) {
	benchReplaceAll(b, `again.\n000`, `again.\n\t000`, 999)
}

// BenchmarkFindAll searches the text itself, as a search across lines does.
func BenchmarkFindAll(b *testing.B) {
	for _, tt := range []struct {
		name             string
		needle           string
		matchCase, words bool
		want             int
	}{
		{"MatchCase", "again.\n000", true, false, 999},
		{"AnyCase", "AGAIN.\n000", false, false, 999},
		{"AnyCaseBeyondASCII", "AGAIN.\n000É", false, false, 0},
		{"WholeWords", "lazy dog", true, true, 200000},
		{"EveryLine", "\n", true, false, 200000},
	} {
		b.Run(tt.name, func(b *testing.B) {
			b.SetBytes(int64(len(largeText)))
			for b.Loop() {
				if got := len(findAll(largeText, tt.needle, tt.matchCase, tt.words)); got != tt.want {
					b.Fatalf("%d matches, want %d", got, tt.want)
				}
			}
		})
	}
}

// BenchmarkPlainText turns Qt's text, its lines ended by paragraph
// separators, into a file's.
func BenchmarkPlainText(b *testing.B) {
	raw := strings.ReplaceAll(largeText, "\n", " ")
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if len(plainText(raw)) == 0 {
			b.Fatal("no text")
		}
	}
}

// BenchmarkVisualColumn finds the column of the end of a long line with
// tabs in it, as the status bar does when the cursor moves.
func BenchmarkVisualColumn(b *testing.B) {
	line := strings.Repeat("ab\tcdé\t", 500)
	for b.Loop() {
		visualColumn(line, len(line), 4)
	}
}
