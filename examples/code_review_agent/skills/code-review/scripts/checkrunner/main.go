//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// checkrunner is a security-hardened wrapper that executes go vet / go test
// inside a sandbox container. It drops privileges before running the checked
// code, enforces timeouts, and returns structured output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type checkResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	TimedOut   bool   `json:"timed_out"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

const (
	nonRootUID     = 65532
	nonRootGID     = 65532
	maxOutputBytes = 1 << 20 // 1MB
)

func main() {
	mode := flag.String("mode", "", "Check mode: vet, test")
	dir := flag.String("dir", "/workspace", "Working directory")
	timeoutSec := flag.Int("timeout", 30, "Timeout in seconds")
	flag.Parse()

	if *mode == "" {
		fmt.Fprintln(os.Stderr, "Usage: checkrunner -mode <vet|test> [-dir <path>] [-timeout <seconds>]")
		os.Exit(2)
	}

	// Drop privileges before executing any checked code.
	if err := dropPrivileges(); err != nil {
		result := checkResult{
			Command:  *mode,
			ExitCode: 126,
			Error:    fmt.Sprintf("privilege drop failed: %v", err),
		}
		outputJSON(result)
		os.Exit(126)
	}

	result := runCheck(*mode, *dir, time.Duration(*timeoutSec)*time.Second)
	outputJSON(result)
	if result.ExitCode != 0 {
		os.Exit(1)
	}
}

func runCheck(mode, dir string, timeout time.Duration) checkResult {
	var cmd *exec.Cmd
	switch mode {
	case "vet":
		cmd = exec.Command("go", "vet", "./...")
	case "test":
		cmd = exec.Command("go", "test", "-count=1", "-timeout=20s", "./...")
	default:
		return checkResult{
			Command:  mode,
			ExitCode: 2,
			Error:    fmt.Sprintf("unknown mode: %s", mode),
		}
	}
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=/usr/local/go/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"GOPATH=/tmp/go",
		"GOCACHE=/tmp/go-cache",
		"GOMODCACHE=/tmp/go-mod-cache",
		"GOFLAGS=-mod=mod",
	}

	result := checkResult{
		Command: fmt.Sprintf("go %s ./...", mode),
	}

	start := time.Now()

	var stdout, stderr io.ReadCloser
	var err error
	stdout, err = cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Sprintf("stdout pipe: %v", err)
		result.ExitCode = 1
		return result
	}
	stderr, err = cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		result.Error = fmt.Sprintf("stderr pipe: %v", err)
		result.ExitCode = 1
		return result
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		result.Error = fmt.Sprintf("start: %v", err)
		result.ExitCode = 1
		return result
	}

	done := make(chan struct{})
	var stdoutBuf, stderrBuf []byte
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		stdoutBuf, _ = io.ReadAll(io.LimitReader(stdout, maxOutputBytes/2))
	}()
	go func() {
		defer wg.Done()
		stderrBuf, _ = io.ReadAll(io.LimitReader(stderr, maxOutputBytes/2))
	}()

	go func() {
		cmd.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	select {
	case <-done:
		timer.Stop()
	case <-timer.C:
		result.TimedOut = true
		killProcess(cmd)
	}

	stdout.Close()
	stderr.Close()
	wg.Wait()
	result.DurationMs = time.Since(start).Milliseconds()

	if result.TimedOut {
		result.Stderr = fmt.Sprintf("timeout after %s", timeout)
		return result
	}

	state := cmd.ProcessState
	if state != nil {
		ws, ok := state.Sys().(syscall.WaitStatus)
		if ok {
			result.ExitCode = ws.ExitStatus()
		}
	}

	result.Stdout = truncateBytes(stdoutBuf)
	result.Stderr = truncateBytes(stderrBuf)
	return result
}

func truncateBytes(b []byte) string {
	if len(b) > int(maxOutputBytes/2) {
		return string(b[:maxOutputBytes/2]) + "\n... [truncated]"
	}
	return string(b)
}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		cmd.Process.Kill()
	}
}

func outputJSON(result checkResult) {
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"marshal: %v"}`, err)
		return
	}
	fmt.Println(string(data))
}
