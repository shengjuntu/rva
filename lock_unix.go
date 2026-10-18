//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// Keep the inode: deleting an advisory lock file can let another process lock
// a different inode. Kernel releases this lock even after SIGKILL/container crash.
func lockData(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return func() { f.Close() }, nil
}
