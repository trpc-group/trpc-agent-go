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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSkillExtractorNormalizesCompleteExecutionSchema(t *testing.T) {
	request, handled, err := extractSkillExec(&tool.PermissionRequest{
		ToolName:   "skill_exec",
		ToolCallID: "call-skill",
		Arguments: []byte(`{
			"skill":"code-review",
			"command":"go test ./...",
			"cwd":"pkg",
			"stdin":"yes",
			"editor_text":"review text",
			"env":{"CI":"true"},
			"timeout":30,
			"tty":true,
			"yield_ms":250,
			"poll_lines":40,
			"inputs":[{
				"from":"host://C:/tmp/input.txt",
				"to":"inputs/input.txt",
				"mode":"copy",
				"pin":true
			}],
			"output_files":["out/*.txt"],
			"outputs":{
				"globs":["out/**/*.json"],
				"MaxFiles":77,
				"MaxFileBytes":2048,
				"MaxTotalBytes":4096,
				"Save":true,
				"NameTemplate":"review/",
				"Inline":true
			},
			"save_as_artifacts":true,
			"omit_inline_content":true,
			"artifact_prefix":"audit/"
		}`),
		Metadata: tool.ToolMetadata{OpenWorld: true},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "call-skill", request.ToolCallID)
	require.Equal(t, BackendSkill, request.Backend)
	require.Equal(t, "code-review", request.Skill)
	require.Equal(t, intPointer(250), request.YieldMS)
	require.Equal(t, 40, request.PollLines)
	require.Equal(t, "review text", request.EditorText)
	require.Equal(t, []InputSpec{{
		From: "host://C:/tmp/input.txt",
		To:   "inputs/input.txt",
		Mode: "copy",
		Pin:  true,
	}}, request.Inputs)
	require.Equal(t, []string{"out/*.txt"}, request.OutputFiles)
	require.Equal(t, &OutputSpec{
		Globs:         []string{"out/**/*.json"},
		MaxFiles:      77,
		MaxFileBytes:  2048,
		MaxTotalBytes: 4096,
		Save:          true,
		NameTemplate:  "review/",
		Inline:        true,
	}, request.Outputs)
	require.True(t, request.SaveArtifacts)
	require.True(t, request.OmitInline)
	require.Equal(t, "audit/", request.ArtifactPrefix)
	require.Zero(t, request.MaxOutputBytes)

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	serialized := string(encoded)
	require.Contains(t, serialized, `"from":"host://C:/tmp/input.txt"`)
	require.Contains(t, serialized, `"max_files":77`)
	require.Contains(t, serialized, `"save_as_artifacts":true`)
	require.NotContains(t, serialized, `"From"`)
	require.NotContains(t, serialized, `"MaxFiles"`)
}

func TestSkillExtractorAcceptsSnakeCaseOutputLimits(t *testing.T) {
	request, handled, err := extractSkillRun(&tool.PermissionRequest{
		ToolName: "skill_run",
		Arguments: []byte(`{
			"command":"go test ./...",
			"outputs":{
				"globs":["out/*.txt"],
				"max_files":3,
				"max_file_bytes":100,
				"max_total_bytes":200
			}
		}`),
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 3, request.Outputs.MaxFiles)
	require.Equal(t, int64(100), request.Outputs.MaxFileBytes)
	require.Equal(t, int64(200), request.Outputs.MaxTotalBytes)
}

func TestStructuredExecutionFieldsFailClosed(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nsecret-material"
	tests := []struct {
		name     string
		mutate   func(*Request)
		decision tool.PermissionAction
		ruleID   string
	}{
		{
			name: "host input requires review",
			mutate: func(req *Request) {
				req.Inputs = []InputSpec{{From: "host://C:/tmp/input.txt"}}
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "skill.input.host_source",
		},
		{
			name: "host credential is denied",
			mutate: func(req *Request) {
				req.Inputs = []InputSpec{{From: "host://C:/Users/test/.ssh/id_rsa"}}
			},
			decision: tool.PermissionActionDeny,
			ruleID:   "credential.access",
		},
		{
			name: "input destination traversal is denied",
			mutate: func(req *Request) {
				req.Inputs = []InputSpec{{
					From: "artifact://input.txt", To: "../../outside.txt",
				}}
			},
			decision: tool.PermissionActionDeny,
			ruleID:   "skill.input.destination",
		},
		{
			name: "output traversal is denied",
			mutate: func(req *Request) {
				req.OutputFiles = []string{"../../outside.txt"}
			},
			decision: tool.PermissionActionDeny,
			ruleID:   "skill.output.path",
		},
		{
			name: "broad output glob requires review",
			mutate: func(req *Request) {
				req.OutputFiles = []string{"out/../**/*"}
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "skill.output.broad_glob",
		},
		{
			name: "excessive output file count requires review",
			mutate: func(req *Request) {
				req.Outputs = &OutputSpec{
					Globs: []string{"out/*.txt"}, MaxFiles: 1_000_000_000,
					MaxFileBytes: 1024, MaxTotalBytes: 4096,
				}
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "skill.output.limits",
		},
		{
			name: "artifact persistence requires review",
			mutate: func(req *Request) {
				req.SaveArtifacts = true
				req.ArtifactPrefix = "review/"
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "artifact.persistence",
		},
		{
			name: "editor secret is denied",
			mutate: func(req *Request) {
				req.EditorText = privateKey
			},
			decision: tool.PermissionActionDeny,
			ruleID:   "credential.access",
		},
	}
	guard, err := New(testPolicy())
	require.NoError(t, err)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := Request{
				ToolName: "skill_run", Backend: BackendSkill,
				Command: "go test ./...", TimeoutMS: 10_000,
				MaxOutputBytes: 4096,
			}
			test.mutate(&request)
			report, scanErr := guard.Scan(context.Background(), request)
			require.NoError(t, scanErr)
			require.Equal(t, test.decision, report.Decision, report.Matches)
			require.True(t, hasRule(report, test.ruleID), report.Matches)
		})
	}
}

func TestOversizedPermissionArgumentsSkipExtractor(t *testing.T) {
	calls := 0
	guard, err := New(testPolicy(), WithExtractor(
		"custom_exec",
		ExtractorFunc(func(*tool.PermissionRequest) (Request, bool, error) {
			calls++
			return Request{}, true, nil
		}),
	))
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "custom_exec",
		Arguments: []byte(strings.Repeat("x", maxPermissionArgumentsBytes+1)),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.arguments_too_large")
	require.Zero(t, calls)
}

type typedNilExtractor struct{}

func (*typedNilExtractor) Extract(*tool.PermissionRequest) (Request, bool, error) {
	panic("typed nil extractor must not be called")
}

type typedNilRule struct{}

func (*typedNilRule) Evaluate(context.Context, Request, Policy) []Match {
	panic("typed nil rule must not be called")
}

func TestNilExtensionAdaptersFailClosed(t *testing.T) {
	var extractorFunc ExtractorFunc
	_, handled, err := extractorFunc.Extract(nil)
	require.ErrorIs(t, err, errNilExtractorFunc)
	require.True(t, handled)

	var extractor *typedNilExtractor
	guard, err := New(testPolicy(), WithExtractor("custom_exec", extractor))
	require.NoError(t, err)
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "custom_exec", Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.invalid_extractor")

	var ruleFunc RuleFunc
	matches := ruleFunc.Evaluate(context.Background(), Request{}, testPolicy())
	require.Len(t, matches, 1)
	require.Equal(t, "rule.invalid", matches[0].RuleID)

	var rule *typedNilRule
	guard, err = New(testPolicy(), WithRule(rule))
	require.NoError(t, err)
	report, err := guard.Scan(
		context.Background(),
		commandRequest(BackendWorkspace, "go test ./tool/safety"),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "rule.invalid"))
}

type typedNilContext struct{}

func (*typedNilContext) Deadline() (time.Time, bool) { panic("nil context used") }
func (*typedNilContext) Done() <-chan struct{}       { panic("nil context used") }
func (*typedNilContext) Err() error                  { panic("nil context used") }
func (*typedNilContext) Value(any) any               { panic("nil context used") }

func TestNilContextsAreNormalized(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)
	request := commandRequest(BackendWorkspace, "go test ./tool/safety")

	report, err := guard.Scan(nil, request)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, report.Decision)

	var typedNil *typedNilContext
	report, err = guard.Scan(typedNil, request)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, report.Decision)
}

