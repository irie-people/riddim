// Package diag defines Riddim's diagnostics: stable codes, exact spans and
// mechanical fixes, rendered as JSON (DESIGN §7.1) or as text.
package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// Severities.
const (
	Error   = "error"
	Warning = "warning"
)

// An Edit replaces the text between (Line, Col) and (EndLine, EndCol) with
// Text. Lines and columns are 1-based, columns count code points and EndCol is
// exclusive. An empty range inserts.
type Edit struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"end_line"`
	EndCol  int    `json:"end_col"`
	Text    string `json:"text"`
}

// A Fix is a set of edits that, applied together, resolve a diagnostic.
type Fix struct {
	Title string `json:"title"`
	Edits []Edit `json:"edits"`
}

// A Diagnostic is one error or warning. Field order matches DESIGN §7.1.
type Diagnostic struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Col      int      `json:"col"`
	EndLine  int      `json:"end_line"`
	EndCol   int      `json:"end_col"`
	Notes    []string `json:"notes"`
	Fixes    []Fix    `json:"fixes"`
}

// SeverityOf returns the severity a code always has: W codes are warnings.
func SeverityOf(code string) string {
	if strings.HasPrefix(code, "W") {
		return Warning
	}
	return Error
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Sort orders diagnostics by file, position and code.
func Sort(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

// normalize makes Notes, Fixes and Edits non-nil, so JSON always has arrays.
func normalize(ds []Diagnostic) []Diagnostic {
	out := make([]Diagnostic, len(ds))
	for i, d := range ds {
		if d.Notes == nil {
			d.Notes = []string{}
		}
		fixes := make([]Fix, len(d.Fixes))
		for j, f := range d.Fixes {
			if f.Edits == nil {
				f.Edits = []Edit{}
			}
			fixes[j] = f
		}
		d.Fixes = fixes
		out[i] = d
	}
	return out
}

// WriteJSON writes ds as a JSON array, one diagnostic per line; `[]` when empty.
// Strings are written without HTML escaping, so a fix's `=>` or `<` reaches
// an agent exactly as written.
func WriteJSON(w io.Writer, ds []Diagnostic) error {
	ds = normalize(ds)
	if len(ds) == 0 {
		_, err := io.WriteString(w, "[]\n")
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	b.WriteString("[")
	for i, d := range ds {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n")
		if err := enc.Encode(d); err != nil {
			return err
		}
		b.Truncate(b.Len() - 1) // Encode ends each value with a newline
	}
	b.WriteString("\n]\n")
	_, err := w.Write(b.Bytes())
	return err
}

// LineSource returns the text of a 1-based line of a file, without its
// newline, and false when it is not available.
type LineSource func(file string, line int) (string, bool)

// WriteText renders ds as text:
//
//	main.rdm:12:5: error[R0401]: message
//	   |
//	12 |     match r {
//	   |     ^^^^^^^
//	   = note: ...
//	   = fix: title: `text`
func WriteText(w io.Writer, ds []Diagnostic, src LineSource) error {
	var b strings.Builder
	for _, d := range ds {
		renderText(&b, d, src)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func renderText(b *strings.Builder, d Diagnostic, src LineSource) {
	fmt.Fprintf(b, "%s:%d:%d: %s[%s]: %s\n", d.File, d.Line, d.Col, d.Severity, d.Code, d.Message)
	num := fmt.Sprint(d.Line)
	gutter := strings.Repeat(" ", len(num)+1)
	if text, ok := src(d.File, d.Line); ok {
		fmt.Fprintf(b, "%s|\n", gutter)
		fmt.Fprintf(b, "%s |%s\n", num, prefixSpace(text))
		fmt.Fprintf(b, "%s|%s\n", gutter, prefixSpace(carets(text, d)))
	}
	for _, n := range d.Notes {
		fmt.Fprintf(b, "%s= note: %s\n", gutter, n)
	}
	for _, f := range d.Fixes {
		var parts []string
		seen := map[string]bool{}
		for _, e := range f.Edits {
			for _, l := range strings.Split(e.Text, "\n") {
				l = strings.TrimPrefix(strings.TrimSpace(l), ", ")
				if l != "" && !seen[l] {
					seen[l] = true
					parts = append(parts, "`"+l+"`")
				}
			}
		}
		if len(parts) == 0 {
			fmt.Fprintf(b, "%s= fix: %s\n", gutter, f.Title)
		} else {
			fmt.Fprintf(b, "%s= fix: %s: %s\n", gutter, f.Title, strings.Join(parts, " "))
		}
	}
}

func prefixSpace(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

// carets underlines the diagnostic's span on its first line. Tabs before the
// span are kept so the carets line up. A span that runs past the line is
// underlined to the line's end, leaving out trailing blanks and a trailing `{`.
func carets(line string, d Diagnostic) string {
	runes := []rune(line)
	start := d.Col - 1
	if start > len(runes) {
		start = len(runes)
	}
	end := d.EndCol - 1
	if d.EndLine != d.Line || end > len(runes) {
		trimmed := []rune(strings.TrimRight(line, " \t"))
		if len(trimmed) > start+1 && trimmed[len(trimmed)-1] == '{' {
			trimmed = []rune(strings.TrimRight(string(trimmed[:len(trimmed)-1]), " \t"))
		}
		end = len(trimmed)
	}
	if end <= start {
		end = start + 1
	}
	var b strings.Builder
	for _, r := range runes[:start] {
		if r == '\t' {
			b.WriteRune('\t')
		} else {
			b.WriteRune(' ')
		}
	}
	b.WriteString(strings.Repeat("^", end-start))
	return b.String()
}

// Apply applies edits to a file's text and returns the result. Edits must not
// overlap; they are applied from last to first.
func Apply(text string, edits []Edit) (string, error) {
	type span struct{ start, end int }
	lines := lineOffsets(text)
	offset := func(line, col int) (int, error) {
		if line < 1 || line > len(lines) {
			return 0, fmt.Errorf("line %d out of range", line)
		}
		off := lines[line-1]
		for i := 1; i < col; i++ {
			if off >= len(text) || text[off] == '\n' {
				return 0, fmt.Errorf("column %d out of range on line %d", col, line)
			}
			_, size := utf8.DecodeRuneInString(text[off:])
			off += size
		}
		return off, nil
	}
	spans := make([]span, len(edits))
	for i, e := range edits {
		s, err := offset(e.Line, e.Col)
		if err != nil {
			return "", err
		}
		t, err := offset(e.EndLine, e.EndCol)
		if err != nil {
			return "", err
		}
		spans[i] = span{s, t}
	}
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return spans[order[a]].start > spans[order[b]].start })
	for _, i := range order {
		text = text[:spans[i].start] + edits[i].Text + text[spans[i].end:]
	}
	return text, nil
}

func lineOffsets(text string) []int {
	offs := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}
