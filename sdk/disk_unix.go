//go:build !windows

package sdk

import "syscall"

// diskFree reports free bytes available to unprivileged users on the
// filesystem holding path.
func diskFree(path string) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return fs.Bavail * uint64(fs.Bsize), nil
}