func TestPermissionRequestIdentityIsAuthoritative(t *testing.T) {
	var seen Request
	guard, err := New(
		testPolicy(),
		WithExtractor("workspace_exec", ExtractorFunc(func(
			*tool.PermissionRequest,
		) (Request, bool, error) {
			return Request{
				ToolName: "lookup", ToolCallID: "forged-call",
				Backend:  BackendWorkspace,
				Metadata: tool.ToolMetadata{Destructive: true},
			}, true, nil
		})),
		WithRule(RuleFunc(func(_ context.Context, request Request, _ Policy) []Match {
			seen = request
			return nil
		})),
	)
	require.NoError(t, err)
	metadata := tool.ToolMetadata{OpenWorld: true}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "workspace_exec", ToolCallID: "real-call",
		Arguments: []byte(`{}`), Metadata: metadata,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.empty")
	require.Equal(t, "workspace_exec", seen.ToolName)
	require.Equal(t, "real-call", seen.ToolCallID)
	require.Equal(t, metadata, seen.Metadata)
}

func TestUnknownExecutorSchemaSignals(t *testing.T) {
	strongFields := []string{"command", "argv", "program", "executable"}
	guard, err := New(testPolicy())
	require.NoError(t, err)
	for _, field := range strongFields {
		t.Run(field, func(t *testing.T) {
			decision, checkErr := guard.CheckToolPermission(
				context.Background(),
				permissionRequestWithSchema("lookup", field, tool.ToolMetadata{}),
			)
			require.NoError(t, checkErr)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, "tool.unknown_executor")
		})
	}
	for _, field := range []string{"args", "code"} {
		t.Run("ordinary "+field, func(t *testing.T) {
			decision, checkErr := guard.CheckToolPermission(
				context.Background(),
				permissionRequestWithSchema("lookup", field, tool.ToolMetadata{}),
			)
			require.NoError(t, checkErr)
			require.Equal(t, tool.PermissionActionAllow, decision.Action)
		})
	}
	for _, request := range []*tool.PermissionRequest{
		permissionRequestWithSchema("job_runner", "args", tool.ToolMetadata{}),
		permissionRequestWithSchema("lookup", "args", tool.ToolMetadata{OpenWorld: true}),
	} {
		decision, checkErr := guard.CheckToolPermission(context.Background(), request)
		require.NoError(t, checkErr)
		require.Equal(t, tool.PermissionActionAsk, decision.Action)
	}
}

