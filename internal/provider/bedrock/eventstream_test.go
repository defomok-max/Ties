package bedrock

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"
)

func TestEventStreamRoundTrip(t *testing.T) {
	headers := map[string]string{
		":message-type": "event",
		":event-type":   "chunk",
		":content-type": "application/json",
	}
	payload := []byte(`{"bytes":"eyJ0eXBlIjoibWVzc2FnZV9zdG9wIn0="}`)

	frame := encodeESMessage(headers, payload)
	msg, err := readESMessage(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("readESMessage: %v", err)
	}
	for k, v := range headers {
		if msg.headers[k] != v {
			t.Fatalf("header %q = %q, want %q", k, msg.headers[k], v)
		}
	}
	if !bytes.Equal(msg.payload, payload) {
		t.Fatalf("payload = %q, want %q", msg.payload, payload)
	}
}

func TestEventStreamMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 3; i++ {
		buf.Write(encodeESMessage(map[string]string{":event-type": "chunk"}, []byte("p")))
	}
	r := bytes.NewReader(buf.Bytes())
	n := 0
	for {
		_, err := readESMessage(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("frame %d: %v", n, err)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("decoded %d frames, want 3", n)
	}
}

func TestEventStreamCRCMismatch(t *testing.T) {
	frame := encodeESMessage(map[string]string{":event-type": "chunk"}, []byte("hello"))
	// Corrupt a payload byte; the message CRC must catch it.
	frame[len(frame)-6] ^= 0xFF
	if _, err := readESMessage(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected CRC mismatch error")
	}
}

func TestEventStreamPreludeCRCMismatch(t *testing.T) {
	frame := encodeESMessage(map[string]string{":event-type": "chunk"}, []byte("hello"))
	// Corrupt the headers-length field without fixing the prelude CRC.
	binary.BigEndian.PutUint32(frame[4:8], 9999)
	if _, err := readESMessage(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected prelude CRC mismatch error")
	}
}

func TestEventStreamTruncated(t *testing.T) {
	frame := encodeESMessage(map[string]string{":event-type": "chunk"}, []byte("hello"))
	if _, err := readESMessage(bytes.NewReader(frame[:len(frame)-5])); err == nil {
		t.Fatal("expected error on truncated frame")
	}
}

func TestParseESHeadersSkipsNonStringTypes(t *testing.T) {
	// Build a header section by hand mixing a bool, an int32 and a string.
	var b []byte
	put := func(name string, typ byte, val []byte) {
		b = append(b, byte(len(name)))
		b = append(b, name...)
		b = append(b, typ)
		b = append(b, val...)
	}
	put("flag", 0, nil)                    // bool true (no value)
	put("n", 4, []byte{0, 0, 0, 5})        // int32
	put(":event-type", 7, strVal("chunk")) // string

	h, err := parseESHeaders(b)
	if err != nil {
		t.Fatalf("parseESHeaders: %v", err)
	}
	if h[":event-type"] != "chunk" {
		t.Fatalf(":event-type = %q", h[":event-type"])
	}
	if _, ok := h["flag"]; ok {
		t.Fatal("non-string header should be skipped")
	}
	if _, ok := h["n"]; ok {
		t.Fatal("int header should be skipped")
	}
}

func strVal(s string) []byte {
	out := make([]byte, 2+len(s))
	binary.BigEndian.PutUint16(out[:2], uint16(len(s)))
	copy(out[2:], s)
	return out
}

// sanity: ensure our CRC choice matches the standard IEEE polynomial used by
// AWS event streams.
func TestPreludeCRCUsesIEEE(t *testing.T) {
	frame := encodeESMessage(nil, []byte("x"))
	got := binary.BigEndian.Uint32(frame[8:12])
	want := crc32.ChecksumIEEE(frame[:8])
	if got != want {
		t.Fatalf("prelude CRC = %d, want %d", got, want)
	}
}
