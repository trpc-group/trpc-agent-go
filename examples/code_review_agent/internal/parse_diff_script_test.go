//
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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDiffScriptDecodesGitQuotedPaths(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	diffPath := filepath.Join(t.TempDir(), "quoted.diff")
	diff := "diff --git \"a/\\346\\265\\213\\350\\257\\225 file.go\" \"b/\\346\\265\\213\\350\\257\\225 file.go\"\n" +
		"--- \"a/\\346\\265\\213\\350\\257\\225 file.go\"\n" +
		"+++ \"b/\\346\\265\\213\\350\\257\\225 file.go\"\n" +
		"@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git \"a/control\\tname.go\" \"b/control\\tname.go\"\n" +
		"--- \"a/control\\tname.go\"\n" +
		"+++ \"b/control\\tname.go\"\n" +
		"@@ -0,0 +1 @@\n+added\n"
	require.NoError(t, os.WriteFile(diffPath, []byte(diff), 0o600))

	script := filepath.Join("..", "skills", "code-review", "scripts", "parse_diff.sh")
	output, err := exec.Command("bash", script, diffPath).CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "测试 file.go")
	require.Contains(t, string(output), "测试 file.go: +1 lines")
	require.Contains(t, string(output), "测试 file.go: -1 lines")
	require.Contains(t, string(output), "control\tname.go")
	require.NotContains(t, string(output), `\346`)
}
