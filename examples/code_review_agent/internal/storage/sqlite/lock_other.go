//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build windows || plan9 || js || wasip1

package sqlite

import (
	"fmt"
	"os"
)

// Non-Unix targets fail closed when another Store already owns the lock file.
func acquireDatabaseLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create sqlite lock %q: %w", path, err)
	}
	return file, nil
}

func releaseDatabaseLock(file *os.File) error {
	if file == nil {
		return nil
	}
	path := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
