//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"syscall"
)

func dropPrivileges() error {
	if err := syscall.Setgid(nonRootGID); err != nil {
		return fmt.Errorf("setgid(%d): %w", nonRootGID, err)
	}
	if err := syscall.Setuid(nonRootUID); err != nil {
		return fmt.Errorf("setuid(%d): %w", nonRootUID, err)
	}
	return nil
}
