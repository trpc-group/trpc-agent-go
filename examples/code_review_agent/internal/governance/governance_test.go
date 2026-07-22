//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package governance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type memoryRecorder struct {
	decisions []Decision
	err       error
}

func (r *memoryRecorder) SaveDecision(_ context.Context, value Decision) error {
	r.decisions = append(r.decisions, value)
	return r.err
}

func TestAuthorizerFailClosed(t *testing.T) {
	spec := validSpec(t)
	tests := []struct {
		name     string
		mutate   func(*CheckSpec)
		policy   tool.PermissionPolicy
		recorder Recorder
		want     bool
	}{
		{"allow", func(*CheckSpec) {}, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, true},
		{"nil policy", func(*CheckSpec) {}, nil, &memoryRecorder{}, false},
		{"zero action", func(*CheckSpec) {}, tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
			return tool.PermissionDecision{}, nil
		}), &memoryRecorder{}, false},
		{"policy error", func(*CheckSpec) {}, tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
			return tool.PermissionDecision{}, errors.New("audit down")
		}), &memoryRecorder{}, false},
		{"deny", func(*CheckSpec) {}, tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
			return tool.DenyPermission("blocked"), nil
		}), &memoryRecorder{}, false},
		{"dynamic argv", func(value *CheckSpec) { value.Argv = append(value.Argv, "-run", "user") }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"network", func(value *CheckSpec) { value.Network = true }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"runtime", func(value *CheckSpec) { value.Runtime = "local" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"cwd", func(value *CheckSpec) { value.Cwd = ".." }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"timeout", func(value *CheckSpec) { value.Timeout = 0 }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"artifact", func(value *CheckSpec) { value.Artifact = "result.json" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"runner path", func(value *CheckSpec) { value.RunnerPath = value.SkillRoot }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"environment", func(value *CheckSpec) { value.Env["LD_PRELOAD"] = "x" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"environment value", func(value *CheckSpec) { value.Env["PATH"] = "/host/bin" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"environment path pair", func(value *CheckSpec) { value.Env["CR_REPO_DIR"] = "/tmp/run/cr-fedcba9876543210/repo" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"dependency digest", func(value *CheckSpec) { value.DependencyDigest = "bad" }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"dependency modules", func(value *CheckSpec) { value.DependencyModules = 513 }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"dependency expanded", func(value *CheckSpec) { value.DependencyExpandedBytes = 257 << 20 }, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{}, false},
		{"persist failure", func(*CheckSpec) {}, tool.PermissionPolicyFunc(DefaultPolicy), &memoryRecorder{err: errors.New("db down")}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := spec
			current.Argv = append([]string(nil), spec.Argv...)
			current.Env = cloneEnv(spec.Env)
			test.mutate(&current)
			err := (Authorizer{Policy: test.policy, Recorder: test.recorder}).Authorize(context.Background(), current)
			if (err == nil) != test.want {
				t.Fatalf("Authorize() error = %v", err)
			}
		})
	}
}

func TestDecisionDigestBindsArtifactAndEnvironment(t *testing.T) {
	first := validSpec(t)
	second := first
	second.Env = cloneEnv(first.Env)
	second.Artifact = "result-fedcba9876543210.json"
	second.Env["CR_RESULT_DIR"] = "/tmp/run/ws_cr-fedcba9876543210_0/out"
	second.DependencyBytes++
	if decision(first, decisionEvidence{}).ArgsDigest == decision(second, decisionEvidence{}).ArgsDigest {
		t.Fatal("security-critical fields are not bound by decision digest")
	}

	reordered := first
	reordered.Env = make(map[string]string, len(first.Env))
	keys := []string{"CR_REPO_DIR", "CR_RESULT_DIR", "GOENV", "GOMAXPROCS", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "GOVCS", "HOME", "PATH", "TMPDIR"}
	for _, key := range keys {
		reordered.Env[key] = first.Env[key]
	}
	if decision(first, decisionEvidence{}).ArgsDigest != decision(reordered, decisionEvidence{}).ArgsDigest {
		t.Fatal("decision digest depends on map insertion order")
	}

	extra := first
	extra.Env = cloneEnv(first.Env)
	extra.Env["GOPROXY"] = "off"
	if decision(first, decisionEvidence{}).ArgsDigest == decision(extra, decisionEvidence{}).ArgsDigest {
		t.Fatal("extra environment field is not bound by decision digest")
	}
}

func TestAuthorizerRedactsPolicyErrors(t *testing.T) {
	const secret = "sk-abcdefghijklmnopqrstuvwxyz123456"
	recorder := &memoryRecorder{}
	policy := tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.PermissionDecision{}, errors.New("failure " + secret)
	})
	err := (Authorizer{Policy: policy, Recorder: recorder}).Authorize(context.Background(), validSpec(t))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Authorize() error = %v", err)
	}
	for _, saved := range recorder.decisions {
		if strings.Contains(saved.Reason, secret) {
			t.Fatalf("secret persisted in decision: %#v", saved)
		}
	}
}

