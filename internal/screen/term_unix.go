//go:build linux || darwin

package screen

import (
	"os"
	"syscall"
	"unsafe"
)

// termiosState restores the saved terminal attributes.
type termiosState struct {
	fd  uintptr
	old syscall.Termios
}

func (s *termiosState) restore() error { return ioctlSetTermios(s.fd, &s.old) }

// startRaw puts the input terminal into raw mode and returns a restorer.
func startRaw(in, _ *os.File) (rawRestorer, error) {
	fd := in.Fd()
	var old syscall.Termios
	if err := ioctlGetTermios(fd, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlSetTermios(fd, &raw); err != nil {
		return nil, err
	}
	return &termiosState{fd: fd, old: old}, nil
}

func ioctlGetTermios(fd uintptr, t *syscall.Termios) error {
	return ioctlTermios(fd, ioctlGetReq, t)
}

func ioctlSetTermios(fd uintptr, t *syscall.Termios) error {
	return ioctlTermios(fd, ioctlSetReq, t)
}

func ioctlTermios(fd, req uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

// winsize mirrors the kernel struct for the TIOCGWINSZ ioctl.
type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// termSize returns the terminal dimensions, defaulting to 80x24 on failure.
func termSize(f *os.File) (int, int) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}
