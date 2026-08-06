//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build unix

package regression

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

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
	return unix.Renameat(int(directory.Fd()), filepath.Base(oldPath), int(directory.Fd()), filepath.Base(newPath))
}
