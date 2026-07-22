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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSkillExecutionInputsAreScannedBeforeUse(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "python")
	guard, err := New(policy)
	require.NoError(t, err)

	tests := []struct {
		name      string
		toolName  string
		arguments string
	}{
		{
			name:     "initial skill stdin",
			toolName: "skill_exec",
			arguments: `{
				"command":"python -","stdin":"rm -rf /","timeout":10
			}`,
		},
		{
			name:      "follow-up skill stdin",
			toolName:  "skill_write_stdin",
			arguments: `{"chars":"rm -rf /","submit":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  test.toolName,
					Arguments: []byte(test.arguments),
					Metadata:  tool.ToolMetadata{MaxResultSize: 4096},
				},
			)
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, decision.Action)
			require.Contains(t, decision.Reason, "destructive.delete")
		})
	}
}

func TestParsedArgumentsCannotHideDeniedPaths(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "cat")
	guard, err := New(policy)
	require.NoError(t, err)

	tests := []struct {
		name    string
		command string
		ruleID  string
	}{
		{name: "credential fragment", command: "cat .e'nv'", ruleID: "credential.access"},
		{name: "denied path fragment", command: "cat /e'tc'/passwd", ruleID: "path.denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := guard.Scan(
				context.Background(),
				commandRequest(BackendWorkspace, test.command),
			)
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionDeny, report.Decision)
			require.True(t, hasRule(report, test.ruleID))
		})
	}
}

func TestUnknownBackendRequiresReview(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)

	for _, backend := range []Backend{"", "custom-untrusted"} {
		t.Run(string(backend), func(t *testing.T) {
			request := commandRequest(backend, "go test ./tool/safety")
			report, err := guard.Scan(context.Background(), request)
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionAsk, report.Decision)
			require.True(t, hasRule(report, "backend.unknown"))
		})
	}
}

func TestOversizedPayloadsSkipDeepAndCustomScanning(t *testing.T) {
	policy := testPolicy()
	policy.Limits.MaxCommandBytes = 8
	policy.Limits.MaxScriptBytes = 8
	policy.Limits.MaxSessionInputBytes = 8
	customCalls := 0
	guard, err := New(policy, WithRule(RuleFunc(func(
		context.Context,
		Request,
		Policy,
	) []Match {
		customCalls++
		return nil
	})))
	require.NoError(t, err)

	tests := []struct {
		name    string
		request Request
		ruleID  string
	}{
		{
			name: "command",
			request: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspace,
				Command: strings.Repeat("x", 9), TimeoutMS: 1000,
				MaxOutputBytes: 1024,
			},
			ruleID: "limits.command_length",
		},
		{
			name: "one-line script",
			request: Request{
				ToolName: "execute_code", Backend: BackendCode,
				Script: strings.Repeat("x", 9), Language: "python",
				TimeoutMS: 1000, MaxOutputBytes: 1024,
			},
			ruleID: "limits.script_bytes",
		},
		{
			name: "code block",
			request: Request{
				ToolName: "execute_code", Backend: BackendCode,
				CodeBlocks: []CodeBlock{{
					Language: "python", Code: strings.Repeat("x", 9),
				}},
				TimeoutMS: 1000, MaxOutputBytes: 1024,
			},
			ruleID: "limits.script_bytes",
		},
		{
			name: "session input",
			request: Request{
				ToolName: "skill_write_stdin", Backend: BackendSkill,
				SessionInput: strings.Repeat("x", 9),
				TimeoutMS:    1000, MaxOutputBytes: 1024,
			},
			ruleID: "limits.session_input_bytes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), test.request)
			require.NoError(t, err)
			require.Equal(t, tool.PermissionActionAsk, report.Decision)
			require.True(t, hasRule(report, test.ruleID))
		})
	}
	largeRequest := Request{
		ToolName: "execute_code", Backend: BackendCode,
		Script:   strings.Repeat("z", maxReportPayloadBytes+1),
		Language: "python", TimeoutMS: 1000, MaxOutputBytes: 1024,
	}
	largeReport, err := guard.Scan(context.Background(), largeRequest)
	require.NoError(t, err)
	require.Equal(
		t,
		"[PAYLOAD OMITTED: exceeds report byte limit]",
		largeReport.Command,
	)
	require.Zero(t, customCalls)
}

type namedSecret string
type namedSecretBytes []byte

type unsupportedSecretOutput struct {
	Token string `json:"token"`
	Ch    chan int
}
type secretStringer struct{}

func (secretStringer) String() string {
	return "token=stringer-secret-value"
}

func TestRedactorHandlesNamedAndUnsupportedValues(t *testing.T) {
	redactor := NewRedactor()

	value, count := redactor.RedactValue(namedSecret("token=named-secret-value"))
	require.Positive(t, count)
	require.IsType(t, namedSecret(""), value)
	require.NotContains(t, string(value.(namedSecret)), "named-secret-value")

	value, count = redactor.RedactValue(
		namedSecretBytes("password=named-byte-secret"),
	)
	require.Positive(t, count)
	require.IsType(t, namedSecretBytes{}, value)
	require.NotContains(t, string(value.(namedSecretBytes)), "named-byte-secret")

	value, count = redactor.RedactValue(errors.New("token=error-secret-value"))
	require.Positive(t, count)
	require.NotContains(t, value.(error).Error(), "error-secret-value")
	value, count = redactor.RedactValue(secretStringer{})
	require.Positive(t, count)
	require.IsType(t, "", value)
	require.NotContains(t, value.(string), "stringer-secret-value")

	value, count = redactor.RedactValue(unsupportedSecretOutput{
		Token: "unsupported-secret-value",
		Ch:    make(chan int),
	})
	require.Equal(t, 1, count)
	require.Equal(t, RedactedValue, value)
}

var errWrappedTool = errors.New("wrapped tool failed")

type secretErrorTool struct{}

func (secretErrorTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (secretErrorTool) Call(context.Context, []byte) (any, error) {
	return nil, fmt.Errorf("token=wrapped-error-secret: %w", errWrappedTool)
}

func TestWrappedCallableRedactsErrorAndPreservesIs(t *testing.T) {
	guard, err := New(testPolicy(), withTrustedWorkspaceOutputLimit(4096))
	require.NoError(t, err)
	wrapped, err := WrapCallableTool(secretErrorTool{}, guard)
	require.NoError(t, err)

	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"go test ./tool/safety","timeout_sec":10}`),
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "wrapped-error-secret")
	require.Contains(t, err.Error(), RedactedValue)
	require.ErrorIs(t, err, errWrappedTool)
	require.Nil(t, errors.Unwrap(err))
}

