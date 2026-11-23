package textfile

import (
	"fmt"
	"strings"
	"testing"
)

// benchText is a text of 13 MB with a character outside ASCII (but in
// Latin-1) on every line, whose lines end with "\n".
var benchText = func() string {
	var b strings.Builder
	for i := range 200000 {
		fmt.Fprintf(&b, "%06d\tThe quick brown fox jumps over the lazy dog, encore une fois é.\n", i)
	}
	return b.String()
}()

// benchFiles are the text as files of each kind that is read and written
// differently.
var benchFiles = []File{
	{Encoding: UTF8, LineEnding: LF},
	{Encoding: UTF8, LineEnding: CRLF},
	{Encoding: UTF16LE, LineEnding: LF},
	{Encoding: UTF32BE, LineEnding: LF},
	{Encoding: Latin1, LineEnding: CRLF},
}

func benchName(f File) string { return fmt.Sprintf("%s/%s", f.Encoding, f.LineEnding) }

// BenchmarkParse reads a file: its encoding and line endings are found and
// its text decoded.
func BenchmarkParse(b *testing.B) {
	for _, f := range benchFiles {
		f.Text = benchText
		data, err := f.Bytes()
		if err != nil {
			b.Fatal(err)
		}
		b.Run(benchName(f), func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				got := Parse(data)
				if got.Encoding != f.Encoding || got.LineEnding != f.LineEnding || len(got.Text) != len(benchText) {
					b.Fatalf("read as %s, %s, %d bytes of text", got.Encoding, got.LineEnding, len(got.Text))
				}
			}
		})
	}
}

// BenchmarkBytes encodes a file's text to write it.
func BenchmarkBytes(b *testing.B) {
	for _, f := range benchFiles {
		f.Text = benchText
		b.Run(benchName(f), func(b *testing.B) {
			b.SetBytes(int64(len(benchText)))
			for b.Loop() {
				if _, err := f.Bytes(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLooksBinary looks at the start of a file for what text never has.
func BenchmarkLooksBinary(b *testing.B) {
	data := []byte(benchText)
	for b.Loop() {
		if LooksBinary(data, UTF8) {
			b.Fatal("the text was taken for binary")
		}
	}
}