func TestExecutionLikeUnknownToolNameFailsClosedWithoutMetadata(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)
	decision, err := guard.CheckToolPermission(
		context.Background(),
		permissionRequestWithSchema(
			"dangerous_exec", "input", tool.ToolMetadata{},
		),
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "tool.unknown_executor")
}

func permissionRequestWithSchema(
	name string,
	field string,
	metadata tool.ToolMetadata,
) *tool.PermissionRequest {
	return &tool.PermissionRequest{
		ToolName: name,
		Declaration: &tool.Declaration{
			Name: name,
			InputSchema: &tool.Schema{Properties: map[string]*tool.Schema{
				field: {Type: "string"},
			}},
		},
		Metadata: metadata,
	}
}

type declarationTool struct {
	declaration *tool.Declaration
	panicValue  any
}

func (t *declarationTool) Declaration() *tool.Declaration {
	if t.panicValue != nil {
		panic(t.panicValue)
	}
	return t.declaration
}

func TestPermissionDeclarationFallbackAndFailures(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)
	executionTool := &declarationTool{declaration: &tool.Declaration{
		Name: "terminal",
		InputSchema: &tool.Schema{Properties: map[string]*tool.Schema{
			"command": {Type: "string"},
		}},
	}}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool: executionTool,
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "tool.unknown_executor")

	var typedNilTool *declarationTool
	for name, pendingTool := range map[string]tool.Tool{
		"typed nil":       typedNilTool,
		"panic":           &declarationTool{panicValue: "boom"},
		"nil declaration": &declarationTool{},
	} {
		t.Run(name, func(t *testing.T) {
			decision, checkErr := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{ToolName: "workspace_exec", Tool: pendingTool},
			)
			require.NoError(t, checkErr)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, "input.invalid_tool")
		})
	}
}

