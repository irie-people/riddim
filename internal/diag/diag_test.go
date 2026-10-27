package diag

import (
	"bytes"
	"strings"
	"testing"
)

func sample() Diagnostic {
	return Diagnostic{
		Code: "R0401", Severity: Error,
		Message: "match on Result<Option<Int>, Error> is missing 1 case: Ok(None)",
		File:    "main.rdm", Line: 12, Col: 5, EndLine: 16, EndCol: 6,
		Notes: []string{"a guarded arm does not count toward exhaustiveness"},
		Fixes: []Fix{{Title: "add the missing arm", Edits: []Edit{{
			File: "main.rdm", Line: 15, Col: 1, EndLine: 15, EndCol: 1,
			Text: "        Ok(None) => todo(),\n"}}}},
	}
}

func TestJSONEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := WriteJSON(&b, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != "[]\n" {
		t.Errorf("got %q, want []", b.String())
	}
}

func TestJSONArraysAlwaysPresent(t *testing.T) {
	var b bytes.Buffer
	d := Diagnostic{Code: "W0003", Severity: Warning, Message: "m", File: "a.rdm", Line: 1, Col: 1, EndLine: 1, EndCol: 2,
		Fixes: []Fix{{Title: "t"}}}
	if err := WriteJSON(&b, []Diagnostic{d}); err != nil {
		t.Fatal(err)
	}
	want := `[
{"code":"W0003","severity":"warning","message":"m","file":"a.rdm","line":1,"col":1,"end_line":1,"end_col":2,"notes":[],"fixes":[{"title":"t","edits":[]}]}
]
`
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestText(t *testing.T) {
	src := func(file string, line int) (string, bool) {
		if line == 12 {
			return "    match r {", true
		}
		return "", false
	}
	var b bytes.Buffer
	if err := WriteText(&b, []Diagnostic{sample()}, src); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"main.rdm:12:5: error[R0401]: match on Result<Option<Int>, Error> is missing 1 case: Ok(None)",
		"   |",
		"12 |     match r {",
		"   |     ^^^^^^^",
		"   = note: a guarded arm does not count toward exhaustiveness",
		"   = fix: add the missing arm: `Ok(None) => todo(),`",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestTextTabsAndSingleLine(t *testing.T) {
	src := func(string, int) (string, bool) { return "\tlet é = foo", true }
	d := Diagnostic{Code: "R0201", Severity: Error, Message: "unknown name `foo`", File: "a.rdm",
		Line: 3, Col: 10, EndLine: 3, EndCol: 13}
	var b bytes.Buffer
	WriteText(&b, []Diagnostic{d}, src)
	if !strings.Contains(b.String(), "\n  | \t        ^^^\n") {
		t.Errorf("carets misplaced:\n%s", b.String())
	}
}

func TestApply(t *testing.T) {
	text := "match r {\n    Ok(x) => x,\n}\n"
	got, err := Apply(text, []Edit{
		{Line: 3, Col: 1, EndLine: 3, EndCol: 1, Text: "    Err(_) => todo(),\n"},
		{Line: 1, Col: 7, EndLine: 1, EndCol: 8, Text: "res"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "match res {\n    Ok(x) => x,\n    Err(_) => todo(),\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
