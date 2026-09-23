//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

func TestWorkspaceSignalTermination(t *testing.T) {
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("requires /bin/bash")
	}
	for _, tc := range []struct {
		name, command string
		exit          int
	}{
		{"SIGTERM", "kill -TERM $$", 143},
		{"SIGKILL", "kill -KILL $$", 137},
		{"nonzero exit", "exit 7", 7},
		{"successful exit", "exit 0", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMockE2BServer(t, func(code string) string {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/bash", "-c", code)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					return ndjsonLines(errorMsg("kernel execution failed", err.Error(), stderr.String()))
				}
				return ndjsonLines(stdoutMsg(stdout.String()), stderrMsg(stderr.String()))
			})
			defer srv.close()
			srv.process.start = runLocalProcessWithEnvdEvents
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			c := newMockedExecutor(t, srv, WithSandboxRunBase(base))
			command := "printf 'before-signal\\n'; printf 'diagnostic\\n' >&2; " + tc.command
			t.Run("RunProgram", func(t *testing.T) {
				result, err := c.Engine().Runner().RunProgram(context.Background(),
					codeexecutor.Workspace{Path: base}, codeexecutor.RunProgramSpec{
						Cmd: "/bin/sh", Args: []string{"-c", command}, Timeout: 5 * time.Second,
					})
				require.NoError(t, err)
				assert.Equal(t, tc.exit, result.ExitCode)
				assert.Equal(t, "before-signal\n", result.Stdout)
				assert.Equal(t, "diagnostic\n", result.Stderr)
				assert.False(t, result.TimedOut)
			})
			t.Run("workspace_exec", func(t *testing.T) {
				args, err := json.Marshal(map[string]any{"command": command, "timeout_sec": 5})
				require.NoError(t, err)
				result, err := workspaceexec.NewExecTool(c).Call(context.Background(), args)
				require.NoError(t, err)
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				var output struct {
					Status   string `json:"status"`
					Output   string `json:"output"`
					ExitCode *int   `json:"exit_code"`
				}
				require.NoError(t, json.Unmarshal(encoded, &output))
				assert.Equal(t, codeexecutor.ProgramStatusExited, output.Status)
				require.NotNil(t, output.ExitCode)
				assert.Equal(t, tc.exit, *output.ExitCode)
				assert.Contains(t, output.Output, "before-signal\n")
				assert.Contains(t, output.Output, "diagnostic\n")
			})
		})
	}
}

// runLocalProcessWithEnvdEvents follows the pinned envd Handler.Wait mapping:
// signal termination has Exited=false and ExitCode=-1, unlike a shell's $?.
func runLocalProcessWithEnvdEvents(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
	cmd := exec.CommandContext(ctx, req.Msg.Process.Cmd, req.Msg.Process.Args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := sendWorkspaceStart(stream, uint32(cmd.Process.Pid)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	waitErr := cmd.Wait()
	for _, data := range []*process.ProcessEvent_DataEvent{
		{Output: &process.ProcessEvent_DataEvent_Stdout{Stdout: stdout.Bytes()}},
		{Output: &process.ProcessEvent_DataEvent_Stderr{Stderr: stderr.Bytes()}},
	} {
		if err := stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
			Event: &process.ProcessEvent_Data{Data: data},
		}}); err != nil {
			return err
		}
	}
	end := &process.ProcessEvent_EndEvent{
		Exited: cmd.ProcessState.Exited(), ExitCode: int32(cmd.ProcessState.ExitCode()),
		Status: cmd.ProcessState.String(),
	}
	if waitErr != nil {
		message := waitErr.Error()
		end.Error = &message
	}
	return stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_End{End: end},
	}})
}
