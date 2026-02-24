//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"errors"
	"os"
	"os/exec"
)

func startPTY(cmd *exec.Cmd) (*os.File, func() error, error) {
	return nil, nil, errors.New("pty is not supported on windows")
}
