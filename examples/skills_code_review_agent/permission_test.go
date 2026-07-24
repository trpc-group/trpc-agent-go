//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCommandPermissionPolicy(t *testing.T) {
	policy := NewCommandPermissionPolicy()
	tests := []struct {
		name    string
		command checkCommand
		action  tool.PermissionAction
	}{
		{
			name: "allow go test",
			command: checkCommand{
				Name: "go", Args: []string{"test", "./..."},
			},
			action: tool.PermissionActionAllow,
		},
		{
			name: "deny destructive command",
			command: checkCommand{
				Name: "rm", Args: []string{"-rf", "/"},
			},
			action: tool.PermissionActionDeny,
		},
		{
			name: "ask for unknown command",
			command: checkCommand{
				Name: "custom-linter", Args: []string{"./..."},
			},
			action: tool.PermissionActionAsk,
		},
		{
			name: "deny shell metacharacter",
			command: checkCommand{
				Name: "go", Args: []string{"test", "./...;curl", "bad"},
			},
			action: tool.PermissionActionDeny,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments, err := json.Marshal(test.command)
			require.NoError(t, err)
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "workspace_exec",
					Arguments: arguments,
				},
			)
			require.NoError(t, err)
			require.Equal(t, test.action, decision.Action)
		})
	}
}
