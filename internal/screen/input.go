package screen

import "unicode/utf8"

// EventKind classifies a decoded input event.
type EventKind int

// Event kinds.
const (
	EventKey EventKind = iota
	EventMouse
)

// Key identifies a non-printable key (or KeyRune for a printable character).
type Key int

// Recognised keys.
const (
	KeyNone Key = iota
	KeyRune
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyTab
	KeyCtrlC
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
)

// Mouse describes a mouse event in 1-based terminal coordinates.
type Mouse struct {
	X, Y   int
	Button int  // 0 left, 1 middle, 2 right
	Press  bool // true on button press, false on release
	Motion bool // true for a pure move (hover) report
	Wheel  int  // -1 wheel up, +1 wheel down, 0 otherwise
}

// Event is a single decoded input event.
type Event struct {
	Kind  EventKind
	Key   Key
	Rune  rune
	Mouse Mouse
}

// parseEvents decodes as many complete events as possible from data, returning
// the events and the number of bytes consumed. A trailing partial escape
// sequence is left unconsumed so the caller can prepend the next read.
func parseEvents(data []byte) (events []Event, consumed int) {
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x1b:
			ev, n, complete := parseEscape(data[i:])
			if !complete {
				// Incomplete sequence at the end of the buffer; stop and keep
				// the remainder for the next read. A lone trailing ESC is
				// treated as a real Esc keypress (no more bytes are coming).
				if len(data[i:]) == 1 {
					events = append(events, Event{Kind: EventKey, Key: KeyEsc})
					i++
					continue
				}
				return events, i
			}
			if ev.Kind != EventKind(-1) {
				events = append(events, ev)
			}
			i += n
		case b == 0x0d || b == 0x0a:
			events = append(events, Event{Kind: EventKey, Key: KeyEnter})
			i++
		case b == 0x03:
			events = append(events, Event{Kind: EventKey, Key: KeyCtrlC})
			i++
		case b == 0x7f || b == 0x08:
			events = append(events, Event{Kind: EventKey, Key: KeyBackspace})
			i++
		case b == 0x09:
			events = append(events, Event{Kind: EventKey, Key: KeyTab})
			i++
		case b < 0x20:
			// Other control byte: ignore.
			i++
		default:
			r, sz := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && sz <= 1 {
				if len(data[i:]) < utf8.UTFMax && !utf8.FullRune(data[i:]) {
					return events, i // incomplete multibyte rune
				}
				i++
				continue
			}
			events = append(events, Event{Kind: EventKey, Key: KeyRune, Rune: r})
			i += sz
		}
	}
	return events, i
}

// parseEscape decodes one escape sequence starting at data[0]==0x1b. It returns
// the event, the number of bytes consumed and whether the sequence was
// complete. A returned Event with Kind==-1 means "consumed but no event".
func parseEscape(data []byte) (Event, int, bool) {
	if len(data) < 2 {
		return Event{}, 0, false
	}
	if data[1] != '[' && data[1] != 'O' {
		// ESC followed by a normal byte: treat ESC alone.
		return Event{Kind: EventKey, Key: KeyEsc}, 1, true
	}
	if len(data) < 3 {
		return Event{}, 0, false
	}
	// SGR mouse: ESC [ < b ; x ; y (M|m)
	if data[1] == '[' && data[2] == '<' {
		return parseMouse(data)
	}
	// CSI final-letter sequences.
	switch data[2] {
	case 'A':
		return Event{Kind: EventKey, Key: KeyUp}, 3, true
	case 'B':
		return Event{Kind: EventKey, Key: KeyDown}, 3, true
	case 'C':
		return Event{Kind: EventKey, Key: KeyRight}, 3, true
	case 'D':
		return Event{Kind: EventKey, Key: KeyLeft}, 3, true
	case 'H':
		return Event{Kind: EventKey, Key: KeyHome}, 3, true
	case 'F':
		return Event{Kind: EventKey, Key: KeyEnd}, 3, true
	}
	// ESC [ <n> ~  (Home/End/PgUp/PgDn/Delete on many terminals)
	if data[2] >= '0' && data[2] <= '9' {
		j := 2
		for j < len(data) && data[j] >= '0' && data[j] <= '9' {
			j++
		}
		if j >= len(data) {
			return Event{}, 0, false
		}
		if data[j] == '~' {
			key := KeyNone
			switch string(data[2:j]) {
			case "1", "7":
				key = KeyHome
			case "4", "8":
				key = KeyEnd
			case "3":
				key = KeyDelete
			case "5":
				key = KeyPgUp
			case "6":
				key = KeyPgDn
			}
			if key == KeyNone {
				return Event{Kind: EventKind(-1)}, j + 1, true
			}
			return Event{Kind: EventKey, Key: key}, j + 1, true
		}
	}
	// Unknown CSI: consume up to the final byte (0x40-0x7e) if present.
	for j := 2; j < len(data); j++ {
		if data[j] >= 0x40 && data[j] <= 0x7e {
			return Event{Kind: EventKind(-1)}, j + 1, true
		}
	}
	return Event{}, 0, false
}

// parseMouse decodes an SGR mouse sequence: ESC [ < b ; x ; y (M|m).
func parseMouse(data []byte) (Event, int, bool) {
	// Find the terminating 'M' or 'm'.
	end := -1
	for j := 3; j < len(data); j++ {
		if data[j] == 'M' || data[j] == 'm' {
			end = j
			break
		}
	}
	if end < 0 {
		return Event{}, 0, false
	}
	b, x, y, ok := parseThreeInts(data[3:end])
	if !ok {
		return Event{Kind: EventKind(-1)}, end + 1, true
	}
	press := data[end] == 'M'
	m := Mouse{X: x, Y: y, Press: press}
	switch {
	case b&64 != 0: // wheel
		if b&1 != 0 {
			m.Wheel = 1 // wheel down (65)
		} else {
			m.Wheel = -1 // wheel up (64)
		}
	default:
		if b&32 != 0 { // motion flag
			m.Motion = true
		}
		m.Button = b & 3
	}
	return Event{Kind: EventMouse, Mouse: m}, end + 1, true
}

// parseThreeInts parses "a;b;c" of decimal integers.
func parseThreeInts(s []byte) (a, b, c int, ok bool) {
	vals := [3]int{}
	idx := 0
	cur := 0
	has := false
	for _, ch := range s {
		switch {
		case ch >= '0' && ch <= '9':
			cur = cur*10 + int(ch-'0')
			has = true
		case ch == ';':
			if idx > 1 {
				return 0, 0, 0, false
			}
			vals[idx] = cur
			idx++
			cur = 0
			has = false
		default:
			return 0, 0, 0, false
		}
	}
	if !has || idx != 2 {
		return 0, 0, 0, false
	}
	vals[2] = cur
	return vals[0], vals[1], vals[2], true
}
