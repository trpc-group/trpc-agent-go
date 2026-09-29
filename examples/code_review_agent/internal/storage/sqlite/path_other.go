//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build plan9 || js || wasip1

package sqlite

import (
	"fmt"
	"os"
)

// copyDatabaseToWorkPath fails closed for existing databases on targets that
// cannot open a leaf without following links or reparse points.
func copyDatabaseToWorkPath(path string, workPath string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		source, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return fmt.Errorf("create sqlite path %q: %w", path, createErr)
		}
		defer source.Close()
		return copyDatabaseFile(source, workPath)
	}
	if err != nil {
		return fmt.Errorf("inspect sqlite path %q: %w", path, err)
	}
	return fmt.Errorf("existing sqlite path %q is unsupported on this platform without no-follow semantics (mode %s)", path, info.Mode())
}
