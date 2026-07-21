//go:build !windows

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openSecureAuditFile(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, errors.New("audit path cannot be a symbolic link")
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create audit file handle")
	}
	if err := validateSecureAuditFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validateSecureAuditFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened audit file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("audit path must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure audit file permissions: %w", err)
	}
	return nil
}
