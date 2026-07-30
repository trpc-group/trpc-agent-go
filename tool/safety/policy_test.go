//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultPolicySafetyDefaults(t *testing.T) {
	policy := DefaultPolicy()

	if policy.MaxTimeoutSec != 300 {
		t.Errorf("MaxTimeoutSec = %d,want 300", policy.MaxTimeoutSec)
	}

	if policy.MaxOutputBytes != 4<<20 {
		t.Errorf("MaxOutputBytes = %d,want %d", policy.MaxOutputBytes, 4<<20)
	}

	if policy.MaxConcurrency != 128 {
		t.Errorf(
			"MaxConcurrency = %d,want 128",
			policy.MaxConcurrency,
		)
	}

	if policy.ParseFailureAction != DecisionAsk {
		t.Errorf("ParseFailureAction = %q,want %q", policy.ParseFailureAction, DecisionAsk)
	}

	if policy.UnknownToolAction != DecisionAsk {
		t.Errorf("UnknowToolAction = %q,want %q", policy.UnknownToolAction, DecisionAsk)
	}

	if policy.PipelineAction != DecisionAsk {
		t.Errorf("PipeLlineAction = %q,want %q", policy.PipelineAction, DecisionAsk)
	}

	wantDeniedCommands := []string{
		"rm", "nc", "netcat", "ssh", "scp", "sftp",
	}

	for _, wantCommand := range wantDeniedCommands {
		found := false

		for _, gotCommand := range policy.DeniedCommands {
			if gotCommand == wantCommand {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("DeniedCommands does not contain %q;got %v", wantCommand, policy.DeniedCommands)
		}
	}

	if err := policy.Validate(); err != nil {
		t.Fatalf("DefaultPolicy().Vaildate() error = %v", err)
	}
}

func TestLoadPolicyFileReadErrorHasContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-policy.yaml")

	_, err := LoadPolicyFile(path)
	if err == nil {
		t.Fatal("LoadPolicyFile() succeeded for a missing file")
	}

	if !strings.Contains(err.Error(), "load safety policy file") {
		t.Fatalf("LoadPolicyFile() error = %q, want context %q", err, "load safety policy file")
	}
}

func TestPolicyNormalizeCleansStringLists(t *testing.T) {
	policy := Policy{
		AllowedCommands:  []string{" go ", "", " ", " git "},
		DeniedCommands:   []string{" rm ", "", " ssh "},
		ForbiddenPaths:   []string{" ~/.ssh", "", ".env"},
		NetworkAllowlist: []string{" github.com ", ""},
		EnvAllowlist:     []string{" PATH", "", "GOCACHE"},
	}

	policy.normalize()

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "allowed commands",
			got:  policy.AllowedCommands,
			want: []string{"go", "git"},
		},
		{
			name: "denied commands",
			got:  policy.DeniedCommands,
			want: []string{"rm", "ssh"},
		},
		{
			name: "forbidden paths",
			got:  policy.ForbiddenPaths,
			want: []string{"~/.ssh", ".env"},
		},
		{
			name: "network allowlist",
			got:  policy.NetworkAllowlist,
			want: []string{"github.com"},
		},
		{
			name: "environment allowlist",
			got:  policy.EnvAllowlist,
			want: []string{"PATH", "GOCACHE"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf(
					"normalized value = %v,want %v",
					tc.got,
					tc.want,
				)
			}
		})
	}
}

func TestPolicyNormalizeDefaultsEmptyActionsToAsk(t *testing.T) {
	policy := Policy{
		MaxTimeoutSec:  1,
		MaxOutputBytes: 1,
		MaxConcurrency: 1,
	}

	policy.normalize()

	tests := []struct {
		name string
		got  Decision
	}{
		{
			name: "parse failure",
			got:  policy.ParseFailureAction,
		},
		{
			name: "unknown tool",
			got:  policy.UnknownToolAction,
		},
		{
			name: "dependency",
			got:  policy.DependencyAction,
		},
		{
			name: "pipeline",
			got:  policy.PipelineAction,
		},
		{
			name: "host PTY",
			got:  policy.HostPTYAction,
		},
		{
			name: "background",
			got:  policy.BackgroundAction,
		},
		{
			name: "disallowed environment",
			got:  policy.DisallowedEnvAction,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != DecisionAsk {
				t.Errorf(
					"normalized action = %q,want %q",
					tc.got,
					DecisionAsk,
				)
			}
		})
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf(
			"normalized policy should be valid: %v",
			err,
		)
	}
}
