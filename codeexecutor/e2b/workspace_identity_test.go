//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

func TestWorkspaceProcessIdentity(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires bash")
	}
	for _, kernelUser := range []string{"root", "custom"} {
		t.Run(kernelUser, func(t *testing.T) {
			srv := newMockE2BServer(t, func(code string) string {
				var stdout, stderr bytes.Buffer
				cmd := exec.Command("bash", "-c", code)
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					return ndjsonLines(errorMsg("kernel execution failed", err.Error(), stderr.String()))
				}
				return ndjsonLines(stdoutMsg(stdout.String()), stderrMsg(stderr.String()))
			})
			defer srv.close()
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			opts := []Option{WithSandboxRunBase(base)}
			if kernelUser == "custom" {
				opts = append(opts, WithHeaders(map[string]string{
					"authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(kernelUser+":")),
				}))
			}
			owner := newMockedExecutor(t, srv, opts...)
			ctx := context.Background()
			ws, err := owner.CreateWorkspace(ctx, "identity", codeexecutor.WorkspacePolicy{})
			require.NoError(t, err)
			srv.process.start = func(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
				if err := sendWorkspaceStart(stream, 1); err != nil {
					return err
				}
				user, _, ok := (&http.Request{Header: req.Header()}).BasicAuth()
				if !ok {
					user = "user" // The native template default differs from the kernel account.
				}
				if user != kernelUser {
					// Model a foreign user's r-x access to a kernel-owned 0755
					// directory without requiring privileged UID changes locally.
					runs := filepath.Join(ws.Path, codeexecutor.DirRuns)
					if err := os.Chmod(runs, 0o555); err != nil {
						return err
					}
					defer os.Chmod(runs, 0o755)
					if os.Geteuid() == 0 {
						return sendWorkspaceResult(stream, "", "mkdir: permission denied", 1)
					}
				}
				cmd := exec.CommandContext(ctx, req.Msg.Process.Cmd, req.Msg.Process.Args...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				var exitErr *exec.ExitError
				var exit int32
				if errors.As(err, &exitErr) {
					exit = int32(exitErr.ExitCode())
				} else if err != nil {
					return err
				}
				return sendWorkspaceResult(stream, stdout.String(), stderr.String(), exit)
			}
			checkWorkspaceProcessFiles(t, ctx, owner, ws)
			// Reconnect after the workspace and its private files already exist.
			connected := newMockedExecutor(t, srv, append(opts, WithSandboxID(owner.SandboxID()))...)
			checkWorkspaceProcessFiles(t, ctx, connected, ws)
		})
	}
}

// checkWorkspaceProcessFiles exercises the same filesystem/process sequence in
// local protocol tests and the credential-gated live integration suite.
func checkWorkspaceProcessFiles(t *testing.T, ctx context.Context, c *CodeExecutor, ws codeexecutor.Workspace) {
	t.Helper()
	require.NoError(t, c.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path: "work/private.txt", Content: []byte("input"), Mode: 0o600,
	}}))
	result, err := c.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh", Args: []string{"-c", `set -e; printf ':edited' >> work/private.txt; cat work/private.txt > out/result.txt`},
		CleanEnv: true,
	})
	require.NoError(t, err)
	require.Zero(t, result.ExitCode, result.Stderr)
	// This copies a private file through the kernel, then commits metadata
	// through RunProgram. Repeated calls also update existing metadata.
	require.NoError(t, c.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "workspace://work/private.txt", To: "work/staged.txt",
	}}))
	result, err = c.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "/bin/sh", Args: []string{"-c", `set -e; printf ':staged' >> work/staged.txt; cat work/staged.txt > out/result.txt`},
	})
	require.NoError(t, err)
	require.Zero(t, result.ExitCode, result.Stderr)
	mf, err := c.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs: []string{"out/result.txt", codeexecutor.MetaFileName}, Inline: true,
	})
	require.NoError(t, err)
	require.Len(t, mf.Files, 2)
	contents := make(map[string]string)
	for _, f := range mf.Files {
		contents[f.Name] = f.Content
	}
	require.Equal(t, "input:edited:staged", contents["out/result.txt"])
	var md codeexecutor.WorkspaceMetadata
	require.NoError(t, json.Unmarshal([]byte(contents[codeexecutor.MetaFileName]), &md))
	require.NotEmpty(t, md.Inputs)
	require.Equal(t, "work/staged.txt", md.Inputs[len(md.Inputs)-1].To)
}
