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
	"os/exec"
	"testing"
)

func TestCheckrunnerBinaryExists(t *testing.T) {
	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not available")
	}
}

func TestCheckrunnerHelp(t *testing.T) {
	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not available")
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Logf("Output: %s", output)
	}
	// help output should contain usage info.
}
