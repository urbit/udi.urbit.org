package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type operationLock struct {
	file *os.File
}

func acquireOperationLock(root, command string) (*operationLock, error) {
	path := filepath.Join(root, ".udi-operation.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operation lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("another UDI build, discover, or refresh command is already running (lock: %s)", path)
		}
		return nil, fmt.Errorf("acquire operation lock %s: %w", path, err)
	}
	if err := file.Truncate(0); err != nil {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, fmt.Errorf("truncate operation lock %s: %w", path, err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, fmt.Errorf("seek operation lock %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d command=%s started=%s\n", os.Getpid(), command, time.Now().UTC().Format(time.RFC3339)); err != nil {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, fmt.Errorf("write operation lock %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, fmt.Errorf("sync operation lock %s: %w", path, err)
	}
	return &operationLock{file: file}, nil
}

func (lock *operationLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	path := lock.file.Name()
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN); err != nil {
		lock.file.Close()
		return fmt.Errorf("release operation lock %s: %w", path, err)
	}
	if err := lock.file.Close(); err != nil {
		return fmt.Errorf("close operation lock %s: %w", path, err)
	}
	lock.file = nil
	return nil
}
