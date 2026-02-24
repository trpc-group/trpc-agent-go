//go:build !windows

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

	"github.com/creack/pty"
)

func startPTY(cmd *exec.Cmd) (*os.File, func() error, error) {
	if cmd == nil {
		return nil, nil, errors.New("nil command")
	}

	master, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	closeIO := func() error {
		return master.Close()
	}
	return master, closeIO, nil
}
