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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fileRenameInformation struct {
	replaceIfExists bool
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [windows.MAX_PATH]uint16
}

func renameRoot(root *os.Root, oldPath, newPath string) error {
	oldDir := filepath.Dir(oldPath)
	newDir := filepath.Dir(newPath)
	if oldDir != newDir {
		return fmt.Errorf("artifact rename crosses directories")
	}
	directory, err := root.Open(oldDir)
	if err != nil {
		return err
	}
	defer directory.Close()
	directoryHandle := windows.Handle(directory.Fd())

	oldName, err := windows.NewNTUnicodeString(filepath.Base(oldPath))
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: directoryHandle,
		ObjectName:    oldName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var sourceHandle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	if err := windows.NtCreateFile(
		&sourceHandle,
		windows.DELETE|windows.SYNCHRONIZE,
		&attributes,
		&status,
		&allocationSize,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	); err != nil {
		return err
	}
	defer windows.CloseHandle(sourceHandle)

	newName, err := windows.UTF16FromString(filepath.Base(newPath))
	if err != nil {
		return err
	}
	if len(newName) > len(fileRenameInformation{}.fileName) {
		return errors.New("artifact name is too long")
	}
	info := fileRenameInformation{
		replaceIfExists: true,
		rootDirectory:   directoryHandle,
		fileNameLength:  uint32((len(newName) - 1) * 2),
	}
	copy(info.fileName[:], newName)
	return windows.NtSetInformationFile(
		sourceHandle,
		&status,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		windows.FileRenameInformation,
	)
}
