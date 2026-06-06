package bedrock

// AWS event-stream (application/vnd.amazon.eventstream) message framing,
// implemented with the standard library only. Bedrock's
// InvokeModelWithResponseStream returns a sequence of these binary frames; each
// frame carries typed headers and a payload, guarded by two CRC32 checksums.
//
// Wire layout of one message:
//
//	+---------------------------------------------------------------+
//	| total length (uint32)                                         |
//	| headers length (uint32)                                       |
//	| prelude CRC32 (uint32)   -- over the first 8 bytes            |
//	| headers (headers length bytes)                               |
//	| payload (total - headers - 16 bytes)                         |
//	| message CRC32 (uint32)   -- over everything but this field    |
//	+---------------------------------------------------------------+

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	esPreludeLen  = 12 // total(4) + headersLen(4) + preludeCRC(4)
	esMsgOverhead = 16 // prelude(12) + messageCRC(4)
	esMaxMsgLen   = 24 << 20
)

// esMessage is a decoded event-stream frame. Only string-typed (type 7) headers
// are retained, which is all Bedrock needs (:message-type, :event-type, …).
type esMessage struct {
	headers map[string]string
	payload []byte
}

// readESMessage reads a single event-stream frame from r. It returns io.EOF
// when the stream ends cleanly at a message boundary.
func readESMessage(r io.Reader) (*esMessage, error) {
	prelude := make([]byte, esPreludeLen)
	if _, err := io.ReadFull(r, prelude); err != nil {
		return nil, err // io.EOF at a clean boundary
	}
	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])
	if crc32.ChecksumIEEE(prelude[0:8]) != preludeCRC {
		return nil, fmt.Errorf("bedrock: event-stream prelude CRC mismatch")
	}
	if totalLen < esMsgOverhead || totalLen > esMaxMsgLen || headersLen > totalLen-esMsgOverhead {
		return nil, fmt.Errorf("bedrock: invalid event-stream lengths (total=%d headers=%d)", totalLen, headersLen)
	}

	rest := make([]byte, totalLen-esPreludeLen)
	if _, err := io.ReadFull(r, rest); err != nil {
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}

	msgCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])
	h := crc32.NewIEEE()
	_, _ = h.Write(prelude)
	_, _ = h.Write(rest[:len(rest)-4])
	if h.Sum32() != msgCRC {
		return nil, fmt.Errorf("bedrock: event-stream message CRC mismatch")
	}

	headers, err := parseESHeaders(rest[:headersLen])
	if err != nil {
		return nil, err
	}
	return &esMessage{headers: headers, payload: rest[headersLen : len(rest)-4]}, nil
}

// parseESHeaders walks the typed header section, capturing string-valued
// headers and skipping the rest by their fixed or length-prefixed sizes.
func parseESHeaders(b []byte) (map[string]string, error) {
	headers := map[string]string{}
	i := 0
	for i < len(b) {
		nameLen := int(b[i])
		i++
		if i+nameLen > len(b) {
			return nil, fmt.Errorf("bedrock: truncated event-stream header name")
		}
		name := string(b[i : i+nameLen])
		i += nameLen
		if i >= len(b) {
			return nil, fmt.Errorf("bedrock: truncated event-stream header type")
		}
		typ := b[i]
		i++
		switch typ {
		case 0, 1: // bool true / false: no value bytes
		case 2: // byte
			i++
		case 3: // int16
			i += 2
		case 4: // int32
			i += 4
		case 5, 8: // int64, timestamp
			i += 8
		case 6, 7: // byte array, string: uint16 length prefix
			if i+2 > len(b) {
				return nil, fmt.Errorf("bedrock: truncated event-stream header value length")
			}
			vlen := int(binary.BigEndian.Uint16(b[i : i+2]))
			i += 2
			if i+vlen > len(b) {
				return nil, fmt.Errorf("bedrock: truncated event-stream header value")
			}
			if typ == 7 {
				headers[name] = string(b[i : i+vlen])
			}
			i += vlen
		case 9: // uuid
			i += 16
		default:
			return nil, fmt.Errorf("bedrock: unknown event-stream header type %d", typ)
		}
		if i > len(b) {
			return nil, fmt.Errorf("bedrock: truncated event-stream header value")
		}
	}
	return headers, nil
}

// encodeESMessage builds a single event-stream frame from string headers and a
// payload. It is the inverse of readESMessage and is used by the tests (and any
// future request-side streaming) to synthesize Bedrock responses.
func encodeESMessage(headers map[string]string, payload []byte) []byte {
	var hb []byte
	for k, v := range headers {
		hb = append(hb, byte(len(k)))
		hb = append(hb, k...)
		hb = append(hb, 7) // string type
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(v)))
		hb = append(hb, l[:]...)
		hb = append(hb, v...)
	}

	totalLen := uint32(esMsgOverhead + len(hb) + len(payload))
	buf := make([]byte, 0, totalLen)
	var u32 [4]byte
	binary.BigEndian.PutUint32(u32[:], totalLen)
	buf = append(buf, u32[:]...)
	binary.BigEndian.PutUint32(u32[:], uint32(len(hb)))
	buf = append(buf, u32[:]...)
	binary.BigEndian.PutUint32(u32[:], crc32.ChecksumIEEE(buf[:8]))
	buf = append(buf, u32[:]...)
	buf = append(buf, hb...)
	buf = append(buf, payload...)
	binary.BigEndian.PutUint32(u32[:], crc32.ChecksumIEEE(buf))
	buf = append(buf, u32[:]...)
	return buf
}
