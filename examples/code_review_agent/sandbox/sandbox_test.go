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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSandbox_RunCommand_Success(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.RunCommand(context.Background(), "echo hello", DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if result.Output != "hello\n" {
		t.Errorf("Expected output 'hello\\n', got '%s'", result.Output)
	}

	if result.TimedOut {
		t.Error("Expected not timed out")
	}
}

func TestLocalSandbox_RunCommand_Failure(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.RunCommand(context.Background(), "false", DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if result.ExitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", result.ExitCode)
	}
}

func TestLocalSandbox_RunCommand_Timeout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping timeout test")
	}

	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	config := SandboxConfig{
		Timeout:         100 * time.Millisecond,
		OutputSizeLimit: 1024,
	}

	result, err := sandbox.RunCommand(context.Background(), "sleep 1", config)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if !result.TimedOut {
		t.Error("Expected timed out")
	}
}

func TestLocalSandbox_RunCommand_OutputLimit(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	config := SandboxConfig{
		Timeout:         10 * time.Second,
		OutputSizeLimit: 10,
	}

	result, err := sandbox.RunCommand(context.Background(), "echo '1234567890abcdef'", config)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if len(result.Output) > 30 {
		t.Errorf("Expected output to be truncated to ~10 chars, got %d", len(result.Output))
	}

	if !strings.Contains(result.Output, "[truncated]") {
		t.Error("Expected output to contain '[truncated]'")
	}
}

func TestNewLocalSandbox_PathTraversal(t *testing.T) {
	testCases := []string{
		"../evil",
		"../../etc/passwd",
		"./../secret",
		"path/../../traversal",
	}

	for _, tc := range testCases {
		_, err := NewLocalSandbox(tc)
		if err == nil {
			t.Errorf("Expected error for path traversal: %s", tc)
		}
	}
}

func TestNewLocalSandbox_InvalidPath(t *testing.T) {
	_, err := NewLocalSandbox("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("Expected error for non-existent path")
	}
}

func TestNewLocalSandbox_NotDirectory(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "sandbox_test")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, err = NewLocalSandbox(tmpFile.Name())
	if err == nil {
		t.Error("Expected error for non-directory path")
	}
}

func TestLocalSandbox_ExecuteScript(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sandbox_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho 'script output'"), 0755); err != nil {
		t.Fatalf("Failed to create script: %v", err)
	}

	sandbox, err := NewLocalSandbox(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.ExecuteScript(context.Background(), scriptPath, []string{"arg1", "arg2"}, DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to execute script: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Output, "script output") {
		t.Errorf("Expected output to contain 'script output', got '%s'", result.Output)
	}
}

func TestLocalSandbox_RunCommand_EmptyCommand(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.RunCommand(context.Background(), "", DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if result.ExitCode != -1 {
		t.Errorf("Expected exit code -1 for empty command, got %d", result.ExitCode)
	}

	if result.Error != "Empty command" {
		t.Errorf("Expected error 'Empty command', got '%s'", result.Error)
	}
}

func TestFilterEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"SECRET_KEY=super-secret",
		"API_KEY=sk-12345",
		"GO_VERSION=1.21",
	}

	whitelist := []string{"PATH", "HOME"}
	result := filterEnv(env, whitelist)

	if len(result) != 2 {
		t.Errorf("Expected 2 environment variables, got %d", len(result))
	}

	expected := map[string]bool{"PATH=/usr/bin": true, "HOME=/home/user": true}
	for _, e := range result {
		if !expected[e] {
			t.Errorf("Unexpected environment variable: %s", e)
		}
	}
}

func TestParseShellCommand(t *testing.T) {
	testCases := []struct {
		input    string
		expected []string
	}{
		{"echo hello", []string{"echo", "hello"}},
		{"go vet ./...", []string{"go", "vet", "./..."}},
		{"", []string{}},
		{"  ", []string{}},
		{"'single'", []string{"single"}},
		{`"double"`, []string{"double"}},
		{"cmd -a -b", []string{"cmd", "-a", "-b"}},
		{"echo 'hello world'", []string{"echo", "hello world"}},
		{"echo \"hello world\"", []string{"echo", "hello world"}},
		{"echo \"hello \\\"world\\\"\"", []string{"echo", `hello "world"`}},
		{"echo 'hello\\'world'", []string{"echo", "hello\\world"}},
	}

	for _, tc := range testCases {
		result := parseShellCommand(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("parseShellCommand(%q) = %v, expected %v", tc.input, result, tc.expected)
			continue
		}
		for i := range result {
			if result[i] != tc.expected[i] {
				t.Errorf("parseShellCommand(%q)[%d] = %q, expected %q", tc.input, i, result[i], tc.expected[i])
			}
		}
	}
}

func TestNewSandbox_UnsafeLocal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sandbox_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Setenv("UNSAFE_LOCAL_SANDBOX", "true")
	sandbox, err := NewSandbox(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create sandbox with UNSAFE_LOCAL_SANDBOX=true: %v", err)
	}
	defer sandbox.Close()

	if sandbox.GetType() != SandboxTypeLocal {
		t.Errorf("Expected sandbox type %s, got %s", SandboxTypeLocal, sandbox.GetType())
	}
}

func TestNewSandbox_SafeMode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sandbox_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	t.Setenv("UNSAFE_LOCAL_SANDBOX", "")
	_, err = NewSandbox(tmpDir)
	if err == nil {
		t.Error("Expected error when UNSAFE_LOCAL_SANDBOX is not set")
	}
}

func TestNewSandboxWithConfig_UnsafeFlag(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sandbox_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := SandboxConfig{UnsafeLocal: true}
	sandbox, err := NewSandboxWithConfig(tmpDir, config)
	if err != nil {
		t.Fatalf("Failed to create sandbox with UnsafeLocal=true: %v", err)
	}
	defer sandbox.Close()

	if sandbox.GetType() != SandboxTypeLocal {
		t.Errorf("Expected sandbox type %s, got %s", SandboxTypeLocal, sandbox.GetType())
	}
}
