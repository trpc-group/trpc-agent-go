//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const helperExecutionTimeout = 5 * time.Second

func TestFixedCommand(t *testing.T) {
	for _, test := range []struct {
		id, verb string
		wantErr  bool
	}{{"go-test", "test", false}, {"go-vet", "vet", false}, {"custom", "", true}} {
		command, err := fixedCommand(test.id)
		if (err != nil) != test.wantErr {
			t.Fatalf("fixedCommand(%q) error = %v", test.id, err)
		}
		if !test.wantErr && (len(command) != 4 || command[1] != test.verb || command[2] != "-mod=readonly") {
			t.Fatalf("fixedCommand(%q) = %v", test.id, command)
		}
	}
}

func TestMainCode(t *testing.T) {
	var output bytes.Buffer
	if code := mainCode(nil, &output, func([]string) error { return nil }); code != 0 {
		t.Fatalf("mainCode success = %d", code)
	}
	if code := mainCode(nil, &output, func([]string) error { return errors.New("failed") }); code != runnerErrorExitCode || !strings.Contains(output.String(), "failed") {
		t.Fatalf("mainCode failure = %d, %q", code, output.String())
	}
	if code := mainCode(nil, errorWriter{}, func([]string) error { return errors.New("failed") }); code != runnerErrorExitCode {
		t.Fatalf("mainCode write failure = %d", code)
	}
	if err := run(nil); err == nil {
		t.Fatal("run accepted missing arguments")
	}
}

func TestParseConfigRejectsUnsafeInputs(t *testing.T) {
	t.Setenv("CR_REPO_DIR", validRepoDir)
	if _, err := parseConfig(validArgs("go-vet")); err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	for _, args := range append([][]string{
		{"--check", "go-vet", "--result", "../result.json"},
		{"--check", "go-vet", "--result", "result-0123456789abcdef.json", "shell"},
	}, invalidConfigArgs()...) {
		if _, err := parseConfig(args); err == nil {
			t.Fatalf("parseConfig(%v) error = nil", args)
		}
	}
	for _, test := range []struct {
		path, leaf string
		want       bool
	}{{"", "repo", false}, {"/tmp/run/other/repo", "repo", false}, {"/tmp/run/ws_cr-test_1/../repo", "repo", false},
		{"/tmp/run/ws_cr-test_1/work", "work", true}, {"/tmp/run/ws_cr-test_1/out", "out", true}} {
		if got := workspacePath(test.path, test.leaf); got != test.want {
			t.Fatalf("workspacePath(%q, %q) = %v", test.path, test.leaf, got)
		}
	}
}

func TestBoundedWriterDrainsAndTruncates(t *testing.T) {
	for _, test := range []struct {
		input, want string
		limit       int64
		truncated   bool
	}{{"abcdefgh", "abcd", 4, true}, {"ok", "ok", defaultOutputLimit, false}} {
		writer := &boundedWriter{limit: test.limit}
		n, err := writer.Write([]byte(test.input))
		if err != nil || n != len(test.input) || writer.String() != test.want || writer.truncated != test.truncated {
			t.Fatalf("Write(%q) = %d, %v; %q %v", test.input, n, err, writer.String(), writer.truncated)
		}
	}
}

