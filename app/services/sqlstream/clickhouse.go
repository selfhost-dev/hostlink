package sqlstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A run's statement goes to the server's own HTTP port on loopback, as the job path's script
// sends it (selfhost script/clickhouse/clickhouse_sql_run.sh), with framing on: one response
// carries the rows in pieces, progress every 100 ms and any exception, as they happen
// (ClickHouse 26.8, framing_output_format). The account's profile lets exactly these settings
// change in read-only mode, within bounds (the backend's docs/sql-stream-protocol.md, section 9).

type chRequest struct {
	BaseURL  string
	User     string
	Password string
	Database string
	QueryID  string
	SQL      string
	Limit    int64 // rows to show + 1, so a cut is seen
	Secs     int64
}

// chInt reads ClickHouse's counters, which framing sends as strings.
type chInt int64

func (n *chInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		u, uerr := strconv.ParseUint(s, 10, 64)
		if uerr != nil {
			return err
		}
		v = int64(u >> 1) // past int64: never a real counter
	}
	*n = chInt(v)
	return nil
}

type chProgress struct {
	ReadRows    chInt `json:"read_rows"`
	ReadBytes   chInt `json:"read_bytes"`
	TotalRows   chInt `json:"total_rows_to_read"`
	ResultRows  chInt `json:"result_rows"`
	ResultBytes chInt `json:"result_bytes"`
	ElapsedNs   chInt `json:"elapsed_ns"`
	Memory      chInt `json:"memory_usage"`
}

// chPacket keeps data and exception as their raw JSON: ClickHouse writes a String's bytes as they
// are, valid UTF-8 or not, and Go's decoder would put one U+FFFD for each bad byte, where the job
// path puts one for each maximal bad sequence (scrub). rawString undoes only JSON's escapes.
type chPacket struct {
	Packet    string          `json:"packet"`
	Data      json.RawMessage `json:"data"`
	Progress  *chProgress     `json:"progress"`
	Exception json.RawMessage `json:"exception"`
}

// rawString reads a JSON string literal keeping every byte that is not an escape as it is.
func rawString(lit []byte) (string, error) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", errors.New("not a JSON string")
	}
	in := lit[1 : len(lit)-1]
	if bytes.IndexByte(in, '\\') < 0 {
		return string(in), nil
	}
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(in) {
			return "", errors.New("a JSON string ends in a backslash")
		}
		switch in[i] {
		case '"', '\\', '/':
			out = append(out, in[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, n, err := unicodeEscape(in[i+1:])
			if err != nil {
				return "", err
			}
			out = utf8.AppendRune(out, r)
			i += n
		default:
			return "", errors.New("a JSON string has an unknown escape")
		}
	}
	return string(out), nil
}

// unicodeEscape reads the hex after \u, and a low surrogate's \uXXXX after a high one; it returns
// the rune and how many bytes it read. A lone surrogate is U+FFFD, as Go's decoder makes it.
func unicodeEscape(p []byte) (rune, int, error) {
	hex4 := func(b []byte) (rune, bool) {
		if len(b) < 4 {
			return 0, false
		}
		v, err := strconv.ParseUint(string(b[:4]), 16, 32)
		return rune(v), err == nil
	}
	r, ok := hex4(p)
	if !ok {
		return 0, 0, errors.New("a JSON string has a bad \\u escape")
	}
	if r >= 0xD800 && r < 0xDC00 {
		if len(p) >= 10 && p[4] == '\\' && p[5] == 'u' {
			if lo, ok := hex4(p[6:]); ok && lo >= 0xDC00 && lo < 0xE000 {
				return (r-0xD800)<<10 + (lo - 0xDC00) + 0x10000, 10, nil
			}
		}
		return utf8.RuneError, 4, nil
	}
	if r >= 0xDC00 && r < 0xE000 {
		return utf8.RuneError, 4, nil
	}
	return r, 4, nil
}

// chSink takes a response's packets as they come. An error from one stops the read.
type chSink interface {
	Data(text string) error
	Progress(p chProgress) error
	Exception(text string) error
}

// errUnreachable is a server that did not answer (refused, reset before a response).
var errUnreachable = errors.New("the database did not answer")

