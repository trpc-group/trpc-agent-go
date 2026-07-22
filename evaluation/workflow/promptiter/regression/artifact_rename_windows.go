//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build windows

package regression

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func renameRoot(root *os.Root, oldPath, newPath string) error {
	oldName, err := windows.UTF16PtrFromString(filepath.Join(root.Name(), oldPath))
	if err != nil {
		return err
	}
	newName, err := windows.UTF16PtrFromString(filepath.Join(root.Name(), newPath))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(oldName, newName, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
