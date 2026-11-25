package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		kind Kind
		h    Header
		body []byte
	}{
		{KindHello, Header{}, []byte("tok.en.sig")},
		{KindWelcome, Header{Conn: "c1", Max: 1 << 20}, nil},
		{KindRun, Header{Run: "sqr_0123456789abcdef0123"}, []byte("ticket")},
		{KindAccepted, Header{Run: "sqr_0123456789abcdef0123", Seq: SeqOf(0)}, []byte(`{"apk":"a","sig":"s"}`)},
		{KindData, Header{Run: "sqr_0123456789abcdef0123", Seq: SeqOf(7), T: TypeRows}, []byte{0, 1, 2, 255}},
		{KindAck, Header{Run: "sqr_0123456789abcdef0123", Seq: SeqOf(7)}, nil},
		{KindError, Header{}, []byte(`{"code":"agent_offline"}`)},
	}
	for _, c := range cases {
		msg, err := Encode(c.kind, c.h, c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		f, err := Decode(msg)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if f.Kind != c.kind || f.Header.Run != c.h.Run || f.Header.T != c.h.T || f.Header.Conn != c.h.Conn || f.Header.Max != c.h.Max {
			t.Fatalf("%s: header %+v", c.kind, f.Header)
		}
		if (c.h.Seq == nil) != (f.Header.Seq == nil) || (c.h.Seq != nil && *c.h.Seq != *f.Header.Seq) {
			t.Fatalf("%s: seq %v", c.kind, f.Header.Seq)
		}
		if !bytes.Equal(f.Body, c.body) && !(len(f.Body) == 0 && len(c.body) == 0) {
			t.Fatalf("%s: body %q", c.kind, f.Body)
		}
		prefix, _ := Prefix(c.kind, c.h)
		if !bytes.Equal(f.Prefix, prefix) {
			t.Fatalf("%s: prefix differs", c.kind)
		}
	}
}

func TestSeqZeroIsSent(t *testing.T) {
	msg, _ := Encode(KindAccepted, Header{Run: "r", Seq: SeqOf(0)}, nil)
	if !strings.Contains(string(msg), `"seq":0`) {
		t.Fatalf("seq 0 dropped: %q", msg)
	}
}

func TestDecodeRefuses(t *testing.T) {
	good, _ := Encode(KindRun, Header{Run: "r"}, []byte("x"))
	withVersion := append([]byte{}, good...)
	withVersion[0] = 2
	withKind := append([]byte{}, good...)
	withKind[1] = 99
	longHeader := append([]byte{}, good...)
	binary.BigEndian.PutUint16(longHeader[2:4], 60000)
	notObject, _ := Encode(KindRun, Header{}, nil)
	notObject = append([]byte{Version, byte(KindRun), 0, 2}, []byte("[]")...)

	cases := map[string]struct {
		msg  []byte
		want error
	}{
		"empty":       {nil, ErrShort},
		"three bytes": {[]byte{1, 3, 0}, ErrShort},
		"version":     {withVersion, ErrVersion},
		"kind":        {withKind, ErrKind},
		"long header": {longHeader, ErrShort},
		"not object":  {notObject, ErrHeader},
		"bad json":    {append([]byte{Version, byte(KindRun), 0, 2}, []byte("{x")...), ErrHeader},
	}
	for name, c := range cases {
		if _, err := Decode(c.msg); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestPrefixRefusesAHugeHeader(t *testing.T) {
	if _, err := Prefix(KindRun, Header{Run: strings.Repeat("x", MaxHeader)}); err == nil {
		t.Fatal("a header over MaxHeader was encoded")
	}
	if _, err := Prefix(Kind(0), Header{}); !errors.Is(err, ErrKind) {
		t.Fatalf("kind 0: %v", err)
	}
}

func TestFinal(t *testing.T) {
	for _, typ := range []string{TypeEnd, TypeError} {
		if !Final(typ) {
			t.Errorf("%s is a run's last frame", typ)
		}
	}
	for _, typ := range []string{TypeColumns, TypeRows, TypeProgress, ""} {
		if Final(typ) {
			t.Errorf("%s is not a run's last frame", typ)
		}
	}
}
