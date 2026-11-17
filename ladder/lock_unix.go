//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDataDir takes the exclusive lock of the data directory, held until the
// process exits: only one process at a time may play matches and build
// images (the service, or a command like 'run'). Without it, a command would
// remove the containers of the service's current match.
func lockDataDir(dataDir string) error {
	file, err := os.OpenFile(filepath.Join(dataDir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return fmt.Errorf("another ladder process is playing matches with this data (the service?): stop it first")
	}
	// Kept open: the lock is released when the process exits
	return nil
}
