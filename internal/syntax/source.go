package syntax

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/irie-people/riddim/internal/diag"
)

// A Pos is an offset into a FileSet. Each source file occupies its own range,
// so a Pos alone identifies a file and a byte offset in it. NoPos is zero.
type Pos int

// NoPos is the zero position.
const NoPos Pos = 0

// A Span is the range [Start, End) of a node or token.
type Span struct{ Start, End Pos }

// A Source is one .rdm file.
type Source struct {
	Name  string // the path shown in diagnostics
	Base  Pos    // position of the first byte
	Src   []byte
	lines []int // byte offsets of line starts
}

// A FileSet holds the sources of one program.
type FileSet struct {
	files []*Source
	next  Pos
}

// NewFileSet returns an empty file set.
func NewFileSet() *FileSet { return &FileSet{next: 1} }

// Add registers a source file and returns it.
func (fs *FileSet) Add(name string, src []byte) *Source {
	s := &Source{Name: name, Base: fs.next, Src: src, lines: []int{0}}
	for i, c := range src {
		if c == '\n' {
			s.lines = append(s.lines, i+1)
		}
	}
	fs.files = append(fs.files, s)
	fs.next += Pos(len(src) + 1)
	return s
}

// Files returns the sources in the order they were added.
func (fs *FileSet) Files() []*Source { return fs.files }

// Source returns the file containing p.
func (fs *FileSet) Source(p Pos) *Source {
	i := sort.Search(len(fs.files), func(i int) bool { return fs.files[i].Base > p }) - 1
	if i < 0 {
		i = 0
	}
	return fs.files[i]
}

// Pos returns the position of byte offset off.
func (s *Source) Pos(off int) Pos { return s.Base + Pos(off) }

// Offset returns the byte offset of p in s.
func (s *Source) Offset(p Pos) int {
	off := int(p - s.Base)
	if off < 0 {
		off = 0
	}
	if off > len(s.Src) {
		off = len(s.Src)
	}
	return off
}

// LineCol returns the 1-based line and column of p; columns count code points.
func (s *Source) LineCol(p Pos) (line, col int) {
	off := s.Offset(p)
	i := sort.Search(len(s.lines), func(i int) bool { return s.lines[i] > off }) - 1
	return i + 1, utf8.RuneCount(s.Src[s.lines[i]:off]) + 1
}

// Line returns the text of a 1-based line without its line ending.
func (s *Source) Line(n int) (string, bool) {
	if n < 1 || n > len(s.lines) {
		return "", false
	}
	start := s.lines[n-1]
	end := len(s.Src)
	if n < len(s.lines) {
		end = s.lines[n] - 1
	}
	if end > start && s.Src[end-1] == '\r' {
		end--
	}
	return string(s.Src[start:end]), true
}

// LineStart returns the position of the first byte of a 1-based line.
func (s *Source) LineStart(n int) Pos { return s.Pos(s.lines[n-1]) }

// Diag makes a diagnostic for a span in this file set.
func (fs *FileSet) Diag(code string, sp Span, format string, args ...any) diag.Diagnostic {
	s := fs.Source(sp.Start)
	l1, c1 := s.LineCol(sp.Start)
	end := sp.End
	if end < sp.Start {
		end = sp.Start
	}
	l2, c2 := s.LineCol(end)
	return diag.Diagnostic{
		Code: code, Severity: diag.SeverityOf(code), Message: fmt.Sprintf(format, args...),
		File: s.Name, Line: l1, Col: c1, EndLine: l2, EndCol: c2,
		Notes: []string{}, Fixes: []diag.Fix{},
	}
}

// Edit makes an edit that replaces the text of sp with text.
func (fs *FileSet) Edit(sp Span, text string) diag.Edit {
	s := fs.Source(sp.Start)
	l1, c1 := s.LineCol(sp.Start)
	l2, c2 := s.LineCol(sp.End)
	return diag.Edit{File: s.Name, Line: l1, Col: c1, EndLine: l2, EndCol: c2, Text: text}
}

// LineSource returns a diag.LineSource over the files of fs.
func (fs *FileSet) LineSource() diag.LineSource {
	return func(file string, line int) (string, bool) {
		for _, s := range fs.files {
			if s.Name == file {
				return s.Line(line)
			}
		}
		return "", false
	}
}
