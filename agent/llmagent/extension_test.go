//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// fakeExt is a hand-rolled extension.Extension used to drive the
// agent-scoped wiring. Each capability category is opt-in via
// nil/empty checks so individual tests can construct an extension
// that only registers the pieces they care about (one BeforeModel
// hook, one tool, one ToolResultMessages converter, …).
type fakeExt struct {
	name    string
	tools   []tool.Tool
	beforeM []model.BeforeModelCallbackStructured
	afterM  []model.AfterModelCallbackStructured
	beforeA []agent.BeforeAgentCallbackStructured
	beforeT []tool.BeforeToolCallbackStructured
}

func (e *fakeExt) Name() string { return e.name }

func (e *fakeExt) Register(r *extension.Registry) {
	if len(e.tools) > 0 {
		r.Tools(e.tools...)
	}
	for _, cb := range e.beforeM {
		r.BeforeModel(cb)
	}
	for _, cb := range e.afterM {
		r.AfterModel(cb)
	}
	for _, cb := range e.beforeA {
		r.BeforeAgent(cb)
	}
	for _, cb := range e.beforeT {
		r.BeforeTool(cb)
	}
}

// hookOnlyExt registers no tools, exercising the "callbacks but
// no tool contributions" path that the extensionContributedTools
// cache must keep empty for OutputSchema-compatible extensions.
type hookOnlyExt struct{ name string }

func (e *hookOnlyExt) Name() string                 { return e.name }
func (e *hookOnlyExt) Register(*extension.Registry) {}

// echoTool is the smallest possible tool we can register; the
// extension-wiring tests do not actually call it, so its body is
// irrelevant — only the declaration name matters.
func echoTool(name string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (string, error) { return name, nil },
		function.WithName(name),
		function.WithDescription("echo "+name),
	)
}

// TestWithExtensions_Absent_NoEffect is the regression baseline:
// constructing without WithExtensions must behave exactly as it
// did before this feature existed — no auto-tools, no synthesised
// callbacks.
func TestWithExtensions_Absent_NoEffect(t *testing.T) {
	user := echoTool("user")
	a := New("a", WithTools([]tool.Tool{user}))

	tools := a.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, "user", tools[0].Declaration().Name)
	assert.Nil(t, a.option.AgentCallbacks)
	assert.Nil(t, a.option.ModelCallbacks)
	assert.Nil(t, a.option.ToolCallbacks)
	assert.Empty(t, a.option.extensionContributedTools)
}

// TestWithExtensions_Tools_AppendedAsFramework verifies that
// extension-contributed tools land in Tools() but NOT in UserTools()
// — extensions are framework injections, not user registrations.
func TestWithExtensions_Tools_AppendedAsFramework(t *testing.T) {
	user := echoTool("user")
	extT := echoTool("from_extension")
	e := &fakeExt{name: "e", tools: []tool.Tool{extT}}

	a := New("a", WithTools([]tool.Tool{user}), WithExtensions(e))

	assert.ElementsMatch(t,
		[]string{"user", "from_extension"},
		toolNames(a.Tools()),
	)
	assert.Equal(t, []string{"user"}, toolNames(a.UserTools()),
		"extension tools must NOT be reported as user tools")
}

// TestWithExtensions_Tools_NameCollisionDropsExtension documents
// the dedup contract: when an extension tries to contribute a
// name that is already taken by a user (or framework) tool, the
// extension's copy is silently dropped. The earlier entry wins
// and we never produce two declarations for the same name (LLM
// providers reject that).
func TestWithExtensions_Tools_NameCollisionDropsExtension(t *testing.T) {
	user := echoTool("shared")
	extDup := echoTool("shared")
	e := &fakeExt{name: "e", tools: []tool.Tool{extDup}}

	a := New("a", WithTools([]tool.Tool{user}), WithExtensions(e))

	tools := a.Tools()
	require.Len(t, tools, 1)
	require.Same(t, user, tools[0],
		"on collision the user-registered tool must win, extension copy is dropped")
}