func TestAuthorizerPersistsAskWithoutExecuting(t *testing.T) {
	recorder := &memoryRecorder{}
	policy := tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.AskPermission("review"), nil
	})
	err := (Authorizer{Policy: policy, Recorder: recorder}).Authorize(context.Background(), validSpec(t))
	if err == nil || len(recorder.decisions) != 2 || recorder.decisions[1].Action != string(tool.PermissionActionAsk) {
		t.Fatalf("Authorize() error = %v, decisions = %#v", err, recorder.decisions)
	}
}

func TestDefaultPolicyRejectsMalformedAndUnknown(t *testing.T) {
	for _, request := range []*tool.PermissionRequest{
		nil,
		{ToolName: "other", Arguments: []byte(`{}`)},
		{ToolName: "code_review_check", Arguments: []byte(`{`)},
		{ToolName: "code_review_check", Arguments: []byte(`{"check_id":"custom"}`)},
	} {
		decision, err := DefaultPolicy(context.Background(), request)
		if err != nil || decision.Action == tool.PermissionActionAllow {
			t.Fatalf("decision=%#v error=%v", decision, err)
		}
	}
}

func TestAuthorizerAllowsDeclaredLocalFallback(t *testing.T) {
	spec := validSpec(t)
	spec.Runtime, spec.Network, spec.HostWrite = "local", true, true
	temporary := filepath.Join(spec.RepoSource, "temporary")
	spec.Env = map[string]string{
		"PATH": os.Getenv("PATH"), "HOME": filepath.Join(temporary, "home"),
		"GOCACHE": filepath.Join(temporary, "gocache"), "GOMODCACHE": filepath.Join(temporary, "gomodcache"),
		"GOTMPDIR": temporary, "TMPDIR": temporary, "TMP": temporary, "TEMP": temporary,
		"GOMAXPROCS": "2", "GOPROXY": "off", "GOSUMDB": "off", "GOENV": "off", "GOWORK": "off",
		"CR_REPO_DIR": spec.RepoSource, "SYSTEMROOT": os.Getenv("SYSTEMROOT"),
	}
	recorder := &memoryRecorder{}
	if err := (Authorizer{Policy: tool.PermissionPolicyFunc(DefaultPolicy), Recorder: recorder}).
		Authorize(context.Background(), spec); err != nil {
		t.Fatalf("Authorize(local) error = %v", err)
	}
	if len(recorder.decisions) != 2 || recorder.decisions[0].Risk != "high" {
		t.Fatalf("decisions = %#v", recorder.decisions)
	}
	spec.Env["GOPROXY"] = "https://proxy.example"
	if err := (Authorizer{Policy: tool.PermissionPolicyFunc(DefaultPolicy), Recorder: &memoryRecorder{}}).
		Authorize(context.Background(), spec); err == nil {
		t.Fatal("Authorize(local network proxy) error = nil")
	}
}

func validSpec(t *testing.T) CheckSpec {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "scripts", "checkrunner")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(dir, "cr-checkrunner")
	if err := os.WriteFile(runner, []byte("runner"), 0o600); err != nil {
		t.Fatal(err)
	}
	return CheckSpec{ID: "go-test", Runtime: "container", RunnerPath: runner, SkillRoot: root, Cwd: "repo", Artifact: "result-0123456789abcdef.json", DependencyDigest: strings.Repeat("0", 64),
		RepoSource: root, Argv: []string{"go", "test", "-mod=readonly", "./..."}, Env: map[string]string{
			"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp/cr-target/home",
			"GOCACHE": "/tmp/cr-target/gocache", "GOMODCACHE": "/tmp/cr-target/gomodcache", "TMPDIR": "/tmp/cr-target/tmp",
			"GOMAXPROCS": "2", "GOPROXY": "file:///opt/trpc-agent/modproxy", "GOSUMDB": "off",
			"GOENV": "off", "GOTOOLCHAIN": "local", "GOVCS": "*:off",
			"CR_RESULT_DIR": "/tmp/run/ws_cr-0123456789abcdef_0/out", "CR_REPO_DIR": "/tmp/run/ws_cr-0123456789abcdef_0/work",
		}, Timeout: time.Minute}
}

func cloneEnv(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
