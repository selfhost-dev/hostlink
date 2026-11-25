package sqlstream

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ClickHouse's TSVWithNamesAndTypes read as the selfhost backend's job path reads it
// (app/services/sql_editor/clickhouse_run_output.rb), so a value reads the same whichever path
// it came by: each value is ClickHouse's own text, \N alone is NULL (in rows, not in names), an
// empty line is one empty field, TSV's escapes are undone except in an array, a tuple, a map or
// a nested column (left as written), and bytes that are not UTF-8 become U+FFFD.

var compositeType = regexp.MustCompile(`^(?:(?:Nullable|LowCardinality)\()*(?:Array|Tuple|Map|Nested)\(`)

var tsvEscapes = map[rune]rune{'t': '\t', 'n': '\n', 'r': '\r', '0': 0, 'b': '\b', 'f': '\f', '\\': '\\', '\'': '\''}

// errWidth is a row whose width is not the header's: never ClickHouse's own output.
var errWidth = errors.New("a row of another width")

// Column is a result's column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// tsvReader takes a response's data packets in order and gives back the columns, then rows. A
// packet need not end on a line: what is left waits for the next one.
type tsvReader struct {
	partial   []byte
	columns   []Column
	names     []string
	asWritten []bool
	lines     int // header lines read: 0, 1 or 2
}

// Feed reads a packet; columns is called once, when the header is complete, and row for each row
// with its fields and the bytes of its line (the job path's byte cap counts ClickHouse's text).
func (r *tsvReader) Feed(text string, columns func([]Column) error, row func([]any, int) error) error {
	data := []byte(text)
	if len(r.partial) > 0 {
		data = append(r.partial, data...)
		r.partial = nil
	}
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if len(data) > 0 {
				r.partial = append([]byte(nil), data...)
			}
			return nil
		}
		if err := r.line(data[:i], columns, row); err != nil {
			return err
		}
		data = data[i+1:]
	}
}

// Finish reads a last line the response did not end (ClickHouse always ends its lines).
func (r *tsvReader) Finish(columns func([]Column) error, row func([]any, int) error) error {
	if len(r.partial) == 0 {
		return nil
	}
	line := r.partial
	r.partial = nil
	return r.line(line, columns, row)
}

func (r *tsvReader) line(raw []byte, columns func([]Column) error, row func([]any, int) error) error {
	text := scrub(raw)
	switch r.lines {
	case 0:
		r.names = fields(text)
		r.lines = 1
		return nil
	case 1:
		types := fields(text)
		if len(types) != len(r.names) {
			return errors.New("names and types disagree")
		}
		r.columns = make([]Column, len(types))
		r.asWritten = make([]bool, len(types))
		for i, t := range types {
			r.columns[i] = Column{Name: unescape(r.names[i]), Type: unescape(t)}
			r.asWritten[i] = compositeType.MatchString(unescape(t))
		}
		r.lines = 2
		return columns(r.columns)
	}
	parts := fields(text)
	if len(parts) != len(r.columns) {
		return errWidth
	}
	values := make([]any, len(parts))
	for i, f := range parts {
		switch {
		case f == `\N`:
			values[i] = nil
		case r.asWritten[i]:
			values[i] = f
		default:
			values[i] = unescape(f)
		}
	}
	return row(values, len(raw)+1)
}

// Columns are the header's, once read.
func (r *tsvReader) Columns() []Column { return r.columns }

func fields(line string) []string {
	if line == "" {
		return []string{""}
	}
	return strings.Split(line, "\t")
}

func unescape(field string) string {
	if !strings.ContainsRune(field, '\\') {
		return field
	}
	var b strings.Builder
	b.Grow(len(field))
	escaped := false
	for _, c := range field {
		if escaped {
			if mapped, ok := tsvEscapes[c]; ok {
				b.WriteRune(mapped)
			} else {
				b.WriteRune(c)
			}
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(c)
	}
	if escaped {
		b.WriteRune('\\') // a field ending in one backslash keeps it, as the job path's gsub does
	}
	return b.String()
}

// scrub makes raw valid UTF-8, one U+FFFD for each maximal part of a sequence that is not, as
// Ruby's String#scrub and a browser's TextDecoder do.
func scrub(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	var b strings.Builder
	b.Grow(len(raw) + 8)
	for i := 0; i < len(raw); {
		c, size := utf8.DecodeRune(raw[i:])
		if c != utf8.RuneError || size > 1 {
			b.WriteRune(c)
			i += size
			continue
		}
		b.WriteRune(utf8.RuneError)
		i += maximalSubpart(raw[i:])
	}
	return b.String()
}

// maximalSubpart is how many bytes from the start of p belong to one ill-formed sequence: the
// longest prefix of a well-formed sequence's start, at least one byte (Unicode 3.9, D93b).
func maximalSubpart(p []byte) int {
	lead := p[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead == 0xE0:
		need, lo = 2, 0xA0
	case lead >= 0xE1 && lead <= 0xEC, lead == 0xEE, lead == 0xEF:
		need = 2
	case lead == 0xED:
		need, hi = 2, 0x9F
	case lead == 0xF0:
		need, lo = 3, 0x90
	case lead >= 0xF1 && lead <= 0xF3:
		need = 3
	case lead == 0xF4:
		need, hi = 3, 0x8F
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(p); k++ {
		c := p[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}
