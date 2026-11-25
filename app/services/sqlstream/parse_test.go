package sqlstream

import (
	"reflect"
	"strings"
	"testing"
)

func readAll(t *testing.T, packets ...string) ([]Column, [][]any, []int, error) {
	t.Helper()
	var r tsvReader
	var cols []Column
	var rows [][]any
	var sizes []int
	onCols := func(c []Column) error { cols = c; return nil }
	onRow := func(v []any, n int) error { rows = append(rows, v); sizes = append(sizes, n); return nil }
	for _, p := range packets {
		if err := r.Feed(p, onCols, onRow); err != nil {
			return cols, rows, sizes, err
		}
	}
	return cols, rows, sizes, r.Finish(onCols, onRow)
}

func TestTSVReadsAsTheJobPathDoes(t *testing.T) {
	// One answer split across packets mid-line, as framing may cut it.
	body := "n\ts\tarr\tnote\n" +
		"UInt64\tString\tArray(String)\tNullable(String)\n" +
		"18446744073709551615\ttab\\there\t['a\\'b','c\\\\d']\t\\N\n" +
		"1\t\\\\N\t[]\tline\\nbreak\n" +
		"2\tcaf\xc3\xa9 \xff\xfe end\t['x']\t\n"
	cols, rows, sizes, err := readAll(t, body[:17], body[17:60], body[60:61], body[61:])
	if err != nil {
		t.Fatal(err)
	}
	wantCols := []Column{{"n", "UInt64"}, {"s", "String"}, {"arr", "Array(String)"}, {"note", "Nullable(String)"}}
	if !reflect.DeepEqual(cols, wantCols) {
		t.Fatalf("columns %+v", cols)
	}
	want := [][]any{
		{"18446744073709551615", "tab\there", `['a\'b','c\\d']`, nil}, // an array as written; \N alone is NULL
		{"1", `\N`, "[]", "line\nbreak"},                              // a string reading \N is not NULL
		{"2", "café �� end", "['x']", ""},                             // each invalid byte one U+FFFD; empty is not NULL
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows\n got %#v\nwant %#v", rows, want)
	}
	if sizes[0] != len("18446744073709551615\ttab\\there\t['a\\'b','c\\\\d']\t\\N")+1 {
		t.Fatalf("a row's bytes are its line's: %v", sizes)
	}
}

func TestTSVEdges(t *testing.T) {
	// An EXPLAIN's blank line is one empty field; a lone trailing backslash stays.
	_, rows, _, err := readAll(t, "explain\nString\nExpression\n\nends\\\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]any{{"Expression"}, {""}, {`ends\`}}) {
		t.Fatalf("rows %#v", rows)
	}
	// A composite inside Nullable or LowCardinality is still as written.
	cols, rows, _, _ := readAll(t, "m\nNullable(Map(String, String))\n{'k':'v\\tw'}\n")
	if cols[0].Type != "Nullable(Map(String, String))" || rows[0][0] != `{'k':'v\tw'}` {
		t.Fatalf("%v %#v", cols, rows)
	}
	// No rows at all: columns only.
	cols, rows, _, _ = readAll(t, "a\tb\nUInt8\tUInt8\n")
	if len(cols) != 2 || len(rows) != 0 {
		t.Fatalf("%v %v", cols, rows)
	}
	// A row of another width is not ClickHouse's output.
	if _, _, _, err := readAll(t, "a\tb\nUInt8\tUInt8\n1\n"); err != errWidth {
		t.Fatalf("got %v", err)
	}
}

