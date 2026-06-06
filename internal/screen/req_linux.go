//go:build linux

package screen

import "syscall"

// ioctl request numbers for getting/setting terminal attributes on Linux.
const (
	ioctlGetReq = syscall.TCGETS
	ioctlSetReq = syscall.TCSETS
)