func TestWithExtensions_Tools_DedupAgainstLaterFrameworkTools(t *testing.T) {
	extAwait := echoTool("await_user_reply")
	extTransfer := echoTool("transfer_to_agent")
	e := &fakeExt{
		name:  "e",
		tools: []tool.Tool{extAwait, extTransfer},
	}

	a := New("a",
		WithAwaitUserReplyTool(true),
		WithSubAgents([]agent.Agent{&mockAgent{name: "sub"}}),
		WithExtensions(e),
	)

	tools := a.Tools()
	await := findTool(tools, "await_user_reply")
	require.NotNil(t, await)
	require.NotSame(t, extAwait, await,
		"framework await_user_reply must win over an extension collision")
	assert.Equal(t, 1, countToolName(tools, "await_user_reply"))

	transfer := findTool(tools, "transfer_to_agent")
	require.NotNil(t, transfer)
	require.NotSame(t, extTransfer, transfer,
		"framework transfer_to_agent must win over an extension collision")
	assert.Equal(t, 1, countToolName(tools, "transfer_to_agent"))
}

// TestWithExtensions_ModelCallbacks_OrderIsUserThenExtension
// asserts the documented merge order. The order matters: a user
// callback that returns a CustomResponse stops the chain by
// default, so installing user-side first preserves the user's
// authority over extension code.
func TestWithExtensions_ModelCallbacks_OrderIsUserThenExtension(t *testing.T) {
	var trail []string
	usercb := model.NewCallbacks()
	usercb.RegisterBeforeModel(model.BeforeModelCallbackStructured(
		func(_ context.Context, _ *model.BeforeModelArgs) (
			*model.BeforeModelResult, error,
		) {
			trail = append(trail, "user")
			return nil, nil
		},
	))
	e := &fakeExt{
		name: "e",
		beforeM: []model.BeforeModelCallbackStructured{
			func(_ context.Context, _ *model.BeforeModelArgs) (
				*model.BeforeModelResult, error,
			) {
				trail = append(trail, "extension")
				return nil, nil
			},
		},
	}

	a := New("a", WithModelCallbacks(usercb), WithExtensions(e))
	require.NotNil(t, a.option.ModelCallbacks)
	_, err := a.option.ModelCallbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"user", "extension"}, trail,
		"merge order must be user → extension so user keeps the first chance to short-circuit")
}

// TestWithExtensions_AgentCallbacks_OrderIsUserThenExtension
// mirrors the model case for agent callbacks. Even though sub-
// agents do not inherit agent-level extensions (by design), it is
// still important that the user vs extension order is honoured
// for the host agent.
func TestWithExtensions_AgentCallbacks_OrderIsUserThenExtension(t *testing.T) {
	var trail []string
	usercb := agent.NewCallbacks()
	usercb.RegisterBeforeAgent(agent.BeforeAgentCallbackStructured(
		func(_ context.Context, _ *agent.BeforeAgentArgs) (
			*agent.BeforeAgentResult, error,
		) {
			trail = append(trail, "user")
			return nil, nil
		},
	))
	e := &fakeExt{
		name: "e",
		beforeA: []agent.BeforeAgentCallbackStructured{
			func(_ context.Context, _ *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				trail = append(trail, "extension")
				return nil, nil
			},
		},
	}

	a := New("a", WithAgentCallbacks(usercb), WithExtensions(e))
	require.NotNil(t, a.option.AgentCallbacks)
	_, err := a.option.AgentCallbacks.RunBeforeAgent(
		context.Background(), &agent.BeforeAgentArgs{},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"user", "extension"}, trail)
}

