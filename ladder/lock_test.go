//go:build unix

package main

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// The lock must survive garbage collections. Another process checks it,
// since flock locks are per open file, and this process holds it.
func TestLockSurvivesGarbageCollection(t *testing.T) {
	if os.Getenv("LADDER_LOCK_DIR") != "" {
		if err := lockDataDir(os.Getenv("LADDER_LOCK_DIR")); err == nil {
			os.Exit(0) // took the lock: it was released
		}
		os.Exit(3)
	}

	dir := t.TempDir()
	if err := lockDataDir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { lockFile.Close(); lockFile = nil }()
	runtime.GC()
	runtime.GC()

	cmd := exec.Command(os.Args[0], "-test.run=^TestLockSurvivesGarbageCollection$")
	cmd.Env = append(os.Environ(), "LADDER_LOCK_DIR="+dir)
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
		t.Errorf("another process should not get the lock: %v", err)
	}
}
