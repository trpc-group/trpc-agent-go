//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// ttyProbeEnv marks the inner run of this test, the one that actually has a
// controlling terminal to lose.
const ttyProbeEnv = "TRPC_HOSTEXEC_TTY_PROBE"

// Markers the inner run prints so the outer run can tell the two outcomes apart
// without depending on the shell's wording.
const (
	ttyReachableMarker = "hostexec-tty-reachable"
	ttyDetachedMarker  = "hostexec-tty-detached"
)

// ttyProbeResultPrefix labels the one line the inner run writes to the PTY, so
// the outer run reads the probe's verdict rather than scraping test output.
const ttyProbeResultPrefix = "hostexec-tty-probe:"

// ttyProbeSkipPrefix labels an unmet precondition in the inner run. Without it
// the outer run sees only a missing verdict and reports a failure, when what
// happened is that the probe never ran.
const ttyProbeSkipPrefix = "hostexec-tty-skip:"

// A nil stdin is not enough to keep a prompt away from a foreground command.
//
// Leaving cmd.Stdin nil points fd 0 at the null device, but the child still
// inherits the host process's controlling terminal, and the prompts that matter
// bypass fd 0 entirely: sudo, ssh, git, and Python's getpass open /dev/tty
// directly. Setpgid only moves the child into a background process group, which
// makes it worse — a read from the terminal there raises SIGTTIN and stops the
// child, so the prompt hangs until the run's timeout rather than failing.
//
// The test needs a controlling terminal to prove that, and a Go test process
// does not have one. So it re-runs itself under a PTY: pty.Start gives the inner
// run a new session with the PTY as its controlling terminal, and the inner run
// then goes through the ordinary foreground exec path and reports whether its
// child could still reach /dev/tty.
//
// Opening is the assertion rather than reading, because opening is the
// precondition for the hang and it fails fast in both directions — a read would
// make the pre-fix case prove itself by timing out.
func TestForegroundDetachesControllingTerminal(t *testing.T) {
	if os.Getenv(ttyProbeEnv) == "1" {
		runControllingTerminalProbe(t)
		return
	}
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}

	inner := exec.Command(
		os.Args[0],
		"-test.run=^TestForegroundDetachesControllingTerminal$",
		"-test.timeout=60s",
	)
	inner.Env = append(os.Environ(), ttyProbeEnv+"=1")

	master, err := pty.Start(inner)
	if err != nil {
		t.Skipf("no PTY available on this host: %v", err)
	}
	defer func() {
		_ = master.Close()
		_ = inner.Process.Kill()
		_, _ = inner.Process.Wait()
	}()

	output := readUntilExit(t, master, inner, 60*time.Second)

	if idx := strings.Index(output, ttyProbeSkipPrefix); idx >= 0 {
		t.Skipf("inner run could not run the probe: %s",
			strings.SplitN(output[idx:], "\n", 2)[0])
	}
	require.Contains(t, output, ttyProbeResultPrefix,
		"the inner run never reported a verdict; output:\n%s", output)
	require.Contains(t, output, ttyProbeResultPrefix+ttyDetachedMarker,
		"a foreground child must not be able to open the caller's terminal;\n"+
			"inner run output:\n%s", output)
	require.NotContains(t, output, ttyReachableMarker)
}

// runControllingTerminalProbe is the inner half. It has a controlling terminal
// because pty.Start gave it one, and it checks whether the foreground exec path
// passes that terminal on to the command it runs.
func runControllingTerminalProbe(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		skipProbe(t, err.Error())
	}
	// Without a controlling terminal of its own there is nothing for the child to
	// inherit, so the probe would pass for the wrong reason.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		skipProbe(t, fmt.Sprintf("inner run has no controlling terminal: %v", err))
	}
	_ = tty.Close()

	set, err := NewToolSet()
	require.NoError(t, err)
	defer set.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	execTool, _, _, _ := toolSetTools(t, set)
	out, err := execTool.Call(
		ctx,
		mustJSON(t, map[string]any{
			"command": "if (exec 3</dev/tty) 2>/dev/null; " +
				"then echo " + ttyReachableMarker + "; " +
				"else echo " + ttyDetachedMarker + "; fi",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)

	res := out.(map[string]any)
	// Report to the PTY before asserting, so the outer run sees the outcome
	// whichever way it went and can name it in its own failure message.
	fmt.Printf("%s%s\n", ttyProbeResultPrefix, outputField(res))

	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), ttyDetachedMarker)
}

// skipProbe tells the outer run over the PTY why the probe did not run, then
// skips. The marker is what keeps an unmet precondition from surfacing as a
// missing verdict.
func skipProbe(t *testing.T, reason string) {
	t.Helper()
	fmt.Printf("%s%s\n", ttyProbeSkipPrefix, reason)
	t.Skip(reason)
}

// readUntilExit drains the PTY until the inner run exits. A PTY read returns
// EIO rather than EOF once the last slave handle closes, so any read error ends
// the loop and the collected output is what the caller inspects.
func readUntilExit(
	t *testing.T,
	master *os.File,
	inner *exec.Cmd,
	timeout time.Duration,
) string {
	t.Helper()

	var builder strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				builder.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		_ = inner.Process.Kill()
		<-done
		t.Fatalf("inner run did not finish within %s; output so far:\n%s",
			timeout, builder.String())
	}
	_, _ = io.Copy(io.Discard, master)
	return builder.String()
}
