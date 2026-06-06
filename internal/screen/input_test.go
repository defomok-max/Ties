package screen

import "testing"

func TestParseKeys(t *testing.T) {
	cases := []struct {
		in  string
		key Key
	}{
		{"\x1b[A", KeyUp},
		{"\x1b[B", KeyDown},
		{"\x1b[C", KeyRight},
		{"\x1b[D", KeyLeft},
		{"\r", KeyEnter},
		{"\n", KeyEnter},
		{"\x03", KeyCtrlC},
		{"\x7f", KeyBackspace},
		{"\t", KeyTab},
		{"\x1b[5~", KeyPgUp},
		{"\x1b[6~", KeyPgDn},
		{"\x1b[3~", KeyDelete},
	}
	for _, c := range cases {
		evs, n := parseEvents([]byte(c.in))
		if n != len(c.in) {
			t.Fatalf("%q: consumed %d want %d", c.in, n, len(c.in))
		}
		if len(evs) != 1 || evs[0].Kind != EventKey || evs[0].Key != c.key {
			t.Fatalf("%q: got %+v want key %d", c.in, evs, c.key)
		}
	}
}

func TestParseRune(t *testing.T) {
	evs, n := parseEvents([]byte("q"))
	if n != 1 || len(evs) != 1 || evs[0].Key != KeyRune || evs[0].Rune != 'q' {
		t.Fatalf("rune parse: %+v n=%d", evs, n)
	}
	// Multi-byte UTF-8.
	evs, _ = parseEvents([]byte("é"))
	if len(evs) != 1 || evs[0].Rune != 'é' {
		t.Fatalf("utf8 parse: %+v", evs)
	}
}

func TestParseMousePress(t *testing.T) {
	// Left button press at column 12, row 7.
	evs, n := parseEvents([]byte("\x1b[<0;12;7M"))
	if n != 10 || len(evs) != 1 {
		t.Fatalf("consumed=%d evs=%+v", n, evs)
	}
	m := evs[0].Mouse
	if evs[0].Kind != EventMouse || !m.Press || m.Button != 0 || m.X != 12 || m.Y != 7 {
		t.Fatalf("mouse press: %+v", m)
	}
	// Release.
	evs, _ = parseEvents([]byte("\x1b[<0;12;7m"))
	if evs[0].Mouse.Press {
		t.Fatal("expected release")
	}
}

func TestParseMouseWheel(t *testing.T) {
	up, _ := parseEvents([]byte("\x1b[<64;3;3M"))
	if up[0].Mouse.Wheel != -1 {
		t.Fatalf("wheel up: %+v", up[0].Mouse)
	}
	down, _ := parseEvents([]byte("\x1b[<65;3;3M"))
	if down[0].Mouse.Wheel != 1 {
		t.Fatalf("wheel down: %+v", down[0].Mouse)
	}
}

func TestParseMotion(t *testing.T) {
	// Motion report: button 0 + motion flag (32) => 35.
	evs, _ := parseEvents([]byte("\x1b[<35;5;5M"))
	if !evs[0].Mouse.Motion {
		t.Fatalf("expected motion: %+v", evs[0].Mouse)
	}
}

func TestPartialSequenceHeldBack(t *testing.T) {
	// An incomplete mouse sequence should consume nothing and yield no events.
	evs, n := parseEvents([]byte("\x1b[<0;12"))
	if n != 0 || len(evs) != 0 {
		t.Fatalf("partial: n=%d evs=%+v", n, evs)
	}
	// A lone trailing ESC is a real Esc key.
	evs, n = parseEvents([]byte("\x1b"))
	if n != 1 || evs[0].Key != KeyEsc {
		t.Fatalf("lone esc: n=%d evs=%+v", n, evs)
	}
}

func TestMixedBatch(t *testing.T) {
	// A burst: down arrow, a rune, enter.
	evs, n := parseEvents([]byte("\x1b[Bxa\r"))
	if n != 6 {
		t.Fatalf("consumed %d", n)
	}
	if len(evs) != 4 || evs[0].Key != KeyDown || evs[1].Rune != 'x' || evs[2].Rune != 'a' || evs[3].Key != KeyEnter {
		t.Fatalf("mixed: %+v", evs)
	}
}