// TestWithExtensions_ToolCallbacks_OrderAndConverterPreserved
// verifies the tool-callback merge in two dimensions: the
// BeforeTool chain follows user→extension order, and a user-
// supplied ToolResultMessages converter survives when the
// extension does not install one of its own. The converter is
// deliberately a single-slot field (not a chain) and the merge
// mirrors that.
func TestWithExtensions_ToolCallbacks_OrderAndConverterPreserved(t *testing.T) {
	var trail []string
	usercb := tool.NewCallbacks()
	usercb.RegisterBeforeTool(tool.BeforeToolCallbackStructured(
		func(_ context.Context, _ *tool.BeforeToolArgs) (
			*tool.BeforeToolResult, error,
		) {
			trail = append(trail, "user")
			return nil, nil
		},
	))
	usercb.ToolResultMessages = func(
		_ context.Context, _ *tool.ToolResultMessagesInput,
	) (any, error) {
		return nil, nil
	}

	e := &fakeExt{
		name: "e",
		beforeT: []tool.BeforeToolCallbackStructured{
			func(_ context.Context, _ *tool.BeforeToolArgs) (
				*tool.BeforeToolResult, error,
			) {
				trail = append(trail, "extension")
				return nil, nil
			},
		},
	}

	a := New("a", WithToolCallbacks(usercb), WithExtensions(e))
	require.NotNil(t, a.option.ToolCallbacks)
	_, err := a.option.ToolCallbacks.RunBeforeTool(
		context.Background(), &tool.BeforeToolArgs{ToolName: "x"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"user", "extension"}, trail)
	assert.NotNil(t, a.option.ToolCallbacks.ToolResultMessages,
		"user converter must survive when extension does not supply one")
}

// TestWithExtensions_MultipleExtensions_OrderPreserved exercises
// the deterministic registration order across more than one
// extension — both for tools (relevant for transcript stability)
// and for the callback chain (relevant for chained guardrails).
func TestWithExtensions_MultipleExtensions_OrderPreserved(t *testing.T) {
	var trail []string
	mk := func(label string) model.BeforeModelCallbackStructured {
		return func(_ context.Context, _ *model.BeforeModelArgs) (
			*model.BeforeModelResult, error,
		) {
			trail = append(trail, label)
			return nil, nil
		}
	}
	e1 := &fakeExt{
		name:    "e1",
		tools:   []tool.Tool{echoTool("t1")},
		beforeM: []model.BeforeModelCallbackStructured{mk("e1")},
	}
	e2 := &fakeExt{
		name:    "e2",
		tools:   []tool.Tool{echoTool("t2")},
		beforeM: []model.BeforeModelCallbackStructured{mk("e2")},
	}

	a := New("a", WithExtensions(e1, e2))

	assert.Equal(t, []string{"t1", "t2"}, toolNames(a.Tools()),
		"extension tools must keep WithExtensions call order")
	require.NotNil(t, a.option.ModelCallbacks)
	_, err := a.option.ModelCallbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"e1", "e2"}, trail)
}

// TestWithExtensions_DuplicateName_Panics verifies the unique-name
// invariant enforced inside extension.Collect propagates up to
// New() as a panic. The cost of relaxing this would be subtle
// debugging when two extensions with the same Name silently
// shadow each other in metrics; the cost of panicking at New()
// time is bounded and surfaces the misconfiguration immediately.
func TestWithExtensions_DuplicateName_Panics(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic on duplicate extension name")
		err, ok := r.(error)
		require.True(t, ok, "panic payload must be an error, got %T", r)
		assert.True(t, strings.Contains(err.Error(), "duplicate name"),
			"panic message should mention duplicate name: %v", err)
	}()
	_ = New("a", WithExtensions(&fakeExt{name: "dup"}, &fakeExt{name: "dup"}))
}

