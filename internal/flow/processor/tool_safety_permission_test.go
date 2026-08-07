//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolsafety "trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestToolSafetyPermissionPolicyAllowExecutesOnce(t *testing.T) {
	const (
		toolName = "workspace_exec"
		toolArgs = `{"command":"echo ready"}`
	)

	policy := toolsafety.DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil

	permissionPolicy := toolsafety.NewPermissionPolicy(
		toolsafety.NewScanner(policy),
	)

	callCount := 0
	spyTool := &mockCallableTool{
		declaration: &tool.Declaration{
			Name: toolName,
		},
		callFn: func(
			_ context.Context,
			args []byte,
		) (any, error) {
			callCount++

			require.JSONEq(
				t,
				toolArgs,
				string(args),
			)

			return map[string]any{
				"ok": true,
			}, nil
		},
	}

	invocation := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolPermissionPolicy(
				permissionPolicy,
			),
		),
	}

	_, result, modifiedArgs, _, _, err :=
		NewFunctionCallResponseProcessor(
			false,
			nil,
		).executeToolWithCallbacks(
			context.Background(),
			invocation,
			model.ToolCall{
				ID: "call-allow",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: []byte(toolArgs),
				},
			},
			spyTool,
			nil,
		)

	require.NoError(t, err)
	require.Equal(t, 1, callCount)
	require.JSONEq(t, toolArgs, string(modifiedArgs))
	require.JSONEq(
		t,
		`{"ok":true}`,
		string(mustJSON(result)),
	)
}

func TestToolSafetyPermissionPolicyDenySkipsExecution(t *testing.T) {
	const (
		toolName = "workspace_exec"
		toolArgs = `{"command":"cat .env"}`
	)

	policy := toolsafety.DefaultPolicy()
	policy.AllowedCommands = []string{"cat"}
	policy.DeniedCommands = nil

	permissionPolicy := toolsafety.NewPermissionPolicy(
		toolsafety.NewScanner(policy),
	)

	callCount := 0
	spyTool := &mockCallableTool{
		declaration: &tool.Declaration{
			Name: toolName,
		},
		callFn: func(
			_ context.Context,
			_ []byte,
		) (any, error) {
			callCount++

			return map[string]any{
				"unexpected": true,
			}, nil
		},
	}

	invocation := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolPermissionPolicy(
				permissionPolicy,
			),
		),
	}

	_, result, modifiedArgs, _, _, err :=
		NewFunctionCallResponseProcessor(
			false,
			nil,
		).executeToolWithCallbacks(
			context.Background(),
			invocation,
			model.ToolCall{
				ID: "call-deny",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: []byte(toolArgs),
				},
			},
			spyTool,
			nil,
		)

	require.NoError(t, err)
	require.Equal(t, 0, callCount)
	require.JSONEq(t, toolArgs, string(modifiedArgs))

	permissionResult, ok := result.(tool.PermissionResult)
	require.True(
		t,
		ok,
		"result type = %T, want tool.PermissionResult",
		result,
	)

	require.Equal(
		t,
		tool.PermissionResultStatusDenied,
		permissionResult.Status,
	)
	require.Equal(t, toolName, permissionResult.Tool)
	require.Contains(
		t,
		permissionResult.Reason,
		"SAFE-SENSITIVE-PATH",
	)
}

