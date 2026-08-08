//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"bytes"
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecCommandRunnerDoesNotUseShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf fixture is Unix-specific")
	}
	var stdout bytes.Buffer
	err := (ExecCommandRunner{}).Run(
		context.Background(),
		"printf",
		[]string{"%s", "$(printf injected); echo still-an-argument"},
		nil,
		&stdout,
	)
	require.NoError(t, err)
	require.Equal(t, "$(printf injected); echo still-an-argument", stdout.String())
}

func TestExecCommandRunnerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	err := (ExecCommandRunner{}).Run(ctx, "git", []string{"--version"}, nil, &stdout)
	require.Error(t, err)
}
