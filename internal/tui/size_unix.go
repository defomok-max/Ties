//go:build linux || darwin

package tui

import (
	"os"
	"syscall"
	"unsafe"
)

// winsize mirrors the kernel's struct winsize (the syscall package does not
// export it on Linux, so we declare our own).
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// terminalSize returns the character cell dimensions of the terminal attached
// to f using the TIOCGWINSZ ioctl. It returns ok=false when f is not a
// terminal or the ioctl fails, letting the caller fall back to defaults.
func terminalSize(f *os.File) (cols, rows int, ok bool) {
	if f == nil {
		return 0, 0, false
	}
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}
