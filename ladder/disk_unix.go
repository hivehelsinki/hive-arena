//go:build unix

package main

import "syscall"

// diskSpace returns the space available to the ladder and the total size of
// the file system holding path, in bytes
func diskSpace(path string) (free uint64, total uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), nil
}