// TestWithExtensions_NilExtensionEntries_Skipped confirms that
// callers who pass nil entries (often the result of conditional
// construction such as `optional() extension.Extension`) get a
// no-op rather than a panic. This is the only place we silently
// swallow nils; New() itself rejects the duplicate-name case
// loudly.
func TestWithExtensions_NilExtensionEntries_Skipped(t *testing.T) {
	a := New("a", WithExtensions(nil, nil))
	assert.Nil(t, a.option.AgentCallbacks)
	assert.Nil(t, a.option.ModelCallbacks)
	assert.Nil(t, a.option.ToolCallbacks)
	assert.Empty(t, a.option.extensionContributedTools)
}

// TestWithExtensions_HookOnlyExtension_NoToolsContributed verifies
// the "callbacks only, no tools" branch: an extension whose
// Register call does not invoke r.Tools(...) still has its hooks
// wired but contributes no tools — and therefore stays compatible
// with WithOutputSchema (see OutputSchema guard regression test).
func TestWithExtensions_HookOnlyExtension_NoToolsContributed(t *testing.T) {
	a := New("a", WithExtensions(&hookOnlyExt{name: "h"}))
	assert.Empty(t, a.option.extensionContributedTools)
}

// TestWithExtensions_Tools_AppearInInvocationToolSurface is the
// regression guard for a dispatch-surface mismatch:
// extension-contributed tools were being surfaced by LLMAgent.Tools()
// but the invocation-time path —
// getFilteredTools → InvocationToolSurface → userToolsForInvocation
// — only walks a.userToolNames-gated entries. Extension tools are
// deliberately NOT folded into userToolNames (they are framework-
// managed, like Knowledge / workspace_exec / Skill auto-injection),
// so without an explicit append in InvocationToolSurface they fell
// off a cliff before reaching model.Request.Tools. The model then
// received an incomplete tools list while LLMAgent.Tools()
// insisted everything was wired.
//
// This test asserts the post-fix invariant: extension tools must
// appear in InvocationToolSurface's returned tool list, and must
// still NOT appear in userToolNames (so UserTools / FilterTools
// continue to treat them as framework injections).
func TestWithExtensions_Tools_AppearInInvocationToolSurface(t *testing.T) {
	user := echoTool("user_only")
	extAlpha := echoTool("ext_alpha")
	extBeta := echoTool("ext_beta")
	e := &fakeExt{name: "e", tools: []tool.Tool{extAlpha, extBeta}}

	a := New("a", WithTools([]tool.Tool{user}), WithExtensions(e))

	tools, userToolNames := a.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
		),
	)

	names := toolNames(tools)
	assert.ElementsMatch(t,
		[]string{"user_only", "ext_alpha", "ext_beta"},
		names,
		"extension-contributed tools must surface alongside user tools "+
			"on the invocation path that feeds model.Request.Tools",
	)
	assert.True(t, userToolNames["user_only"],
		"user-registered tool must remain marked as user")
	assert.False(t, userToolNames["ext_alpha"],
		"extension tool must NOT be marked as user (framework-managed)")
	assert.False(t, userToolNames["ext_beta"],
		"extension tool must NOT be marked as user (framework-managed)")
}

// TestWithExtensions_Tools_InvocationSurfaceDedupAgainstUser
// verifies the dedup contract holds on the invocation path too:
// when an extension tries to contribute a name a user tool
// already owns, the invocation surface must contain exactly one
// entry (the user's), not two — duplicate names blow up most LLM
// providers.
func TestWithExtensions_Tools_InvocationSurfaceDedupAgainstUser(t *testing.T) {
	user := echoTool("shared")
	extDup := echoTool("shared")
	e := &fakeExt{name: "e", tools: []tool.Tool{extDup}}

	a := New("a", WithTools([]tool.Tool{user}), WithExtensions(e))

	tools, _ := a.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
		),
	)
	require.Len(t, tools, 1,
		"name collision must produce exactly one entry on the invocation surface")
	assert.Same(t, user, tools[0],
		"user-registered tool must win the collision")
}

