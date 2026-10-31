package check

import (
	"sort"
	"strings"
	"unicode"

	"github.com/irie-people/riddim/internal/diag"
	"github.com/irie-people/riddim/internal/syntax"
)

// A naming style (DESIGN §4.1).
type nameStyle int

const (
	upperCamel nameStyle = iota // types, variants, type parameters
	snake                       // functions, variables, fields, methods
	screaming                   // constants
)

var styleNames = map[nameStyle]string{upperCamel: "UpperCamelCase", snake: "snake_case", screaming: "SCREAMING_SNAKE_CASE"}

var styleKinds = map[nameStyle]string{upperCamel: "types, variants and type parameters", snake: "functions, variables, fields and methods", screaming: "constants"}

type styled struct {
	id    *syntax.Ident
	style nameStyle
	refs  *[]syntax.Span
}

// style records a declared name to check against its naming style once all
// references are known, so the rename fix can change every one of them.
func (c *checker) style(id *syntax.Ident, s nameStyle, refs *[]syntax.Span) {
	c.styles = append(c.styles, styled{id, s, refs})
}

func validStyle(name string, s nameStyle) bool {
	switch s {
	case upperCamel:
		r := []rune(name)
		return len(r) > 0 && unicode.IsUpper(r[0]) && !strings.Contains(name, "_")
	case snake:
		body := strings.TrimLeft(name, "_")
		if body == "" || strings.HasSuffix(body, "_") || strings.Contains(body, "__") {
			return false
		}
		for i, r := range body {
			if unicode.IsUpper(r) || i == 0 && unicode.IsDigit(r) {
				return false
			}
		}
		return true
	case screaming:
		if name == "" || strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") || strings.Contains(name, "__") {
			return false
		}
		for i, r := range name {
			if unicode.IsLower(r) || i == 0 && unicode.IsDigit(r) {
				return false
			}
		}
		return true
	}
	return true
}

// words splits a name into words at underscores and case changes:
// "maxRetries" and "MaxRetries" give ["max", "Retries"], "HTTPServer" gives
// ["HTTP", "Server"].
func words(name string) []string {
	var out []string
	for _, part := range strings.Split(name, "_") {
		r := []rune(part)
		start := 0
		for i := 1; i < len(r); i++ {
			lowerToUpper := unicode.IsUpper(r[i]) && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]))
			acronymEnd := unicode.IsUpper(r[i]) && unicode.IsUpper(r[i-1]) && i+1 < len(r) && unicode.IsLower(r[i+1])
			if lowerToUpper || acronymEnd {
				out = append(out, string(r[start:i]))
				start = i
			}
		}
		if start < len(r) {
			out = append(out, string(r[start:]))
		}
	}
	return out
}

// convert renames name into style s; it returns "" when that is impossible.
func convert(name string, s nameStyle) string {
	lead := ""
	if s == snake {
		lead = name[:len(name)-len(strings.TrimLeft(name, "_"))]
	}
	ws := words(name)
	if len(ws) == 0 {
		return ""
	}
	var out string
	switch s {
	case upperCamel:
		var b strings.Builder
		for _, w := range ws {
			r := []rune(strings.ToLower(w))
			r[0] = unicode.ToUpper(r[0])
			b.WriteString(string(r))
		}
		out = b.String()
	case snake:
		out = lead + strings.ToLower(strings.Join(ws, "_"))
	case screaming:
		out = strings.ToUpper(strings.Join(ws, "_"))
	}
	if !validStyle(out, s) {
		return ""
	}
	return out
}

// finishNames reports names that break their naming style (R0203), with a
// fix that renames the declaration and every reference.
func (c *checker) finishNames() {
	for _, st := range c.styles {
		name := st.id.Name
		if validStyle(name, st.style) {
			continue
		}
		d := c.errorf("R0203", st.id.Span(), "`%s` should be %s: %s are %s", name, styleNames[st.style], styleKinds[st.style], styleNames[st.style])
		to := convert(name, st.style)
		if to == "" {
			continue
		}
		d.Message += "; rename it `" + to + "`"
		spans := append([]syntax.Span{st.id.Span()}, *st.refs...)
		sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
		fix := diag.Fix{Title: "rename `" + name + "` to `" + to + "`"}
		var last syntax.Span
		for i, sp := range spans {
			if i > 0 && sp == last {
				continue
			}
			last = sp
			fix.Edits = append(fix.Edits, c.fset.Edit(sp, to))
		}
		d.Fixes = append(d.Fixes, fix)
	}
}

// closest returns up to three candidates within a small edit distance of
// name, nearest first.
func closest(name string, candidates []string) []string {
	type scored struct {
		name string
		d    int
	}
	limit := max(1, len([]rune(name))/3)
	var found []scored
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == name || seen[cand] || cand == "" {
			continue
		}
		seen[cand] = true
		d := editDistance(strings.ToLower(name), strings.ToLower(cand))
		if d <= limit {
			found = append(found, scored{cand, d})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].d != found[j].d {
			return found[i].d < found[j].d
		}
		return found[i].name < found[j].name
	})
	var out []string
	for i := 0; i < len(found) && i < 3; i++ {
		out = append(out, found[i].name)
	}
	return out
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