func chURL(req chRequest) string {
	q := url.Values{}
	q.Set("database", req.Database)
	q.Set("query_id", req.QueryID)
	q.Set("default_format", "TSVWithNamesAndTypes")
	q.Set("framing_output_format", "JSONEachPacketString")
	q.Set("send_profile_events", "0")
	q.Set("http_wait_end_of_query", "0")
	q.Set("limit", strconv.FormatInt(req.Limit, 10))
	q.Set("max_execution_time", strconv.FormatInt(req.Secs, 10))
	return strings.TrimRight(req.BaseURL, "/") + "/?" + q.Encode()
}

// runClickHouse sends the statement and hands each packet to sink until the response ends,
// ctx ends, or sink stops it.
func runClickHouse(ctx context.Context, client *http.Client, req chRequest, sink chSink) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chURL(req), strings.NewReader(req.SQL))
	if err != nil {
		return err
	}
	httpReq.Header.Set("X-ClickHouse-User", req.User)
	httpReq.Header.Set("X-ClickHouse-Key", req.Password)
	httpReq.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Refused before it ran (a login, a constraint): ClickHouse's error as plain text.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return sink.Exception(string(bytes.TrimSpace(body)))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var p chPacket
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", errUnreachable, err)
		}
		switch p.Packet {
		case "data":
			var text string
			if text, err = rawString(p.Data); err == nil {
				err = sink.Data(text)
			}
		case "progress":
			if p.Progress != nil {
				err = sink.Progress(*p.Progress)
			}
		case "exception":
			var text string
			if text, err = rawString(p.Exception); err == nil {
				err = sink.Exception(text)
			}
		}
		if err != nil {
			return err
		}
	}
}

// engineError is ClickHouse's error read as the job path reads it: its number and its words,
// without its name and version, and where it is, in characters of what ran.
type engineError struct {
	Code     string
	Message  string
	Position *int
}

// "Code: 62. DB::Exception: <words> (SYNTAX_ERROR) (version 26.8.30.8 (official build))"
var (
	chErrorPattern   = regexp.MustCompile(`(?s)\ACode: (\d+)\. DB::Exception: (.*?)(?: \([A-Z_]+\))?(?: \(version [^)]*\)?\))?\s*\z`)
	failedAtPosition = regexp.MustCompile(`failed at position (\d+)`)
	unknownName      = regexp.MustCompile("Unknown (?:expression|table expression) identifier [`'](.+?)[`']")
)

const maxMessage = 2000

func parseEngineError(text, sql string) engineError {
	text = strings.TrimSpace(scrub([]byte(text)))
	m := chErrorPattern.FindStringSubmatch(text)
	if m == nil {
		return engineError{Message: truncateRunes(text, maxMessage)}
	}
	message := truncateRunes(strings.TrimSpace(m[2]), maxMessage)
	return engineError{Code: m[1], Message: message, Position: errorPosition(message, sql)}
}

// errorPosition is the job path's: a syntax error's byte position (from 1) as characters, or
// where an unknown name first appears.
func errorPosition(message, sql string) *int {
	if m := failedAtPosition.FindStringSubmatch(message); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 {
			return nil
		}
		end := n - 1
		if end > len(sql) {
			end = len(sql)
		}
		chars := utf8.RuneCountInString(scrub([]byte(sql[:end]))) + 1
		return &chars
	}
	m := unknownName.FindStringSubmatch(message)
	if m == nil {
		return nil
	}
	name := m[1]
	if i := strings.Index(sql, "`"+name+"`"); i >= 0 {
		pos := utf8.RuneCountInString(sql[:i]) + 1
		return &pos
	}
	bare := regexp.MustCompile(`(^|[^\w` + "`" + `$.])` + regexp.QuoteMeta(name) + `($|[^\w` + "`" + `$])`)
	if loc := bare.FindStringSubmatchIndex(sql); loc != nil {
		start := loc[3] // after the character before the name
		pos := utf8.RuneCountInString(sql[:start]) + 1
		return &pos
	}
	return nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

// errorClass names an engine error for the browser and the outcome (spec section 5).
func errorClass(code string) string {
	switch code {
	case "62":
		return "syntax"
	case "159":
		return "timeout"
	case "241":
		return "memory"
	case "516":
		return "login_failed"
	case "":
		return "internal"
	}
	return "engine"
}
