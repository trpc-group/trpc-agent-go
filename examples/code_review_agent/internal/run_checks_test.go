// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunChecksScriptReturnsFailure(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/failing\n\ngo 1.21\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "failing_test.go"), []byte("package failing\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"expected\") }\n"), 0o600))
	script, err := filepath.Abs(filepath.Join("..", "skills", "code-review", "scripts", "run_checks.sh"))
	require.NoError(t, err)
	cmd := exec.Command("bash", bashPath(script), bashPath(repo))
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "failing go test returned a successful script status: %s", output)
	require.Contains(t, string(output), "=== Running go vet ===")
	require.Contains(t, string(output), "=== Running go test ===")
}

func bashPath(input string) string {
	volume := filepath.VolumeName(input)
	if len(volume) == 2 && volume[1] == ':' {
		return "/mnt/" + strings.ToLower(volume[:1]) + filepath.ToSlash(strings.TrimPrefix(input, volume))
	}
	return filepath.ToSlash(input)
}
