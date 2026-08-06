//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestGitClientRunPreservesCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell alias cancellation probe requires a Unix-like host")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	repo := newGitRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (gitClient{
		timeout:        5 * time.Second,
		maxOutputBytes: 1024,
		maxDiffBytes:   1024,
	}).run(
		ctx,
		repo,
		nil,
		"-c",
		"alias.pause=!sleep 5",
		"pause",
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("git cancellation error = %v, want context deadline exceeded", err)
	}
}
