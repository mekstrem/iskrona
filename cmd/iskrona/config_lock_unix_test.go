//go:build unix

package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func tryConfigLock(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func configLockBlocked(err error) bool { return errors.Is(err, unix.EWOULDBLOCK) }