func TestScrubMaximalSubparts(t *testing.T) {
	cases := map[string]string{
		"ok":                 "ok",
		"\xff\xfe":           "��",
		"a\xe2\x82b":         "a�b", // a cut 3-byte sequence is one
		"\xe2\x82\xac":       "€",
		"\xed\xa0\x80":       "���", // a surrogate: ED then two bytes outside A0..BF's rule
		"\xf0\x9f\x98":       "�",
		"\xc0\xaf":           "��",
		"x\xf4\x90\x80\x80y": "x����y",
	}
	for in, want := range cases {
		if got := scrub([]byte(in)); got != want {
			t.Errorf("scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEngineErrors(t *testing.T) {
	sql := "SELECT café, evnt FROM events WHERE x = 1"
	syntax := parseEngineError("Code: 62. DB::Exception: Syntax error: failed at position 14 (evnt): evnt FROM. Expected one of: ... (SYNTAX_ERROR) (version 26.8.45.6 (official build))", sql)
	if syntax.Code != "62" || strings.Contains(syntax.Message, "SYNTAX_ERROR") || strings.Contains(syntax.Message, "version") {
		t.Fatalf("%+v", syntax)
	}
	// Byte 14 is after "SELECT café, " (é is two bytes): character 13.
	if syntax.Position == nil || *syntax.Position != 13 {
		t.Fatalf("position %v", syntax.Position)
	}
	unknown := parseEngineError("Code: 47. DB::Exception: Unknown expression identifier `evnt` in scope SELECT ... (UNKNOWN_IDENTIFIER) (version 26.8.45.6 (official build))", sql)
	// "evnt" starts at character 14, as the job path's regexp finds it.
	if unknown.Code != "47" || unknown.Position == nil || *unknown.Position != 14 {
		t.Fatalf("%+v %v", unknown, *unknown.Position)
	}
	backticked := parseEngineError("Code: 47. DB::Exception: Unknown expression identifier `x y` in scope", "SELECT `x y` FROM t")
	if backticked.Position == nil || *backticked.Position != 8 {
		t.Fatalf("backticked position %v", backticked.Position)
	}
	notCH := parseEngineError("proxy said no", sql)
	if notCH.Code != "" || notCH.Message != "proxy said no" || errorClass(notCH.Code) != "internal" {
		t.Fatalf("%+v", notCH)
	}
	long := parseEngineError("Code: 1. DB::Exception: "+strings.Repeat("x", 3000), sql)
	if n := len([]rune(long.Message)); n != maxMessage || !strings.HasSuffix(long.Message, "...") {
		t.Fatalf("message of %d", n)
	}
	for code, class := range map[string]string{"62": "syntax", "159": "timeout", "241": "memory", "516": "login_failed", "47": "engine"} {
		if got := errorClass(code); got != class {
			t.Errorf("errorClass(%s) = %s", code, got)
		}
	}
}

func TestChURLCarriesTheSettingsTheProfileAllows(t *testing.T) {
	u := chURL(chRequest{BaseURL: "http://127.0.0.1:8123/", Database: "analytics", QueryID: "sqr_0123456789abcdef0123_1", Limit: 10001, Secs: 3600})
	for _, want := range []string{"framing_output_format=JSONEachPacketString", "send_profile_events=0", "http_wait_end_of_query=0",
		"limit=10001", "max_execution_time=3600", "query_id=sqr_0123456789abcdef0123_1", "database=analytics", "default_format=TSVWithNamesAndTypes"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s lacks %s", u, want)
		}
	}
	if !strings.HasPrefix(u, "http://127.0.0.1:8123/?") {
		t.Errorf("url %s", u)
	}
}

func TestRawStringKeepsBytes(t *testing.T) {
	cases := map[string]string{
		`"plain"`:                   "plain",
		"\"caf\xc3\xa9 \xe2\x82x\"": "café \xe2\x82x", // a bad sequence stays bytes, for scrub
		`"a\tb\nc\\d\"e\/f\u0000g"`: "a\tb\nc\\d\"e/f\x00g",
		`"😀 é"`:                     "\U0001F600 é",
		`"lone \ud83d here"`:        "lone � here",
		`"\b\f\r"`:                  "\b\f\r",
	}
	for in, want := range cases {
		got, err := rawString([]byte(in))
		if err != nil || got != want {
			t.Errorf("rawString(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`plain`, `"ends\"`, `"\x"`, `"\u12"`} {
		if _, err := rawString([]byte(bad)); err == nil {
			t.Errorf("rawString(%q) read", bad)
		}
	}
	// Through the reader: one U+FFFD for the cut sequence, as the job path gives.
	_, rows, _, _ := readAll(t, "s\nString\n"+"\xe2\x82x\n")
	if rows[0][0] != "�x" {
		t.Fatalf("%q", rows[0][0])
	}
}
