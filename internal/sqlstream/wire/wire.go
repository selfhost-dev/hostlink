// Package wire is the frame of the SQL stream path (sql-stream-v1): one binary WebSocket
// message holds a version, a kind, a JSON header and a body. The relay reads headers and
// routes; only the agent and the browser tab read a data frame's body. The contract is the
// selfhost backend's docs/sql-stream-protocol.md (section 3).
package wire

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the only frame version there is.
const Version = 1

// MaxHeader is the largest header a frame may carry. Headers name a run, a sequence number
// and a type; anything bigger is not a header.
const MaxHeader = 4096

// Kind is what a frame is for.
type Kind uint8

const (
	KindHello    Kind = 1  // agent, browser -> relay: the body is a token
	KindWelcome  Kind = 2  // relay -> agent, browser
	KindRun      Kind = 3  // browser -> relay -> agent: the body is a ticket
	KindAccepted Kind = 4  // agent -> browser: the key exchange, seq 0
	KindRejected Kind = 5  // agent -> browser: a refusal before the run starts
	KindData     Kind = 6  // agent -> browser: sealed columns, rows, progress, end, error
	KindAck      Kind = 7  // browser -> agent: every frame up to seq is in
	KindCancel   Kind = 8  // browser -> agent
	KindResume   Kind = 9  // browser -> agent: send again every frame after seq
	KindGone     Kind = 10 // relay -> agent: the run's browser did not come back
	KindError    Kind = 11 // relay -> agent, browser
)

func (k Kind) String() string {
	switch k {
	case KindHello:
		return "hello"
	case KindWelcome:
		return "welcome"
	case KindRun:
		return "run"
	case KindAccepted:
		return "accepted"
	case KindRejected:
		return "rejected"
	case KindData:
		return "data"
	case KindAck:
		return "ack"
	case KindCancel:
		return "cancel"
	case KindResume:
		return "resume"
	case KindGone:
		return "gone"
	case KindError:
		return "error"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// Known reports whether k is a kind of this version.
func (k Kind) Known() bool { return k >= KindHello && k <= KindError }

// Data frame types (Header.T), in the clear so the relay knows a run's last frame.
const (
	TypeColumns  = "columns"
	TypeRows     = "rows"
	TypeProgress = "progress"
	TypeEnd      = "end"
	TypeError    = "error"
)

// Header is a frame's JSON header. Seq is a pointer because 0 is a frame's number (accepted).
type Header struct {
	Run  string  `json:"run,omitempty"`
	Seq  *uint64 `json:"seq,omitempty"`
	T    string  `json:"t,omitempty"`
	Conn string  `json:"conn,omitempty"`
	Max  int     `json:"max,omitempty"`
}

// SeqOf returns a pointer to n, for Header.Seq.
func SeqOf(n uint64) *uint64 { return &n }

// Final reports whether a data frame of this type is a run's last.
func Final(t string) bool { return t == TypeEnd || t == TypeError }

// Frame is a decoded message. Prefix is the version, kind, length and header bytes exactly as
// they arrived: a data frame's additional data, so a header cannot change under its body.
type Frame struct {
	Kind   Kind
	Header Header
	Prefix []byte
	Body   []byte
}

var (
	ErrShort   = errors.New("frame shorter than its header")
	ErrVersion = errors.New("frame of another version")
	ErrKind    = errors.New("frame of an unknown kind")
	ErrHeader  = errors.New("frame header is not a JSON object")
)

// Prefix encodes a frame's version, kind, header length and header: what a body follows,
// and a sealed body's additional data.
func Prefix(kind Kind, h Header) ([]byte, error) {
	if !kind.Known() {
		return nil, ErrKind
	}
	header, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if len(header) > MaxHeader {
		return nil, fmt.Errorf("frame header is %d bytes, at most %d", len(header), MaxHeader)
	}
	out := make([]byte, 4, 4+len(header))
	out[0] = Version
	out[1] = byte(kind)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(header)))
	return append(out, header...), nil
}

// Encode returns a whole message: the prefix, then the body.
func Encode(kind Kind, h Header, body []byte) ([]byte, error) {
	prefix, err := Prefix(kind, h)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	return append(out, body...), nil
}

// Decode parses a message. Prefix and Body alias msg.
func Decode(msg []byte) (Frame, error) {
	if len(msg) < 4 {
		return Frame{}, ErrShort
	}
	if msg[0] != Version {
		return Frame{}, ErrVersion
	}
	kind := Kind(msg[1])
	if !kind.Known() {
		return Frame{}, ErrKind
	}
	n := int(binary.BigEndian.Uint16(msg[2:4]))
	if n > MaxHeader || len(msg) < 4+n {
		return Frame{}, ErrShort
	}
	raw := msg[4 : 4+n]
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Frame{}, ErrHeader
	}
	var h Header
	if err := json.Unmarshal(raw, &h); err != nil {
		return Frame{}, ErrHeader
	}
	return Frame{Kind: kind, Header: h, Prefix: msg[:4+n], Body: msg[4+n:]}, nil
}
