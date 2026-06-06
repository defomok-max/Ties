//go:build !linux && !darwin && !windows

package screen

import (
	"errors"
	"os"
)

// startRaw is unsupported on this platform; callers fall back to a line UI.
func startRaw(_, _ *os.File) (rawRestorer, error) {
	return nil, errors.New("interactive screen not supported on this platform")
}

// termSize returns a conservative default on unsupported platforms.
func termSize(_ *os.File) (int, int) { return 80, 24 }