func TestTimeoutConversionsSaturateInsteadOfWrapping(t *testing.T) {
	maximum := time.Duration(1<<63 - 1)
	direct := Request{
		ToolName: "workspace_exec", Backend: BackendWorkspace,
		Command: "go test ./tool/safety", TimeoutMS: int64(1<<63 - 1),
		MaxOutputBytes: 4096,
	}
	require.Equal(t, maximum, direct.EffectiveTimeout())
	guard, err := New(testPolicy())
	require.NoError(t, err)
	report, err := guard.Scan(context.Background(), direct)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "limits.timeout"))

	maxInt := int(^uint(0) >> 1)
	extracted, handled, err := extractWorkspaceExec(&tool.PermissionRequest{
		ToolName: "workspace_exec",
		Arguments: []byte(fmt.Sprintf(
			`{"command":"go test ./tool/safety","timeout":%d}`,
			maxInt,
		)),
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, maximum, extracted.EffectiveTimeout())
	extracted.MaxOutputBytes = 4096
	report, err = guard.Scan(context.Background(), extracted)
	require.NoError(t, err)
	require.True(t, hasRule(report, "limits.timeout"))
}

func TestStructuredOutputDefaultsRequireReview(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)

	tests := []struct {
		name    string
		request Request
		ruleID  string
	}{
		{
			name: "legacy output files have no declarative limits",
			request: Request{
				ToolName: "skill_run", Backend: BackendSkill,
				Command: "go test ./...", OutputFiles: []string{"out/*.txt"},
				TimeoutMS: 10_000, MaxOutputBytes: 4096,
			},
			ruleID: "skill.output.limits",
		},
		{
			name: "empty globs use the runtime default collection scope",
			request: Request{
				ToolName: "skill_run", Backend: BackendSkill,
				Command: "go test ./...", Outputs: &OutputSpec{
					MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 2048,
				},
				TimeoutMS: 10_000, MaxOutputBytes: 4096,
			},
			ruleID: "skill.output.broad_glob",
		},
		{
			name: "zero collection limits use larger runtime defaults",
			request: Request{
				ToolName: "skill_run", Backend: BackendSkill,
				Command: "go test ./...", Outputs: &OutputSpec{
					Globs: []string{"out/*.txt"},
				},
				TimeoutMS: 10_000, MaxOutputBytes: 4096,
			},
			ruleID: "skill.output.limits",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(context.Background(), test.request)
			require.NoError(t, scanErr)
			require.Equal(t, tool.PermissionActionAsk, report.Decision)
			require.True(t, hasRule(report, test.ruleID), report.Matches)
		})
	}
}

func TestPermissionRequestWithoutIdentityRequiresReview(t *testing.T) {
	guard, err := New(testPolicy())
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{Arguments: []byte(`{}`)},
	)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.invalid_tool")
}

func TestCustomExecutionBackendRequiresPayload(t *testing.T) {
	guard, err := New(testPolicy(), WithExtractor(
		"custom_exec",
		ExtractorFunc(func(*tool.PermissionRequest) (Request, bool, error) {
			return Request{Backend: BackendWorkspace}, true, nil
		}),
	))
	require.NoError(t, err)

	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "custom_exec", Arguments: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, decision.Action)
	require.Contains(t, decision.Reason, "input.empty")
}

func TestOversizedEnvironmentSkipsDeepAndCustomScanning(t *testing.T) {
	policy := testPolicy()
	policy.Limits.MaxScriptBytes = 64
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

	request := commandRequest(BackendWorkspace, "go test ./tool/safety")
	request.Env = map[string]string{
		"CI": strings.Repeat("x", policy.Limits.MaxScriptBytes+1),
	}
	report, err := guard.Scan(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAsk, report.Decision)
	require.True(t, hasRule(report, "limits.structured_execution_fields"))
	require.Zero(t, customCalls)
}