func TestExecuteWithPreparation(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func() error
		command     []string
		wantExit    int
		wantTimeout bool
		wantError   bool
	}{
		{"success", func() error { return nil }, helperCommand("success"), 0, false, false},
		{"exit", func() error { return nil }, helperCommand("exit"), 7, false, false},
		{"prepare", func() error { return errors.New("prepare failed") }, helperCommand("success"), -1, false, true},
		{"timeout", func() error { return nil }, helperCommand("timeout"), -1, true, true},
		{"missing", func() error { return nil }, []string{"missing-command"}, -1, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := testConfig(t)
			value.timeout = 50 * time.Millisecond
			if test.name != "timeout" {
				value.timeout = helperExecutionTimeout
			}
			result := executeWithPreparation(value, test.command, test.prepare)
			if !result.TimedOut && result.ExitCode != test.wantExit || result.TimedOut != test.wantTimeout || (result.Error != "") != test.wantError {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestExecuteWrapper(t *testing.T) {
	value := testConfig(t)
	value.prepare = func() error { return nil }
	result := execute(value, helperCommand("success"))
	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("execute() = %#v", result)
	}
}

func helperCommand(mode string) []string {
	return []string{os.Args[0], "-test.run=TestHelperProcess", "--", mode}
}

func TestHelperProcess(t *testing.T) {
	mode, ok := helperMode(os.Args)
	if !ok {
		return
	}
	switch mode {
	case "success":
		os.Exit(0)
	case "exit":
		os.Exit(7)
	case "timeout":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func helperMode(args []string) (string, bool) {
	for index := range args {
		if args[index] == "--" && index+1 < len(args) {
			return args[index+1], true
		}
	}
	return "", false
}

func TestWriteResultAt(t *testing.T) {
	dir := t.TempDir()
	name := "result-0123456789abcdef.json"
	if err := writeResultAt(dir, name, checkResult{CheckID: "go-test"}); err != nil {
		t.Fatalf("writeResultAt() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || !strings.Contains(string(data), "go-test") {
		t.Fatalf("result=%q error=%v", data, err)
	}
	if err := writeResultAt(dir, name, checkResult{}); err == nil {
		t.Fatal("existing result overwritten")
	}
}

func TestWriteResultUsesTrustedOutputDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trusted runner output path is a Linux container contract")
	}
	dir := filepath.Join("/tmp/run", fmt.Sprintf("ws_cr-test_%d", os.Getpid()), "out")
	if err := os.RemoveAll(filepath.Dir(dir)); err != nil {
		t.Fatalf("remove stale result fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Dir(dir)); err != nil {
			t.Errorf("remove result fixture: %v", err)
		}
	})
	t.Setenv("CR_RESULT_DIR", dir)
	name := "result-0123456789abcdef.json"
	if err := writeResult(name, checkResult{CheckID: "go-test"}); err != nil {
		t.Fatalf("writeResult() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("stat result: %v", err)
	}
}

func TestExitErrorRecognition(t *testing.T) {
	commandArgs := helperCommand("exit")
	// helperCommand executes only this test binary with fixed arguments.
	//nolint:gosec
	command := exec.Command(commandArgs[0], commandArgs[1:]...)
	if err := command.Run(); err == nil || !isExitError(err) {
		t.Fatalf("exit handling error = %v", err)
	}
}

func TestTargetEnvironmentAndDirectories(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Dir(root), 0711); err != nil {
			t.Fatalf("make temporary parent traversable: %v", err)
		}
		if err := os.Chown(root, int(targetUID), int(targetUID)); err != nil {
			t.Skipf("target UID test requires chown permission: %v", err)
		}
	}
	paths := []string{filepath.Join(root, "one"), filepath.Join(root, "two")}
	if err := makeTargetDirectories(paths, targetUID); err != nil {
		t.Fatalf("makeTargetDirectories() error = %v", err)
	}
}

func TestRunWith(t *testing.T) {
	t.Setenv("CR_REPO_DIR", validRepoDir)
	executed, saved := false, false
	err := runWith(validArgs("go-test"), func(_ config, command []string) checkResult {
		executed = len(command) > 0
		return checkResult{CheckID: "go-test"}
	}, func(name string, result checkResult) error {
		saved = name != "" && result.CheckID == "go-test"
		return nil
	})
	if err != nil || !executed || !saved {
		t.Fatalf("runWith() error=%v executed=%v saved=%v", err, executed, saved)
	}
	if err := runWith([]string{"--check", "bad"}, execute, writeResult); err == nil {
		t.Fatal("invalid args accepted")
	}
	want := errors.New("save failed")
	err = runWith(validArgs("go-test"), func(value config, _ []string) checkResult {
		return checkResult{CheckID: value.checkID}
	}, func(string, checkResult) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("runWith() error = %v", err)
	}
}

func TestFailureAndEnvironmentBranches(t *testing.T) {
	t.Setenv("CR_RESULT_DIR", "/outside")
	if err := writeResult("result-0123456789abcdef.json", checkResult{}); err == nil {
		t.Fatal("writeResult accepted an unsafe directory")
	}
	t.Setenv("HOME", "/outside")
	if err := prepareTargetDirectories(); err == nil {
		t.Fatal("prepareTargetDirectories accepted an unsafe target")
	}
	t.Setenv("HOME", "included-home")
	t.Setenv("GOCACHE", "")
	environment := strings.Join(targetEnvironment(), "\n")
	if !strings.Contains(environment, "HOME=included-home") || strings.Contains(environment, "GOCACHE=") {
		t.Fatalf("targetEnvironment() = %q", environment)
	}
}

const validRepoDir = "/tmp/run/ws_cr-0123456789abcdef_1/work"

func validArgs(checkID string) []string {
	return []string{"--check", checkID, "--result", "result-0123456789abcdef.json"}
}

func invalidConfigArgs() [][]string {
	base := validArgs("go-test")
	with := func(args ...string) []string { return append(append([]string(nil), base...), args...) }
	return [][]string{with("--timeout", "100m"), with("--timeout", "0s"), with("--output-limit", "0"), with("--output-limit", "99999999")}
}

func testConfig(t *testing.T) config {
	return config{checkID: "test", cwd: t.TempDir(), timeout: helperExecutionTimeout, outputLimit: defaultOutputLimit, configureProcess: func(*exec.Cmd) {}}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
