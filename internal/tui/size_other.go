//go:build !linux && !darwin

package tui

import "os"

// terminalSize falls back to the environment-driven default on platforms where
// the TIOCGWINSZ ioctl is not wired up here. resolveSize handles the env and
// default values, so this simply reports "unknown".
func terminalSize(_ *os.File) (cols, rows int, ok bool) {
	return 0, 0, false
}
