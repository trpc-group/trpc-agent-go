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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type policyTransition struct {
	name       string
	command    string
	baseline   string
	modified   string
	wantBefore Decision
	wantAfter  Decision
}

func TestPolicyFilesChangeDecisionsWithoutCodeChanges(t *testing.T) {
	for _, extension := range []string{"yaml", "json"} {
		for _, transition := range policyTransitions(extension) {
			t.Run(extension+"/"+transition.name, func(t *testing.T) {
				before := scanWithPolicyFile(
					t, extension, transition.baseline, transition.command,
				)
				after := scanWithPolicyFile(
					t, extension, transition.modified, transition.command,
				)
				require.Equal(t, transition.wantBefore, before.Decision)
				require.Equal(t, transition.wantAfter, after.Decision)
			})
		}
	}
}

func scanWithPolicyFile(
	t *testing.T,
	extension string,
	content string,
	command string,
) Report {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy."+extension)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	report, err := guard.Scan(context.Background(), scanCommand(command))
	require.NoError(t, err)
	return report
}

func policyTransitions(extension string) []policyTransition {
	if extension == "json" {
		return jsonPolicyTransitions()
	}
	return yamlPolicyTransitions()
}

func yamlPolicyTransitions() []policyTransition {
	return []policyTransition{
		{
			name: "allowed domain", command: "curl -q --noproxy '*' https://new.example/file",
			baseline:   "version: 1\ncommands:\n  allowed: [curl]\nnetwork:\n  allowed_domains: []\n",
			modified:   "version: 1\ncommands:\n  allowed: [curl]\nnetwork:\n  allowed_domains: [new.example]\n",
			wantBefore: DecisionDeny, wantAfter: DecisionAllow,
		},
		{
			name: "denied path", command: "cat project-secret.txt",
			baseline:   "version: 1\ncommands:\n  allowed: [cat]\npaths:\n  denied: []\n",
			modified:   "version: 1\ncommands:\n  allowed: [cat]\npaths:\n  denied: [project-secret.txt]\n",
			wantBefore: DecisionAllow, wantAfter: DecisionDeny,
		},
		{
			name: "allowed command", command: "date",
			baseline:   "version: 1\ncommands:\n  allowed: []\n",
			modified:   "version: 1\ncommands:\n  allowed: [date]\n",
			wantBefore: DecisionDeny, wantAfter: DecisionAllow,
		},
	}
}

func jsonPolicyTransitions() []policyTransition {
	return []policyTransition{
		{
			name: "allowed domain", command: "curl -q --noproxy '*' https://new.example/file",
			baseline:   `{"version":1,"commands":{"allowed":["curl"]},"network":{"allowed_domains":[]}}`,
			modified:   `{"version":1,"commands":{"allowed":["curl"]},"network":{"allowed_domains":["new.example"]}}`,
			wantBefore: DecisionDeny, wantAfter: DecisionAllow,
		},
		{
			name: "denied path", command: "cat project-secret.txt",
			baseline:   `{"version":1,"commands":{"allowed":["cat"]},"paths":{"denied":[]}}`,
			modified:   `{"version":1,"commands":{"allowed":["cat"]},"paths":{"denied":["project-secret.txt"]}}`,
			wantBefore: DecisionAllow, wantAfter: DecisionDeny,
		},
		{
			name: "allowed command", command: "date",
			baseline:   `{"version":1,"commands":{"allowed":[]}}`,
			modified:   `{"version":1,"commands":{"allowed":["date"]}}`,
			wantBefore: DecisionDeny, wantAfter: DecisionAllow,
		},
	}
}