func TestToolSafetyPermissionPolicyAskSkipsExecution(t *testing.T) {
	const (
		toolName = "workspace_exec"
		toolArgs = `{"command":"echo hello | wc -c"}`
	)

	policy := toolsafety.DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"wc",
	}
	policy.DeniedCommands = nil

	permissionPolicy := toolsafety.NewPermissionPolicy(
		toolsafety.NewScanner(policy),
	)

	callCount := 0
	spyTool := &mockCallableTool{
		declaration: &tool.Declaration{
			Name: toolName,
		},
		callFn: func(
			_ context.Context,
			_ []byte,
		) (any, error) {
			callCount++

			return map[string]any{
				"unexpected": true,
			}, nil
		},
	}

	invocation := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolPermissionPolicy(
				permissionPolicy,
			),
		),
	}

	_, result, modifiedArgs, _, _, err :=
		NewFunctionCallResponseProcessor(
			false,
			nil,
		).executeToolWithCallbacks(
			context.Background(),
			invocation,
			model.ToolCall{
				ID: "call-ask",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: []byte(toolArgs),
				},
			},
			spyTool,
			nil,
		)

	require.NoError(t, err)
	require.Equal(t, 0, callCount)
	require.JSONEq(t, toolArgs, string(modifiedArgs))

	permissionResult, ok := result.(tool.PermissionResult)
	require.True(
		t,
		ok,
		"result type = %T, want tool.PermissionResult",
		result,
	)

	require.Equal(
		t,
		tool.PermissionResultStatusApprovalRequired,
		permissionResult.Status,
	)
	require.Equal(t, toolName, permissionResult.Tool)
	require.Contains(
		t,
		permissionResult.Reason,
		"SAFE-SHELL-PIPELINE",
	)
}

func TestToolSafetyPermissionPolicyScannerErrorSkipsExecution(
	t *testing.T,
) {
	const (
		toolName = "workspace_exec"
		toolArgs = `{"command":"echo ready"}`
	)

	invalidScanner := toolsafety.NewScanner(
		toolsafety.Policy{},
	)
	permissionPolicy := toolsafety.NewPermissionPolicy(
		invalidScanner,
	)

	callCount := 0
	spyTool := &mockCallableTool{
		declaration: &tool.Declaration{
			Name: toolName,
		},
		callFn: func(
			_ context.Context,
			_ []byte,
		) (any, error) {
			callCount++

			return map[string]any{
				"unexpected": true,
			}, nil
		},
	}

	invocation := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolPermissionPolicy(
				permissionPolicy,
			),
		),
	}

	_, result, _, _, _, err :=
		NewFunctionCallResponseProcessor(
			false,
			nil,
		).executeToolWithCallbacks(
			context.Background(),
			invocation,
			model.ToolCall{
				ID: "call-scanner-error",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: []byte(toolArgs),
				},
			},
			spyTool,
			nil,
		)

	require.NoError(t, err)
	require.Equal(t, 0, callCount)

	permissionResult, ok := result.(tool.PermissionResult)
	require.True(
		t,
		ok,
		"result type = %T, want tool.PermissionResult",
		result,
	)

	require.Equal(
		t,
		tool.PermissionResultStatusDenied,
		permissionResult.Status,
	)
	require.Contains(
		t,
		permissionResult.Reason,
		"safety scan failed",
	)
	require.Contains(
		t,
		permissionResult.Reason,
		"invalid safety policy",
	)
}

type toolSafetyFailingAuditor struct{}

func (toolSafetyFailingAuditor) Record(
	toolsafety.AuditEvent,
) error {
	return errors.New("audit unavailable")
}

func TestToolSafetyPermissionPolicyAuditErrorSkipsExecution(
	t *testing.T,
) {
	const (
		toolName = "workspace_exec"
		toolArgs = `{"command":"echo ready"}`
	)

	policy := toolsafety.DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil

	scanner := toolsafety.NewScanner(
		policy,
		toolsafety.WithAuditor(
			toolSafetyFailingAuditor{},
		),
	)
	permissionPolicy := toolsafety.NewPermissionPolicy(
		scanner,
	)

	callCount := 0
	spyTool := &mockCallableTool{
		declaration: &tool.Declaration{
			Name: toolName,
		},
		callFn: func(
			_ context.Context,
			_ []byte,
		) (any, error) {
			callCount++

			return map[string]any{
				"unexpected": true,
			}, nil
		},
	}

	invocation := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithToolPermissionPolicy(
				permissionPolicy,
			),
		),
	}

	_, result, _, _, _, err :=
		NewFunctionCallResponseProcessor(
			false,
			nil,
		).executeToolWithCallbacks(
			context.Background(),
			invocation,
			model.ToolCall{
				ID: "call-audit-error",
				Function: model.FunctionDefinitionParam{
					Name:      toolName,
					Arguments: []byte(toolArgs),
				},
			},
			spyTool,
			nil,
		)

	require.NoError(t, err)
	require.Equal(t, 0, callCount)

	permissionResult, ok := result.(tool.PermissionResult)
	require.True(
		t,
		ok,
		"result type = %T, want tool.PermissionResult",
		result,
	)

	require.Equal(
		t,
		tool.PermissionResultStatusDenied,
		permissionResult.Status,
	)
	require.Contains(
		t,
		permissionResult.Reason,
		"safety scan failed",
	)
	require.Contains(
		t,
		permissionResult.Reason,
		"record safety audit",
	)
	require.Contains(
		t,
		permissionResult.Reason,
		"audit unavailable",
	)
}