func TestWithExtensions_Tools_InvocationSurfaceDedupAgainstLaterFrameworkTool(t *testing.T) {
	extWorkspaceExec := echoTool("workspace_exec")
	e := &fakeExt{name: "e", tools: []tool.Tool{extWorkspaceExec}}

	a := New("a",
		WithCodeExecutor(&stubExec{}),
		WithExtensions(e),
	)

	tools, userToolNames := a.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
		),
	)

	got := findTool(tools, "workspace_exec")
	require.NotNil(t, got)
	require.NotSame(t, extWorkspaceExec, got,
		"framework workspace_exec must win over an extension collision")
	assert.Equal(t, 1, countToolName(tools, "workspace_exec"),
		"extension collisions must not produce duplicate declarations")
	assert.False(t, userToolNames["workspace_exec"],
		"framework-managed workspace_exec must not be marked as user")
}

func TestWithExtensions_Tools_InvocationSurfaceDedupAgainstTransferTool(t *testing.T) {
	extTransfer := echoTool("transfer_to_agent")
	e := &fakeExt{name: "e", tools: []tool.Tool{extTransfer}}

	a := New("a",
		WithSubAgents([]agent.Agent{&mockAgent{name: "sub"}}),
		WithExtensions(e),
	)

	tools, userToolNames := a.InvocationToolSurface(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
		),
	)

	got := findTool(tools, "transfer_to_agent")
	require.NotNil(t, got)
	require.NotSame(t, extTransfer, got,
		"framework transfer_to_agent must win over an extension collision")
	assert.Equal(t, 1, countToolName(tools, "transfer_to_agent"),
		"extension collisions must not produce duplicate declarations")
	assert.False(t, userToolNames["transfer_to_agent"],
		"framework-managed transfer_to_agent must not be marked as user")
}

// The block below covers the small nil-edge branches inside
// mergeAgentCallbacks / mergeModelCallbacks / mergeToolCallbacks
// that the larger ordering tests (Test*_OrderIsUserThenExtension)
// only exercise via the "both non-nil" path. The merge functions
// short-circuit when one or both sides have no content; without
// these tests those branches stay dark and the patch coverage
// CodeRabbit / codecov computes for new code on extension.go
// falls below the project threshold.

func TestMergeAgentCallbacks_BothNil_ReturnsNil(t *testing.T) {
	assert.Nil(t, mergeAgentCallbacks(nil, nil),
		"merging two absent callbacks must yield nil — callers downstream "+
			"branch on \"!= nil\" before iterating")
}

func TestMergeAgentCallbacks_OnlyOneSide_ReturnsTheNonNilSideAsIs(t *testing.T) {
	a := agent.NewCallbacks()
	a.RegisterBeforeAgent(func(_ context.Context, _ *agent.BeforeAgentArgs) (
		*agent.BeforeAgentResult, error,
	) {
		return nil, nil
	})

	require.Same(t, a, mergeAgentCallbacks(a, nil),
		"merging with a nil/empty side must return the other side untouched (no copy)")
	require.Same(t, a, mergeAgentCallbacks(nil, a),
		"same identity-preserving shortcut applies when nil is on the left")

	// Empty (allocated but no hooks) on the b-side must also be
	// treated as absent — otherwise extension authors that
	// defensively agent.NewCallbacks() would force a copy even
	// when they registered nothing.
	empty := agent.NewCallbacks()
	require.Same(t, a, mergeAgentCallbacks(a, empty),
		"empty (zero-content) callbacks bundle must be treated as absent")
}

