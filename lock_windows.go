//go:build windows

package main

import (
	"path/filepath"
	"syscall"
)

func lockData(dir string) (func(), error) {
	name, e := syscall.UTF16PtrFromString(filepath.Join(dir, "server.lock"))
	if e != nil {
		return nil, e
	}
	h, e := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return func() { syscall.CloseHandle(h) }, nil
}
