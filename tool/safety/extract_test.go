//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func mustCompiledDefault(t *testing.T) *Policy {
	t.Helper()
	p := DefaultPolicy()
	if err := p.compile(); err != nil {
		t.Fatalf("compile default: %v", err)
	}
	return &p
}

func TestBackendOf(t *testing.T) {
	p := mustCompiledDefault(t)
	cases := map[string]string{
		"workspace_exec": BackendWorkspace,
		"exec_command":   BackendHost,
		"execute_code":   BackendCode,
		"search_file":    "",
		"":               "",
	}
	for name, want := range cases {
		if got := backendOf(name, p); got != want {
			t.Errorf("backendOf(%q) = %q, want %q", name, got, want)
		}
	}
	if backendOf("workspace_exec", nil) != "" {
		t.Errorf("backendOf with nil policy must return empty")
	}
}

func TestExtractWorkspace(t *testing.T) {
	args := []byte(`{"command":"go test ./...","cwd":"work","timeout_sec":30}`)
	er, err := extract(args, BackendWorkspace)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if er.Command != "go test ./..." {
		t.Errorf("command = %q", er.Command)
	}
	if er.Cwd != "work" {
		t.Errorf("cwd = %q, want work", er.Cwd)
	}
	if er.TimeoutSec != 30 {
		t.Errorf("timeout = %d, want 30", er.TimeoutSec)
	}
	if er.PTY || er.Background {
		t.Errorf("pty/background should be false")
	}
}

func TestExtractHostWorkdirAndTTY(t *testing.T) {
	// exec_command uses "workdir"; tty should map to PTY.
	args := []byte(`{"command":"sleep 5","workdir":"/srv","background":true,"tty":true}`)
	er, err := extract(args, BackendHost)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if er.Cwd != "/srv" {
		t.Errorf("cwd = %q, want /srv (from workdir)", er.Cwd)
	}
	if !er.Background {
		t.Errorf("background = false, want true")
	}
	if !er.PTY {
		t.Errorf("pty = false, want true (tty alias)")
	}
}

func TestExtractPTYAlias(t *testing.T) {
	args := []byte(`{"command":"top","pty":true}`)
	er, err := extract(args, BackendHost)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !er.PTY {
		t.Errorf("pty alias not honored")
	}
}

func TestExtractTimeoutPrecedence(t *testing.T) {
	// timeout_sec wins over the bare timeout field.
	args := []byte(`{"command":"x","timeout":10,"timeout_sec":99}`)
	er, err := extract(args, BackendWorkspace)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if er.TimeoutSec != 99 {
		t.Errorf("timeout = %d, want 99", er.TimeoutSec)
	}
}

func TestExtractMalformedJSON(t *testing.T) {
	if _, err := extract([]byte(`{not json`), BackendWorkspace); err == nil {
		t.Fatalf("expected JSON parse error")
	}
}

func TestExtractCodeBlocksArray(t *testing.T) {
	args := []byte(`{"code_blocks":[{"language":"python","code":"import os"},` +
		`{"language":"python","code":"print(1)"}]}`)
	er, err := extract(args, BackendCode)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := "import os\nprint(1)"
	if er.Command != want {
		t.Errorf("command = %q, want %q", er.Command, want)
	}
}

func TestExtractCodeBlocksSingleObject(t *testing.T) {
	args := []byte(`{"code_blocks":{"language":"python","code":"x=1"}}`)
	er, err := extract(args, BackendCode)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if er.Command != "x=1" {
		t.Errorf("command = %q, want x=1", er.Command)
	}
}

func TestExtractEmptyArgs(t *testing.T) {
	er, err := extract(nil, BackendWorkspace)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if er.Command != "" || er.Cwd != "" {
		t.Errorf("empty args should yield zero ExecRequest, got %+v", er)
	}
}
