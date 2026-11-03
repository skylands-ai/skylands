// Package filelock holds an exclusive lock on a file for as long as a
// process runs. The kernel releases the lock when the process exits,
// however it exits, so a lock left by a process that died never needs
// clearing, and the lock file itself is never removed: removing it would
// let a third process lock a new file while a second still waits on the
// old one.
package filelock

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// HeldError means another process holds the lock.
type HeldError struct {
	Path string
	// PID is the process that holds it, as that process recorded it, or 0
	// if it has not yet.
	PID int
}

func (e *HeldError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("%s is locked by process %d", e.Path, e.PID)
	}
	return e.Path + " is locked by another process"
}

// Lock is a lock this process holds.
type Lock struct {
	f *os.File
}

// Acquire takes the lock on the file at path, creating the file if need
// be, without waiting. If another process holds it, the error is a
// *HeldError. On platforms without flock, Acquire always succeeds.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		held := errors.Is(err, errHeld)
		pid := readPID(f)
		_ = f.Close()
		if held {
			return nil, &HeldError{Path: path, PID: pid}
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	// The holder's process ID is for the message the next process gets.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}

// Release gives the lock up.
func (l *Lock) Release() error {
	_ = l.f.Truncate(0)
	return l.f.Close()
}

func readPID(f *os.File) int {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	pid, err := strconv.Atoi(string(bytes.TrimSpace(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}
