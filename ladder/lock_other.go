//go:build !unix

package main

// lockDataDir does nothing on systems without flock
func lockDataDir(dataDir string) error { return nil }
