//go:build windows

package sdk

import "errors"

// diskFree is not implemented on Windows: the diagnostics contract already
// treats an error as "disk: unknown", so the probe degrades gracefully there.
func diskFree(path string) (uint64, error) {
	return 0, errors.New("disk free probe not supported on this platform")
}
