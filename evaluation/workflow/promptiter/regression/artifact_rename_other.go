//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !unix && !windows

package regression

import (
	"os"
	"path/filepath"
)

func renameRoot(root *os.Root, oldPath, newPath string) error {
	return os.Rename(filepath.Join(root.Name(), oldPath), filepath.Join(root.Name(), newPath))
}
