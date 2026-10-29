//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sqlite

import (
	"fmt"
	"os"
	"syscall"
)

// copyDatabaseToWorkPath copies the database through an O_NOFOLLOW descriptor.
// SQLite only opens the private work path, never the caller-controlled leaf.
func copyDatabaseToWorkPath(path string, workPath string) error {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("open sqlite path %q without following links: %w", path, err)
	}
	source := os.NewFile(uintptr(fd), path)
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("inspect sqlite path %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("sqlite path %q is not a regular file", path)
	}
	return copyDatabaseFile(source, workPath)
}
