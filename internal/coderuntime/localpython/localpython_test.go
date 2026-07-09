//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package localpython

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHardenedEnvUsesMinimalDefault(t *testing.T) {
	t.Setenv("SECRET_SHOULD_NOT_LEAK", "secret")
	require.ElementsMatch(t, []string{
		"PYTHONIOENCODING=utf-8",
		"PYTHONNOUSERSITE=1",
	}, HardenedEnv(nil))
}

func TestHardenedEnvFiltersDangerousAndForcedKeys(t *testing.T) {
	input := []string{
		"KEEP=1",
		"PYTHONPATH=/tmp/unsafe",
		"pythonpath=/tmp/unsafe-lower",
		"PYTHONHOME=/tmp/unsafe",
		"PYTHONSTARTUP=/tmp/startup.py",
		"PYTHONUSERBASE=/tmp/userbase",
		"LD_PRELOAD=/tmp/lib.so",
		"ld_preload=/tmp/lib-lower.so",
		"LD_LIBRARY_PATH=/tmp/lib",
		"LD_AUDIT=/tmp/audit.so",
		"DYLD_INSERT_LIBRARIES=/tmp/lib.dylib",
		"DYLD_LIBRARY_PATH=/tmp/lib",
		"PATH=/tmp/bin",
		"HOME=/tmp/home",
		"BASH_ENV=/tmp/bashrc",
		"PYTHONIOENCODING=latin1",
		"pythonioencoding=latin1",
		"PYTHONNOUSERSITE=0",
		"pythonnousersite=0",
		"malformed",
		"BAD KEY=value",
	}
	require.ElementsMatch(t, []string{
		"KEEP=1",
		"PYTHONIOENCODING=utf-8",
		"PYTHONNOUSERSITE=1",
	}, HardenedEnv(input))
}

func TestValidateCodeSize(t *testing.T) {
	require.NoError(t, ValidateCodeSize("return 1", 0))
	require.NoError(t, ValidateCodeSize(strings.Repeat("x", 100<<10), -1))
	require.ErrorContains(t, ValidateCodeSize("return 1", 4), "code exceeds 4 bytes")
}

func TestStartScriptRunsInTemporaryWorkDirAndCleansIt(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	proc, err := StartScript(
		context.Background(),
		Config{},
		"print('ok')",
		"guest.py",
		[]byte("import json, os; print(json.dumps({'cwd': os.getcwd()}))\n"),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	dir := proc.Dir
	require.DirExists(t, dir)
	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)

	var got struct {
		CWD string `json:"cwd"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got))
	requireSamePath(t, dir, got.CWD)
	require.NoError(t, proc.Wait())
	require.NoDirExists(t, dir)
}

func TestStartScriptUsesConfiguredWorkDirWithoutCleaningIt(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	workDir := t.TempDir()
	proc, err := StartScript(
		context.Background(),
		Config{WorkDir: workDir},
		"print('ok')",
		"guest.py",
		[]byte("import os; print(os.getcwd())\n"),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)
	require.NoError(t, proc.Wait())
	require.DirExists(t, workDir)
	requireSamePath(t, workDir, strings.TrimSpace(string(out)))
	require.NoFileExists(t, filepath.Join(workDir, "guest.py"))
}

func TestStartScriptEnforcesTimeout(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	start := time.Now()
	proc, err := StartScript(
		context.Background(),
		Config{Timeout: 50 * time.Millisecond},
		"while True: pass",
		"guest.py",
		[]byte("while True: pass\n"),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, proc.Stdout())
	err = proc.Wait()
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestStartScriptUsesHardenedEnvironment(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	proc, err := StartScript(
		context.Background(),
		Config{Env: []string{
			"KEEP=1",
			"PYTHONPATH=/tmp/unsafe",
			"PYTHONNOUSERSITE=0",
		}},
		"print('ok')",
		"guest.py",
		[]byte(`import json, os; print(json.dumps({"keep": os.getenv("KEEP"), "path": os.getenv("PYTHONPATH"), "nousersite": os.getenv("PYTHONNOUSERSITE")}))`),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)
	require.NoError(t, proc.Wait())

	var got map[string]*string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got))
	require.NotNil(t, got["keep"])
	require.Equal(t, "1", *got["keep"])
	require.Nil(t, got["path"])
	require.NotNil(t, got["nousersite"])
	require.Equal(t, "1", *got["nousersite"])
}

func TestStartScriptRejectsMissingName(t *testing.T) {
	_, err := StartScript(context.Background(), Config{}, "code", "", []byte("print('x')"), nil, nil, io.Discard)
	require.ErrorContains(t, err, "script name is required")
}

func TestStartScriptCleansScriptDirOnStartFailure(t *testing.T) {
	workDir := t.TempDir()
	_, err := StartScript(
		context.Background(),
		Config{Python: "definitely-not-a-python-executable", WorkDir: workDir},
		"code",
		"guest.py",
		[]byte("print('x')"),
		nil,
		nil,
		io.Discard,
	)
	require.Error(t, err)
	entries, readErr := os.ReadDir(workDir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestStartScriptSupportsRelativePythonPathWithTemporaryWorkDir(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	pythonPath, err = filepath.Abs(pythonPath)
	require.NoError(t, err)
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	relativePython, err := filepath.Rel(currentDir, pythonPath)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(relativePython))

	proc, err := StartScript(
		context.Background(),
		Config{Python: relativePython},
		"print('ok')",
		"guest.py",
		[]byte("print('ok')\n"),
		nil,
		nil,
		io.Discard,
	)
	require.NoError(t, err)
	out, err := io.ReadAll(proc.Stdout())
	require.NoError(t, err)
	require.NoError(t, proc.Wait())
	require.Equal(t, "ok", strings.TrimSpace(string(out)))
}

func requireSamePath(t *testing.T, want, got string) {
	t.Helper()
	resolvedWant, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	resolvedGot, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	require.Equal(t, resolvedWant, resolvedGot)
}
