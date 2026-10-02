//go:build !windows

package storage

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockInstanceFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