func TestBuiltInExtractorsRejectUnknownFields(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
	}{
		{name: "workspace_exec", arguments: `{"command":"go test ./...","future_mount":"host"}`},
		{name: "exec_command", arguments: `{"command":"go test ./...","future_mount":"host"}`},
		{name: "execute_code", arguments: `{"code_blocks":[{"language":"go","code":"package main"}],"network_mode":"host"}`},
		{name: "skill_run", arguments: `{"skill":"review","command":"go test ./...","mounts":[]}`},
		{name: "skill_exec", arguments: `{"skill":"review","command":"go test ./...","mounts":[]}`},
		{name: "write_stdin", arguments: `{"session_id":"host-1","future_signal":"TERM"}`},
		{name: "kill_session", arguments: `{"session_id":"host-1","future_signal":"KILL"}`},
		{name: "workspace_write_stdin", arguments: `{"session_id":"workspace-1","future_signal":"TERM"}`},
		{name: "workspace_kill_session", arguments: `{"session_id":"workspace-1","future_signal":"KILL"}`},
		{name: "skill_write_stdin", arguments: `{"session_id":"skill-1","future_signal":"TERM"}`},
		{name: "skill_poll_session", arguments: `{"session_id":"skill-1","future_offset":42}`},
		{name: "skill_kill_session", arguments: `{"session_id":"skill-1","future_signal":"KILL"}`},
	}
	extractors := defaultExtractors()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractor, ok := extractors[test.name]
			require.True(t, ok)
			_, handled, err := extractor.Extract(&tool.PermissionRequest{
				ToolName:  test.name,
				Arguments: []byte(test.arguments),
			})
			require.True(t, handled)
			require.ErrorContains(t, err, "unknown field")
		})
	}
}

func TestBuiltInExtractorsRejectFieldsIgnoredByRuntime(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
	}{
		{name: "host timeout alias belongs to workspace", toolName: "exec_command",
			arguments: `{"command":"go test ./...","timeout":30}`},
		{name: "host stdin is not implemented", toolName: "exec_command",
			arguments: `{"command":"go test ./...","stdin":"yes"}`},
		{name: "workspace workdir alias is not implemented", toolName: "workspace_exec",
			arguments: `{"command":"go test ./...","workdir":"pkg"}`},
		{name: "noninteractive skill tty", toolName: "skill_run",
			arguments: `{"skill":"review","command":"go test ./...","tty":true}`},
		{name: "host kill does not accept chars", toolName: "kill_session",
			arguments: `{"session_id":"host-1","chars":"yes"}`},
		{name: "skill poll does not accept chars", toolName: "skill_poll_session",
			arguments: `{"session_id":"skill-1","chars":"yes"}`},
		{name: "nested output field", toolName: "skill_exec",
			arguments: `{"skill":"review","command":"go test ./...","outputs":{"globs":["out/**"],"future_upload":true}}`},
		{name: "nested input field", toolName: "skill_exec",
			arguments: `{"skill":"review","command":"go test ./...","inputs":[{"from":"artifact://notes","future_mount":true}]}`},
	}
	extractors := defaultExtractors()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractor := extractors[test.toolName]
			_, handled, err := extractor.Extract(&tool.PermissionRequest{
				ToolName:  test.toolName,
				Arguments: []byte(test.arguments),
			})
			require.True(t, handled)
			require.ErrorContains(t, err, "unknown field")
		})
	}
}

func TestWriteStdinExtractorsPreserveSessionControls(t *testing.T) {
	tests := []struct {
		name      string
		extract   ExtractorFunc
		toolName  string
		arguments string
		backend   Backend
		sessionID string
		yieldMS   *int
		pollLines int
		input     string
	}{
		{
			name: "host snake case", extract: extractHostWriteStdin,
			toolName:  "write_stdin",
			arguments: `{"session_id":"host-1","chars":"yes","yield-time_ms":25,"append_newline":true}`,
			backend:   BackendHost, sessionID: "host-1", yieldMS: intPointer(25),
			input: "yes\n",
		},
		{
			name: "workspace legacy aliases", extract: extractWorkspaceWriteStdin,
			toolName:  "workspace_write_stdin",
			arguments: `{"sessionId":"workspace-1","yieldMs":30}`,
			backend:   BackendWorkspace, sessionID: "workspace-1", yieldMS: intPointer(30),
		},
		{
			name: "skill poll controls", extract: extractSkillWriteStdin,
			toolName:  "skill_write_stdin",
			arguments: `{"session_id":"skill-1","chars":"","yield_ms":35,"poll_lines":12}`,
			backend:   BackendSkill, sessionID: "skill-1", yieldMS: intPointer(35),
			pollLines: 12,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, handled, err := test.extract.Extract(&tool.PermissionRequest{
				ToolName:  test.toolName,
				Arguments: []byte(test.arguments),
			})
			require.NoError(t, err)
			require.True(t, handled)
			require.Equal(t, test.backend, request.Backend)
			require.Equal(t, test.sessionID, request.SessionID)
			require.Equal(t, test.yieldMS, request.YieldMS)
			require.Equal(t, test.pollLines, request.PollLines)
			require.Equal(t, test.input, request.SessionInput)
		})
	}
}

