//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build windows

package sqlite

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// copyDatabaseToWorkPath opens the final path itself, then rejects reparse
// points before copying through that stable handle.
func copyDatabaseToWorkPath(path string, workPath string) error {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fmt.Errorf("open sqlite path %q without following reparse points: %w", path, err)
	}
	source := os.NewFile(uintptr(handle), path)
	defer source.Close()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect sqlite path %q: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("sqlite path %q is a reparse point", path)
	}
	return copyDatabaseFile(source, workPath)
}