type hiddenSecretError struct {
	Token string
	cause error
}

func (e *hiddenSecretError) Error() string {
	return "wrapped operation failed"
}

func (e *hiddenSecretError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

type hiddenSecretErrorTool struct{}

func (hiddenSecretErrorTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (hiddenSecretErrorTool) Call(context.Context, []byte) (any, error) {
	return nil, &hiddenSecretError{
		Token: "json-hidden-error-token",
		cause: errWrappedTool,
	}
}

func TestWrappedCallableFailsClosedForHiddenErrorSecret(t *testing.T) {
	guard, err := New(testPolicy(), withTrustedWorkspaceOutputLimit(4096))
	require.NoError(t, err)
	wrapped, err := WrapCallableTool(hiddenSecretErrorTool{}, guard)
	require.NoError(t, err)

	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"go test ./tool/safety","timeout_sec":10}`),
	)

	require.Error(t, err)
	require.NotContains(t, err.Error(), "json-hidden-error-token")
	require.Contains(t, err.Error(), RedactedValue)
	require.ErrorIs(t, err, errWrappedTool)
	require.Nil(t, errors.Unwrap(err))
}

type combinedSecretErrorTool struct{}

func (combinedSecretErrorTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (combinedSecretErrorTool) Call(context.Context, []byte) (any, error) {
	return nil, fmt.Errorf(
		"password=builtin-error-secret tenant-error-secret: %w",
		errWrappedTool,
	)
}

func TestWrappedCallableComposesDefaultAndCustomErrorRedaction(t *testing.T) {
	guard, err := New(
		testPolicy(),
		withTrustedWorkspaceOutputLimit(4096),
		WithRedactor(literalRedactor{secret: "tenant-error-secret"}),
	)
	require.NoError(t, err)
	wrapped, err := WrapCallableTool(combinedSecretErrorTool{}, guard)
	require.NoError(t, err)

	_, err = wrapped.Call(
		context.Background(),
		[]byte(`{"command":"go test ./tool/safety","timeout_sec":10}`),
	)

	require.Error(t, err)
	require.NotContains(t, err.Error(), "builtin-error-secret")
	require.NotContains(t, err.Error(), "tenant-error-secret")
	require.ErrorIs(t, err, errWrappedTool)
	require.Nil(t, errors.Unwrap(err))
}
func TestShellDoubleSlashPathIsNotTreatedAsAComment(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)
	report, err := guard.Scan(context.Background(), Request{
		ToolName:       "execute_code",
		Backend:        BackendCode,
		Language:       "sh",
		Script:         "//bin/rm -rf /",
		TimeoutMS:      1000,
		MaxOutputBytes: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, report.Decision)
	require.True(t, hasRule(report, "destructive.delete"))
}

func TestWhitespaceSessionInputAndScriptLineLimitFailClosed(t *testing.T) {
	policy := testPolicy()
	policy.Limits.MaxScriptLines = 2
	customCalls := 0
	guard, err := New(policy, WithRule(RuleFunc(func(
		context.Context,
		Request,
		Policy,
	) []Match {
		customCalls++
		return nil
	})))
	require.NoError(t, err)

	sessionReport, err := guard.Scan(context.Background(), Request{
		ToolName:       "skill_write_stdin",
		Backend:        BackendSkill,
		SessionInput:   "\n",
		TimeoutMS:      1000,
		MaxOutputBytes: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, sessionReport.Decision)
	require.True(t, hasRule(sessionReport, "session.input"))

	customCalls = 0
	scriptReport, err := guard.Scan(context.Background(), Request{
		ToolName:       "execute_code",
		Backend:        BackendCode,
		Language:       "python",
		Script:         "print(1)\nprint(2)\nprint(3)",
		TimeoutMS:      1000,
		MaxOutputBytes: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, scriptReport.Decision)
	require.True(t, hasRule(scriptReport, "limits.script_lines"))
	require.Zero(t, customCalls)
}