func TestBuiltInSessionControlExtractors(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		backend   Backend
		sessionID string
		yieldMS   *int
		pollLines int
		ruleID    string
	}{
		{
			name: "kill_session", arguments: `{"sessionId":"host-kill"}`,
			backend: BackendHost, sessionID: "host-kill",
			ruleID: "host.session_control",
		},
		{
			name:      "workspace_kill_session",
			arguments: `{"session_id":"workspace-kill"}`,
			backend:   BackendWorkspace, sessionID: "workspace-kill",
			ruleID: "session.ownership",
		},
		{
			name:      "skill_poll_session",
			arguments: `{"session_id":"skill-poll","yield_ms":45,"poll_lines":18}`,
			backend:   BackendSkill, sessionID: "skill-poll",
			yieldMS: intPointer(45), pollLines: 18,
			ruleID: "session.ownership",
		},
		{
			name:      "skill_kill_session",
			arguments: `{"session_id":"skill-kill"}`,
			backend:   BackendSkill, sessionID: "skill-kill",
			ruleID: "session.ownership",
		},
	}
	extractors := defaultExtractors()
	guard, err := New(testPolicy())
	require.NoError(t, err)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractor, ok := extractors[test.name]
			require.True(t, ok)
			request, handled, err := extractor.Extract(&tool.PermissionRequest{
				ToolName:  test.name,
				Arguments: []byte(test.arguments),
			})
			require.NoError(t, err)
			require.True(t, handled)
			require.Equal(t, test.backend, request.Backend)
			require.Equal(t, test.sessionID, request.SessionID)
			require.Equal(t, test.yieldMS, request.YieldMS)
			require.Equal(t, test.pollLines, request.PollLines)

			decision, checkErr := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  test.name,
					Arguments: []byte(test.arguments),
				},
			)
			require.NoError(t, checkErr)
			require.Equal(t, tool.PermissionActionAsk, decision.Action)
			require.Contains(t, decision.Reason, test.ruleID)
		})
	}
}

func TestSessionControlsFailClosed(t *testing.T) {
	policy := testPolicy()
	policy.Commands.Allowed = append(policy.Commands.Allowed, "scp")
	guard, err := New(policy)
	require.NoError(t, err)

	tests := []struct {
		name     string
		request  Request
		decision tool.PermissionAction
		ruleID   string
	}{
		{
			name: "host poll requires review",
			request: Request{
				ToolName: "write_stdin", Backend: BackendHost,
				SessionID: "host-1",
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "host.session_control",
		},
		{
			name: "missing session identifier",
			request: Request{
				ToolName: "workspace_write_stdin", Backend: BackendWorkspace,
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "session.id",
		},
		{
			name: "missing skill identifier",
			request: Request{
				ToolName: "skill_exec", Backend: BackendSkill,
				Command: "go test ./...", TimeoutMS: 10_000,
				MaxOutputBytes: 4096,
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "skill.id",
		},
		{
			name: "missing poll session identifier",
			request: Request{
				ToolName: "skill_poll_session", Backend: BackendSkill,
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "session.id",
		},
		{
			name: "negative poll is denied",
			request: Request{
				ToolName: "skill_write_stdin", Backend: BackendSkill,
				SessionID: "skill-1", PollLines: -1,
			},
			decision: tool.PermissionActionDeny,
			ruleID:   "limits.invalid",
		},
		{
			name: "large yield requires review",
			request: Request{
				ToolName: "skill_write_stdin", Backend: BackendSkill,
				SessionID: "skill-1",
				YieldMS:   intPointer((policy.Limits.MaxTimeoutSeconds + 1) * 1000),
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.session_yield",
		},
		{
			name: "large poll requires review",
			request: Request{
				ToolName: "skill_write_stdin", Backend: BackendSkill,
				SessionID: "skill-1",
				PollLines: policy.Limits.MaxScriptLines + 1,
			},
			decision: tool.PermissionActionAsk,
			ruleID:   "limits.session_poll",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(context.Background(), test.request)
			require.NoError(t, scanErr)
			require.Equal(t, test.decision, report.Decision, report.Matches)
			require.True(t, hasRule(report, test.ruleID), report.Matches)
		})
	}
}
