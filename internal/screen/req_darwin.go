//go:build darwin

package screen

import "syscall"

// ioctl request numbers for getting/setting terminal attributes on macOS.
const (
	ioctlGetReq = syscall.TIOCGETA
	ioctlSetReq = syscall.TIOCSETA
)
