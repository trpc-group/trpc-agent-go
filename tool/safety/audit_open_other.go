//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build plan9

package safety

import (
	"errors"
	"os"
)

func openAuditFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil &&
		info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("audit path is a symbolic link")
	}
	file, err := os.OpenFile(
		path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600,
	)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("audit path is not a regular file")
	}
	return file, nil
}

func setAuditFilePermissions(file *os.File) error {
	return file.Chmod(0o600)
}