func TestMergeAgentCallbacks_PreservesUserOptionsAndDoesNotMutateOriginal(t *testing.T) {
	var trail []string
	user := agent.NewCallbacks(agent.WithContinueOnResponse(true))
	user.RegisterBeforeAgent(func(_ context.Context, _ *agent.BeforeAgentArgs) (
		*agent.BeforeAgentResult, error,
	) {
		trail = append(trail, "user")
		return &agent.BeforeAgentResult{
			CustomResponse: &model.Response{ID: "user"},
		}, nil
	})

	ext := agent.NewCallbacks()
	ext.RegisterBeforeAgent(func(_ context.Context, _ *agent.BeforeAgentArgs) (
		*agent.BeforeAgentResult, error,
	) {
		trail = append(trail, "extension")
		return &agent.BeforeAgentResult{
			CustomResponse: &model.Response{ID: "extension"},
		}, nil
	})

	merged := mergeAgentCallbacks(user, ext)
	require.NotSame(t, user, merged,
		"merge must clone before appending extension callbacks")
	result, err := merged.RunBeforeAgent(context.Background(), &agent.BeforeAgentArgs{})
	require.NoError(t, err)
	require.Equal(t, "extension", result.CustomResponse.ID)
	require.Equal(t, []string{"user", "extension"}, trail,
		"ContinueOnResponse from user callbacks must survive the merge")

	trail = nil
	result, err = user.RunBeforeAgent(context.Background(), &agent.BeforeAgentArgs{})
	require.NoError(t, err)
	require.Equal(t, "user", result.CustomResponse.ID)
	require.Equal(t, []string{"user"}, trail,
		"merged chain must not mutate the caller-owned callbacks")
}

func TestMergeModelCallbacks_BothNil_ReturnsNil(t *testing.T) {
	assert.Nil(t, mergeModelCallbacks(nil, nil))
}

func TestMergeModelCallbacks_OnlyOneSide_ReturnsTheNonNilSideAsIs(t *testing.T) {
	a := model.NewCallbacks()
	a.RegisterBeforeModel(func(_ context.Context, _ *model.BeforeModelArgs) (
		*model.BeforeModelResult, error,
	) {
		return nil, nil
	})

	require.Same(t, a, mergeModelCallbacks(a, nil))
	require.Same(t, a, mergeModelCallbacks(nil, a))
	require.Same(t, a, mergeModelCallbacks(a, model.NewCallbacks()),
		"empty bundle on b-side must be treated as absent")
}

func TestMergeModelCallbacks_PreservesUserOptionsAndDoesNotMutateOriginal(t *testing.T) {
	var trail []string
	user := model.NewCallbacks(model.WithContinueOnResponse(true))
	user.RegisterBeforeModel(func(_ context.Context, _ *model.BeforeModelArgs) (
		*model.BeforeModelResult, error,
	) {
		trail = append(trail, "user")
		return &model.BeforeModelResult{
			CustomResponse: &model.Response{ID: "user"},
		}, nil
	})

	ext := model.NewCallbacks()
	ext.RegisterBeforeModel(func(_ context.Context, _ *model.BeforeModelArgs) (
		*model.BeforeModelResult, error,
	) {
		trail = append(trail, "extension")
		return &model.BeforeModelResult{
			CustomResponse: &model.Response{ID: "extension"},
		}, nil
	})

	merged := mergeModelCallbacks(user, ext)
	require.NotSame(t, user, merged,
		"merge must clone before appending extension callbacks")
	result, err := merged.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.Equal(t, "extension", result.CustomResponse.ID)
	require.Equal(t, []string{"user", "extension"}, trail,
		"ContinueOnResponse from user callbacks must survive the merge")

	trail = nil
	result, err = user.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.Equal(t, "user", result.CustomResponse.ID)
	require.Equal(t, []string{"user"}, trail,
		"merged chain must not mutate the caller-owned callbacks")
}

func TestMergeToolCallbacks_BothNil_ReturnsNil(t *testing.T) {
	assert.Nil(t, mergeToolCallbacks(nil, nil))
}

func TestMergeToolCallbacks_OnlyOneSide_ReturnsTheNonNilSideAsIs(t *testing.T) {
	a := tool.NewCallbacks()
	a.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		return nil, nil
	})

	require.Same(t, a, mergeToolCallbacks(a, nil))
	require.Same(t, a, mergeToolCallbacks(nil, a))
}

