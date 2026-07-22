//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Command checkrunner executes one fixed, bounded sandbox check.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	targetUID            = 65532
	runnerErrorExitCode  = 2
	targetDirectoryCount = 4
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
	resultOverheadBytes  = 16 << 10
	defaultOutputLimit   = 64 << 10
	maxOutputLimit       = 1 << 20
	defaultTimeout       = 60 * time.Second
	maxTimeout           = 90 * time.Second
	terminationGrace     = 2 * time.Second
)

var resultPattern = regexp.MustCompile(`^result-[a-f0-9]{16}\.json$`)

type config struct {
	checkID, resultName, cwd string
	timeout                  time.Duration
	outputLimit              int64
	configureProcess         func(*exec.Cmd)
	prepare                  func() error
}

type checkResult struct {
	CheckID         string `json:"check_id"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	DurationMS      int64  `json:"duration_ms"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	Error           string `json:"error"`
}

func main() {
	os.Exit(mainCode(os.Args[1:], os.Stderr, run))
}

func mainCode(args []string, stderr io.Writer, runner func([]string) error) int {
	if err := runner(args); err != nil {
		if _, writeErr := fmt.Fprintln(stderr, "checkrunner:", err); writeErr != nil {
			return runnerErrorExitCode
		}
		return runnerErrorExitCode
	}
	return 0
}

func run(args []string) error {
	return runWith(args, execute, writeResult)
}

func runWith(args []string, executeCheck func(config, []string) checkResult, saveResult func(string, checkResult) error) error {
	value, err := parseConfig(args)
	if err != nil {
		return err
	}
	command, err := fixedCommand(value.checkID)
	if err != nil {
		return err
	}
	result := executeCheck(value, command)
	return saveResult(value.resultName, result)
}

func parseConfig(args []string) (config, error) {
	value := config{timeout: defaultTimeout, outputLimit: defaultOutputLimit, cwd: os.Getenv("CR_REPO_DIR")}
	set := flag.NewFlagSet("cr-checkrunner", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&value.checkID, "check", "", "fixed check ID")
	set.StringVar(&value.resultName, "result", "", "opaque result basename")
	set.DurationVar(&value.timeout, "timeout", value.timeout, "inner timeout")
	set.Int64Var(&value.outputLimit, "output-limit", value.outputLimit, "bytes retained per stream")
	if err := set.Parse(args); err != nil {
		return config{}, err
	}
	if len(set.Args()) != 0 {
		return config{}, errors.New("positional arguments are forbidden")
	}
	if !resultPattern.MatchString(value.resultName) {
		return config{}, errors.New("invalid result basename")
	}
	if !workspacePath(value.cwd, "work") {
		return config{}, errors.New("CR_REPO_DIR is not a staged repo path")
	}
	if value.timeout <= 0 || value.timeout > maxTimeout {
		return config{}, errors.New("timeout outside allowed range")
	}
	if value.outputLimit <= 0 || value.outputLimit > maxOutputLimit {
		return config{}, errors.New("output limit outside allowed range")
	}
	return value, nil
}

func fixedCommand(checkID string) ([]string, error) {
	switch checkID {
	case "go-test":
		return []string{"go", "test", "-mod=readonly", "./..."}, nil
	case "go-vet":
		return []string{"go", "vet", "-mod=readonly", "./..."}, nil
	default:
		return nil, fmt.Errorf("unknown check ID %q", checkID)
	}
}

func execute(value config, argv []string) checkResult {
	prepare := value.prepare
	if prepare == nil {
		prepare = prepareTargetDirectories
	}
	return executeWithPreparation(value, argv, prepare)
}

func executeWithPreparation(value config, argv []string, prepare func() error) checkResult {
	started := time.Now()
	if err := prepare(); err != nil {
		return failedResult(value.checkID, started, err)
	}
	timedCtx, cancel := context.WithTimeout(context.Background(), value.timeout)
	defer cancel()
	// #nosec G204 -- argv comes only from fixedCommand's closed check ID map.
	cmd := exec.CommandContext(timedCtx, argv[0], argv[1:]...)
	cmd.Dir = value.cwd
	cmd.Env = targetEnvironment()
	cmd.WaitDelay = terminationGrace
	if value.configureProcess == nil {
		configureTargetProcess(cmd, targetUID)
	} else {
		value.configureProcess(cmd)
	}
	stdout := &boundedWriter{limit: value.outputLimit}
	stderr := &boundedWriter{limit: value.outputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcess(cmd.Process)
	}
	waitErr := cmd.Run()
	timedOut := errors.Is(timedCtx.Err(), context.DeadlineExceeded)
	result := checkResult{
		CheckID: value.checkID, ExitCode: exitCode(cmd), TimedOut: timedOut,
		DurationMS: time.Since(started).Milliseconds(), Stdout: stdout.String(), Stderr: stderr.String(),
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
	}
	if waitErr != nil && (timedOut || !isExitError(waitErr)) {
		result.Error = waitErr.Error()
	}
	return result
}

func isExitError(err error) bool { var exitErr *exec.ExitError; return errors.As(err, &exitErr) }

func exitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

func failedResult(checkID string, started time.Time, err error) checkResult {
	return checkResult{CheckID: checkID, ExitCode: -1, DurationMS: time.Since(started).Milliseconds(), Error: err.Error()}
}

func targetEnvironment() []string {
	keys := []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "TMPDIR", "GOMAXPROCS"}
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func prepareTargetDirectories() error {
	paths := make([]string, 0, targetDirectoryCount)
	for _, key := range []string{"HOME", "GOCACHE", "GOMODCACHE", "TMPDIR"} {
		path := filepath.ToSlash(filepath.Clean(os.Getenv(key)))
		if !strings.HasPrefix(path, "/tmp/cr-target/") {
			return fmt.Errorf("%s is outside target tmp root", key)
		}
		paths = append(paths, path)
	}
	return makeTargetDirectories(paths, targetUID)
}

func writeResult(name string, result checkResult) error {
	dir := os.Getenv("CR_RESULT_DIR")
	if !workspacePath(dir, "out") {
		return errors.New("CR_RESULT_DIR is not a workspace output path")
	}
	if err := os.MkdirAll(dir, privateDirectoryMode); err != nil {
		return err
	}
	if err := os.Chmod(dir, privateDirectoryMode); err != nil {
		return err
	}
	return writeResultAt(dir, name, result)
}

func writeResultAt(dir, name string, result checkResult) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(encoded) > 2*maxOutputLimit+resultOverheadBytes {
		return errors.New("result exceeds hard limit")
	}
	destination := filepath.Join(dir, name)
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return errors.Join(err, os.Remove(destination))
	}
	return nil
}

func workspacePath(value, leaf string) bool {
	clean := filepath.ToSlash(filepath.Clean(value))
	if !strings.HasPrefix(clean, "/tmp/run/ws_cr-") {
		return false
	}
	relative := strings.TrimPrefix(clean, "/tmp/run/")
	return strings.HasSuffix(clean, "/"+leaf) && !strings.Contains(relative, "../")
}

type boundedWriter struct {
	buffer         bytes.Buffer
	limit, written int64
	truncated      bool
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	original := len(data)
	remaining := w.limit - w.written
	if remaining > 0 {
		keep := int64(len(data))
		if keep > remaining {
			keep = remaining
		}
		if _, err := w.buffer.Write(data[:keep]); err != nil {
			return 0, err
		}
		w.written += keep
	}
	if int64(original) > remaining {
		w.truncated = true
	}
	return original, nil
}

func (w *boundedWriter) String() string { return w.buffer.String() }
