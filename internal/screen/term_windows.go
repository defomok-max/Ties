//go:build windows

package screen

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

// Console mode flags (wincon.h).
const (
	enableProcessedInput        = 0x0001
	enableLineInput             = 0x0002
	enableEchoInput             = 0x0004
	enableMouseInput            = 0x0010
	enableQuickEditMode         = 0x0040
	enableExtendedFlags         = 0x0080
	enableVirtualTerminalInput  = 0x0200
	enableProcessedOutput       = 0x0001
	enableVirtualTerminalOutput = 0x0004
)

type winState struct {
	inFd, outFd     uintptr
	inMode, outMode uint32
}

func (s *winState) restore() error {
	_ = setConsoleMode(s.inFd, s.inMode)
	return setConsoleMode(s.outFd, s.outMode)
}

// startRaw enables virtual-terminal input/output and mouse reporting on the
// Windows console so the same ANSI/SGR event stream used on Unix is delivered.
func startRaw(in, out *os.File) (rawRestorer, error) {
	inFd, outFd := in.Fd(), out.Fd()
	var im, om uint32
	if err := getConsoleMode(inFd, &im); err != nil {
		return nil, err
	}
	if err := getConsoleMode(outFd, &om); err != nil {
		return nil, err
	}
	ni := im
	ni &^= enableLineInput | enableEchoInput | enableProcessedInput | enableQuickEditMode
	ni |= enableVirtualTerminalInput | enableMouseInput | enableExtendedFlags
	no := om | enableVirtualTerminalOutput | enableProcessedOutput
	if err := setConsoleMode(inFd, ni); err != nil {
		return nil, err
	}
	if err := setConsoleMode(outFd, no); err != nil {
		_ = setConsoleMode(inFd, im)
		return nil, err
	}
	return &winState{inFd: inFd, outFd: outFd, inMode: im, outMode: om}, nil
}

func getConsoleMode(fd uintptr, mode *uint32) error {
	r, _, err := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(mode)))
	if r == 0 {
		return err
	}
	return nil
}

func setConsoleMode(fd uintptr, mode uint32) error {
	r, _, err := procSetConsoleMode.Call(fd, uintptr(mode))
	if r == 0 {
		return err
	}
	return nil
}

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

// termSize returns the console window dimensions, defaulting to 80x24.
func termSize(f *os.File) (int, int) {
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80, 24
	}
	cols := int(info.Window.Right-info.Window.Left) + 1
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	if cols <= 0 || rows <= 0 {
		return 80, 24
	}
	return cols, rows
}
