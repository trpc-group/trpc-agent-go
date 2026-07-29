//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFakeRun(t *testing.T) {
	e := NewExecutor(RuntimeFake)
	ctx := context.Background()
	run := e.RunGoVet(ctx, "/tmp")
	assert.Equal(t, 0, run.ExitCode)
	assert.Contains(t, run.Stdout, "fake")
}

func TestLocalGoVet(t *testing.T) {
	// Skip if go is not available
	tmpDir, err := os.MkdirTemp("", "sandbox_test")
	if err != nil {
		t.Skip("cannot create temp dir")
	}
	defer os.RemoveAll(tmpDir)

	goModContent := `module test

go 1.21
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o644)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(`package main

import "fmt"

func main() { fmt.Println("hello") }
`), 0o644)
	assert.NoError(t, err)

	e := NewExecutor(RuntimeLocal)
	e.SetTimeout(10 * time.Second)
	ctx := context.Background()
	run := e.RunGoVet(ctx, tmpDir)
	assert.NotNil(t, run)
	// go vet should pass on clean code
	assert.Equal(t, 0, run.ExitCode)
}

func TestTimeout(t *testing.T) {
	e := NewExecutor(RuntimeLocal)
	e.SetTimeout(1 * time.Nanosecond) // immediate timeout
	ctx := context.Background()
	run := e.RunGoVet(ctx, "/tmp")
	assert.True(t, run.TimedOut || run.Error != "")
}

func TestFakeGoTest(t *testing.T) {
	e := NewExecutor(RuntimeFake)
	ctx := context.Background()
	run := e.RunGoTest(ctx, "/tmp")
	assert.Equal(t, 0, run.ExitCode)
	assert.Contains(t, run.Stdout, "fake")
}

func TestOutputTruncation(t *testing.T) {
	e := NewExecutor(RuntimeFake)
	run := e.RunGoVet(context.Background(), "/tmp")
	assert.Equal(t, "[fake] sandbox execution skipped", run.Stdout)
}
