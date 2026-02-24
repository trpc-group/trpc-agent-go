package octool

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecTool_Foreground(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := NewExecTool("exec", mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo hello",
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, res.Output, "hello")
	require.Equal(t, 0, res.ExitCode)
}

func TestExecTool_YieldBackgroundAndPoll(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := NewExecTool("exec", mgr)
	procTool := NewProcessTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo start; sleep 0.2; echo end",
		"yieldMs": 10,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	deadline := time.Now().Add(2 * time.Second)
	var all string
	for time.Now().Before(deadline) {
		pollArgs := mustJSON(t, map[string]any{
			"action":    "poll",
			"sessionId": res.SessionID,
		})
		pollAny, err := procTool.Call(context.Background(), pollArgs)
		require.NoError(t, err)

		poll := pollAny.(processPoll)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			require.Contains(t, all, "start")
			require.Contains(t, all, "end")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestProcessTool_Submit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := NewExecTool("exec", mgr)
	procTool := NewProcessTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":    `read -r line; echo got:$line`,
		"background": true,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	submitArgs := mustJSON(t, map[string]any{
		"action":    "submit",
		"sessionId": res.SessionID,
		"data":      "hi",
	})
	_, err = procTool.Call(context.Background(), submitArgs)
	require.NoError(t, err)

	deadline := time.Now().Add(2 * time.Second)
	var all string
	for time.Now().Before(deadline) {
		pollArgs := mustJSON(t, map[string]any{
			"action":    "poll",
			"sessionId": res.SessionID,
		})
		pollAny, err := procTool.Call(context.Background(), pollArgs)
		require.NoError(t, err)

		poll := pollAny.(processPoll)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			require.Contains(t, all, "got:hi")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