func TestToolSafetyPermissionPolicyScansFinalCallbackArguments(
	t *testing.T,
) {
	const toolName = "workspace_exec"

	policy := toolsafety.DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"cat",
	}
	policy.DeniedCommands = nil

	permissionPolicy := toolsafety.NewPermissionPolicy(
		toolsafety.NewScanner(policy),
	)

	tests := []struct {
		name          string
		originalArgs  string
		rewrittenArgs string
		wantCalls     int
		wantDenied    bool
	}{
		{
			name:          "safe original rewritten to dangerous",
			originalArgs:  `{"command":"echo ready"}`,
			rewrittenArgs: `{"command":"cat .env"}`,
			wantCalls:     0,
			wantDenied:    true,
		},
		{
			name:          "dangerous original rewritten to safe",
			originalArgs:  `{"command":"cat .env"}`,
			rewrittenArgs: `{"command":"echo ready"}`,
			wantCalls:     1,
			wantDenied:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callbacks := tool.NewCallbacks()
			callbacks.RegisterBeforeTool(func(
				_ context.Context,
				args *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				require.JSONEq(
					t,
					tc.originalArgs,
					string(args.Arguments),
				)

				return &tool.BeforeToolResult{
					ModifiedArguments: []byte(
						tc.rewrittenArgs,
					),
				}, nil
			})

			callCount := 0
			spyTool := &mockCallableTool{
				declaration: &tool.Declaration{
					Name: toolName,
				},
				callFn: func(
					_ context.Context,
					args []byte,
				) (any, error) {
					callCount++

					require.JSONEq(
						t,
						tc.rewrittenArgs,
						string(args),
					)

					return map[string]any{
						"ok": true,
					}, nil
				},
			}

			invocation := &agent.Invocation{
				RunOptions: agent.NewRunOptions(
					agent.WithToolPermissionPolicy(
						permissionPolicy,
					),
				),
			}

			_, result, modifiedArgs, _, _, err :=
				NewFunctionCallResponseProcessor(
					false,
					callbacks,
				).executeToolWithCallbacks(
					context.Background(),
					invocation,
					model.ToolCall{
						ID: "call-rewritten",
						Function: model.FunctionDefinitionParam{
							Name: toolName,
							Arguments: []byte(
								tc.originalArgs,
							),
						},
					},
					spyTool,
					nil,
				)

			require.NoError(t, err)
			require.Equal(t, tc.wantCalls, callCount)
			require.JSONEq(
				t,
				tc.rewrittenArgs,
				string(modifiedArgs),
			)

			if tc.wantDenied {
				permissionResult, ok :=
					result.(tool.PermissionResult)
				require.True(
					t,
					ok,
					"result type = %T, want tool.PermissionResult",
					result,
				)
				require.Equal(
					t,
					tool.PermissionResultStatusDenied,
					permissionResult.Status,
				)
				require.Contains(
					t,
					permissionResult.Reason,
					"SAFE-SENSITIVE-PATH",
				)
				return
			}

			require.JSONEq(
				t,
				`{"ok":true}`,
				string(mustJSON(result)),
			)
		})
	}
}