func TestMergeToolCallbacks_PreservesUserOptionsAndDoesNotMutateOriginal(t *testing.T) {
	var trail []string
	user := tool.NewCallbacks(tool.WithContinueOnResponse(true))
	user.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		trail = append(trail, "user")
		return &tool.BeforeToolResult{CustomResult: "user"}, nil
	})

	ext := tool.NewCallbacks()
	ext.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		trail = append(trail, "extension")
		return &tool.BeforeToolResult{CustomResult: "extension"}, nil
	})

	merged := mergeToolCallbacks(user, ext)
	require.NotSame(t, user, merged,
		"merge must clone before appending extension callbacks")
	result, err := merged.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{ToolName: "x"},
	)
	require.NoError(t, err)
	require.Equal(t, "extension", result.CustomResult)
	require.Equal(t, []string{"user", "extension"}, trail,
		"ContinueOnResponse from user callbacks must survive the merge")

	trail = nil
	result, err = user.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{ToolName: "x"},
	)
	require.NoError(t, err)
	require.Equal(t, "user", result.CustomResult)
	require.Equal(t, []string{"user"}, trail,
		"merged chain must not mutate the caller-owned callbacks")
}

// TestMergeToolCallbacks_ToolResultMessages_BWinsWhenBothSet pins
// the documented "b wins" semantics for the single-slot
// ToolResultMessages converter. Without this assertion an honest
// refactor could flip it to "a wins" silently.
func TestMergeToolCallbacks_ToolResultMessages_BWinsWhenBothSet(t *testing.T) {
	aMarker := "a-converter"
	bMarker := "b-converter"

	a := tool.NewCallbacks()
	a.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		return nil, nil
	})
	a.ToolResultMessages = func(_ context.Context, _ *tool.ToolResultMessagesInput) (any, error) {
		return aMarker, nil
	}

	b := tool.NewCallbacks()
	b.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		return nil, nil
	})
	b.ToolResultMessages = func(_ context.Context, _ *tool.ToolResultMessagesInput) (any, error) {
		return bMarker, nil
	}

	out := mergeToolCallbacks(a, b)
	require.NotNil(t, out)
	require.NotNil(t, out.ToolResultMessages,
		"merged callbacks must carry forward a ToolResultMessages converter")

	got, err := out.ToolResultMessages(context.Background(), &tool.ToolResultMessagesInput{})
	require.NoError(t, err)
	assert.Equal(t, bMarker, got,
		"when both sides set ToolResultMessages, b must win — see mergeToolCallbacks docstring")
}

// TestMergeToolCallbacks_ToolResultMessages_OnlyOneSide verifies
// the "winner is the only non-nil side" branches that the BWins
// test above does not exercise.
func TestMergeToolCallbacks_ToolResultMessages_OnlyOneSide(t *testing.T) {
	aMarker := "a"
	a := tool.NewCallbacks()
	a.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		return nil, nil
	})
	a.ToolResultMessages = func(_ context.Context, _ *tool.ToolResultMessagesInput) (any, error) {
		return aMarker, nil
	}

	// b has hooks but no ToolResultMessages.
	b := tool.NewCallbacks()
	b.RegisterBeforeTool(func(_ context.Context, _ *tool.BeforeToolArgs) (
		*tool.BeforeToolResult, error,
	) {
		return nil, nil
	})

	out := mergeToolCallbacks(a, b)
	require.NotNil(t, out.ToolResultMessages)
	got, _ := out.ToolResultMessages(context.Background(), &tool.ToolResultMessagesInput{})
	assert.Equal(t, aMarker, got,
		"when only a has ToolResultMessages, it must survive into the merge")
}

func toolNames(tools []tool.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Declaration().Name)
	}
	return out
}

func countToolName(tools []tool.Tool, name string) int {
	var n int
	for _, tl := range tools {
		if tl.Declaration().Name == name {
			n++
		}
	}
	return n
}
