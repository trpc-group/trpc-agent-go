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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCheckToolPermissionDoesNotMutateCallerRequest(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)
	request := &tool.PermissionRequest{
		ToolName:  "  WORKSPACE_EXEC  ",
		Arguments: []byte(`{"command":"go test ./tool/safety","timeout_sec":10}`),
		Metadata:  tool.ToolMetadata{MaxResultSize: 4096},
	}

	decision, err := guard.CheckToolPermission(context.Background(), request)

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "limits.output_unspecified")
	require.Equal(t, "  WORKSPACE_EXEC  ", request.ToolName)
}

func TestRegisteredExtractorUnhandledFailsClosed(t *testing.T) {
	guard, err := New(
		testPolicy(),
		WithExtractor("custom_exec", ExtractorFunc(func(*tool.PermissionRequest) (Request, bool, error) {
			return Request{}, false, nil
		})),
	)
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "custom_exec",
	})

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.unhandled_arguments")
}

func TestExtractorRegistrationAndFieldFallbacks(t *testing.T) {
	var got AuditEvent
	guard, err := New(
		testPolicy(),
		WithExtractor("", ExtractorFunc(func(*tool.PermissionRequest) (Request, bool, error) {
			t.Fatal("empty extractor registration was used")
			return Request{}, true, nil
		})),
		WithExtractor("custom_exec", ExtractorFunc(func(*tool.PermissionRequest) (Request, bool, error) {
			request := commandRequest(BackendWorkspace, "echo safe")
			request.ToolName = ""
			request.ToolCallID = ""
			request.Metadata = tool.ToolMetadata{}
			return request, true, nil
		})),
		WithAuditSink(AuditSinkFunc(func(_ context.Context, event AuditEvent) error {
			got = event
			return nil
		})),
	)
	require.NoError(t, err)
	metadata := tool.ToolMetadata{OpenWorld: true, MaxResultSize: 4096}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "custom_exec", ToolCallID: "custom-call", Metadata: metadata,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
	require.Equal(t, "custom_exec", got.ToolName)
	require.Equal(t, "custom-call", got.ToolCallID)
	require.Equal(t, "unit-test", got.PolicyID)
	require.Equal(t, BackendWorkspace, got.Backend)

	ordinary, err := New(testPolicy(), WithExtractor("workspace_exec", nil))
	require.NoError(t, err)
	decision, err = ordinary.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec",
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "tool.unknown_executor")
}

func TestGuardNilAndPolicyContracts(t *testing.T) {
	var nilGuard *Guard
	require.Equal(t, Policy{}, nilGuard.Policy())
	_, err := nilGuard.Scan(context.Background(), Request{})
	require.ErrorContains(t, err, "guard is nil")
	decision, err := nilGuard.CheckToolPermission(context.Background(), &tool.PermissionRequest{})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	guard, err := New(testPolicy(), nil)
	require.NoError(t, err)
	decision, err = guard.CheckToolPermission(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)

	copyOne := guard.Policy()
	copyOne.Commands.Allowed[0] = "mutated"
	copyOne.Paths.Denied[0] = "mutated"
	copyTwo := guard.Policy()
	require.NotEqual(t, "mutated", copyTwo.Commands.Allowed[0])
	require.NotEqual(t, "mutated", copyTwo.Paths.Denied[0])
}

func TestGuardFailClosedInputBoundaries(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := guard.Scan(ctx, commandRequest(BackendWorkspace, "go test ./tool/safety"))
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "input.cancelled"))

	report, err = guard.Scan(context.Background(), Request{
		ToolName: "workspace_exec", Backend: BackendWorkspace,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "input.empty"))

	defaultGuard, err := New(DefaultPolicy())
	require.NoError(t, err)
	report, err = defaultGuard.Scan(context.Background(), Request{
		ToolName: "workspace_exec", Backend: BackendWorkspace,
		Command: "printf safe", TimeoutMS: 1000, MaxOutputBytes: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "command.not_allowed"))
}

func TestEnvironmentOverridesAreNonNegotiable(t *testing.T) {
	policy := testPolicy()
	policy.Environment.AllowedVariables = append(
		policy.Environment.AllowedVariables,
		"PATH",
		"MALFORMED;KEY",
	)
	guard, err := New(policy)
	require.NoError(t, err)

	pathRequest := commandRequest(BackendWorkspace, "go test ./tool/safety")
	pathRequest.Env = map[string]string{"PATH": "./attacker-bin"}
	report, err := guard.Scan(context.Background(), pathRequest)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "env.denied"))

	malformedRequest := commandRequest(BackendWorkspace, "go test ./tool/safety")
	malformedRequest.Env = map[string]string{"MALFORMED;KEY": "value"}
	report, err = guard.Scan(context.Background(), malformedRequest)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "env.malformed"))
}

func TestPermissionExtractorScansInitialStdin(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "python")
	guard, err := New(policy)
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec",
		Arguments: []byte(`{
			"command":"python -",
			"stdin":"rm -rf /",
			"timeout_sec":10
		}`),
		Metadata: tool.ToolMetadata{MaxResultSize: 4096},
	})

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Contains(t, decision.Reason, "destructive.delete")
}

func TestCustomRuleReceivesDefensiveCopies(t *testing.T) {
	policy := testPolicy()
	request := Request{
		ToolName: "execute_code", Backend: BackendCode,
		CodeBlocks: []CodeBlock{{Language: "go", Code: "package main"}},
		Env:        map[string]string{"CI": "true"},
		TimeoutMS:  1000, MaxOutputBytes: 1024,
	}
	guard, err := New(
		policy,
		WithRule(nil),
		WithRule(RuleFunc(func(_ context.Context, got Request, gotPolicy Policy) []Match {
			got.Env["CI"] = "mutated"
			got.CodeBlocks[0].Code = "mutated"
			gotPolicy.Commands.Allowed[0] = "mutated"
			match := Match{
				Decision:  "invalid",
				RiskLevel: "invalid",
				Evidence:  "custom invalid result",
			}
			return []Match{match, match}
		})),
	)
	require.NoError(t, err)

	report, err := guard.Scan(context.Background(), request)

	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.Equal(t, RiskLevelHigh, report.RiskLevel)
	require.Equal(t, "rule.invalid", report.RuleID)
	require.Equal(t, "true", request.Env["CI"])
	require.Equal(t, "package main", request.CodeBlocks[0].Code)
	require.NotEqual(t, "mutated", guard.Policy().Commands.Allowed[0])
	require.Len(t, report.Matches, 1)
	require.Contains(t, report.Evidence, "invalid decision")
}

func TestNormalizeMatchFillsRequiredExplanationFields(t *testing.T) {
	match := normalizeMatch(Match{
		Decision:  tool.PermissionActionDeny,
		RiskLevel: RiskLevelHigh,
		RuleID:    "custom.incomplete",
	})
	require.NotEmpty(t, match.Evidence)
	require.NotEmpty(t, match.Recommendation)
}
