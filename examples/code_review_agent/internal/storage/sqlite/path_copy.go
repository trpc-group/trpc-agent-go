//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"fmt"
	"io"
	"os"
)

func copyDatabaseFile(source *os.File, workPath string) error {
	destination, err := os.OpenFile(workPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create sqlite work database: %w", err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		return fmt.Errorf("copy sqlite database to secure work path: %w", err)
	}
	if err := destination.Close(); err != nil {
		return fmt.Errorf("close sqlite work database: %w", err)
	}
	return nil
}
