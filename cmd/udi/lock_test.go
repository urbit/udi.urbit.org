package main

import (
	"strings"
	"testing"
)

func TestOperationLockRejectsConcurrentCommand(t *testing.T) {
	root := t.TempDir()
	first, err := acquireOperationLock(root, "refresh")
	if err != nil {
		t.Fatalf("first acquireOperationLock() error = %v", err)
	}
	defer first.Release()
	if _, err := acquireOperationLock(root, "discover"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second acquireOperationLock() error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second, err := acquireOperationLock(root, "build")
	if err != nil {
		t.Fatalf("acquire after release error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}
