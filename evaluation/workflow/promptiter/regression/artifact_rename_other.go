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
	"errors"
	"os"
)

func renameRoot(*os.Root, string, string) error {
	// Go 1.24 has no portable root-relative atomic rename operation for these
	// targets, so fail closed rather than falling back to an unrestricted path.
	return errors.New("atomic artifact replacement is unsupported on this platform")
}
