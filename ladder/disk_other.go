//go:build !unix

package main

import "errors"

func diskSpace(path string) (free uint64, total uint64, err error) {
	return 0, 0, errors.New("disk space is not available on this system")
}
