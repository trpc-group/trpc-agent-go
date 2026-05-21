//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package extension

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeExt is the minimum-viable Extension used across tests. We
// deliberately keep it self-contained — no real plugin imports —
// so the extension package can be exercised in isolation from
// llmagent / runner.
type fakeExt struct {
	name     string
	register func(*Registry)
}

func (f fakeExt) Name() string         { return f.name }
func (f fakeExt) Register(r *Registry) { f.register(r) }

// fakeTool implements tool.Tool to give Tools(...) something to
// accept. The Tool interface only needs Declaration() in many code
// paths but we satisfy the runtime-callable contract too so the
// Contributions.Tools slice is usable end-to-end if a downstream test
// wants to call it.
type fakeTool struct{ name string }

func (f fakeTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}

func TestCollect_Empty_ReturnsNilContributions(t *testing.T) {
	b, err := Collect(nil)
	require.NoError(t, err, "empty input must not error")
	assert.Nil(t, b,
		"Collect(nil) must return nil so consumers can short-circuit the merge pipeline; "+
			"returning an empty *Contributions would force every consumer to add an IsEmpty() guard.")

	b2, err := Collect([]Extension{})
	require.NoError(t, err)
	assert.Nil(t, b2, "Collect([]Extension{}) must behave identically to Collect(nil)")
}

func TestCollect_RejectsNilExtension(t *testing.T) {
	_, err := Collect([]Extension{
		fakeExt{name: "ok", register: func(*Registry) {}},
		nil,
		fakeExt{name: "also-ok", register: func(*Registry) {}},
	})
	require.Error(t, err, "a nil entry anywhere in the slice must surface as an error, not a silent skip")
	assert.Contains(t, err.Error(), "nil extension at index 1",
		"error must point at the offending index so users can fix the install site quickly")
}

func TestCollect_RejectsEmptyName(t *testing.T) {
	_, err := Collect([]Extension{
		fakeExt{name: "", register: func(*Registry) {}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty name",
		"name is required for log correlation and dedup; missing it is a programming error, not a recoverable runtime condition")
}

func TestCollect_RejectsDuplicateNames(t *testing.T) {
	_, err := Collect([]Extension{
		fakeExt{name: "duplicate", register: func(*Registry) {}},
		fakeExt{name: "duplicate", register: func(*Registry) {}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate name "duplicate"`,
		"duplicate detection must call out the colliding name so the user knows which install to remove")
}

func TestCollect_ConvertsRegisterPanicToError(t *testing.T) {
	calledAfterPanic := false

	contrib, err := Collect([]Extension{
		fakeExt{name: "ok", register: func(r *Registry) {
			r.Tools(fakeTool{name: "ok-tool"})
		}},
		fakeExt{name: "boom", register: func(*Registry) {
			panic("register failed")
		}},
		fakeExt{name: "after", register: func(*Registry) {
			calledAfterPanic = true
		}},
	})

	require.Error(t, err)
	assert.Nil(t, contrib,
		"panicking Register must discard the partially-built contributions")
	assert.False(t, calledAfterPanic,
		"Collect must stop immediately after a Register panic")
	assert.Contains(t, err.Error(), `panic during register "boom" at index 1`)
	assert.Contains(t, err.Error(), "register failed")
	assert.Contains(t, err.Error(), "goroutine",
		"error should include a stack trace for construction-time diagnostics")
}

func TestCollect_PreservesToolInstallOrder(t *testing.T) {
	a := fakeTool{name: "alpha"}
	b := fakeTool{name: "beta"}
	g := fakeTool{name: "gamma"}

	contrib, err := Collect([]Extension{
		fakeExt{name: "ext1", register: func(r *Registry) { r.Tools(a, b) }},
		fakeExt{name: "ext2", register: func(r *Registry) { r.Tools(g) }},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)
	require.Len(t, contrib.Tools(), 3,
		"Collect must preserve every tool — dedup is the consuming agent's job, not Registry's")
	assert.Equal(t, "alpha", contrib.Tools()[0].Declaration().Name,
		"install order must be preserved so LLMAgent's earlier-wins dedup is deterministic")
	assert.Equal(t, "beta", contrib.Tools()[1].Declaration().Name)
	assert.Equal(t, "gamma", contrib.Tools()[2].Declaration().Name)
}

func TestContributions_AccessorsReturnCopies(t *testing.T) {
	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext",
			register: func(r *Registry) {
				r.Tools(fakeTool{name: "alpha"})
				r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return nil, nil
				})
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)

	tools := contrib.Tools()
	require.Len(t, tools, 1)
	tools[0] = fakeTool{name: "mutated"}
	assert.Equal(t, "alpha", contrib.Tools()[0].Declaration().Name,
		"Tools must return a copy so consumers cannot mutate stored contributions")

	modelCallbacks := contrib.ModelCallbacks()
	require.NotNil(t, modelCallbacks)
	modelCallbacks.BeforeModel = nil
	require.Len(t, contrib.ModelCallbacks().BeforeModel, 1,
		"callback accessors must return clones so consumers cannot mutate stored contributions")
}

func TestRegistry_Tools_SilentlyDropsNil(t *testing.T) {
	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext",
			register: func(r *Registry) {
				r.Tools(nil, fakeTool{name: "real"}, nil)
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)
	require.Len(t, contrib.Tools(), 1,
		"nil tool entries must be filtered so callers can build slices with conditional inclusion without per-entry guards")
	assert.Equal(t, "real", contrib.Tools()[0].Declaration().Name)
}

func TestRegistry_Tools_NoopOnNilRegistry(t *testing.T) {
	// Defensive: callers should never see a nil Registry from
	// Collect, but Register implementations might in the future
	// pass r through helpers that don't nil-check. The Registry
	// methods must tolerate that gracefully rather than panic
	// inside extension code.
	var r *Registry
	require.NotPanics(t, func() { r.Tools(fakeTool{name: "x"}) },
		"Tools on a nil Registry must be a no-op, not a panic")
	assert.Equal(t, "", r.Name(),
		"Name on a nil Registry returns the empty string by convention")
}

func TestCollect_MergesCallbacksAcrossExtensions_InInstallOrder(t *testing.T) {
	var order []string

	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext-a",
			register: func(r *Registry) {
				r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
					order = append(order, "before-agent:a")
					return nil, nil
				})
				r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
					order = append(order, "after-agent:a")
					return nil, nil
				})
				r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					order = append(order, "before-model:a")
					return nil, nil
				})
				r.AfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					order = append(order, "after-model:a")
					return nil, nil
				})
				r.BeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
					order = append(order, "before-tool:a")
					return nil, nil
				})
				r.AfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
					order = append(order, "after-tool:a")
					return nil, nil
				})
			},
		},
		fakeExt{
			name: "ext-b",
			register: func(r *Registry) {
				r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
					order = append(order, "before-agent:b")
					return nil, nil
				})
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)

	// Drive a real *agent.Callbacks chain to confirm order matches
	// install order — this is the contract LLMAgent depends on
	// when it folds the contributions into the agent's callback set.
	_, _ = contrib.AgentCallbacks().RunBeforeAgent(context.Background(), nil)
	assert.Equal(t,
		[]string{"before-agent:a", "before-agent:b"}, order,
		"merged callbacks must fire in install order; the contract guarantees user-side ordering of extensions",
	)
}

func TestRegistry_Callbacks_WrapErrorsWithExtensionName(t *testing.T) {
	// One test drives every callback method's error-wrapping path
	// instead of six near-identical tests — the wrapping logic is
	// the same code shape repeated per surface, so the regression
	// risk is "did somebody drop the wrap on one of them?" which
	// table-style coverage handles cleanly.
	sentinel := errors.New("boom")

	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext",
			register: func(r *Registry) {
				r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
					return nil, sentinel
				})
				r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
					return nil, sentinel
				})
				r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return nil, sentinel
				})
				r.AfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					return nil, sentinel
				})
				r.BeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
					return nil, sentinel
				})
				r.AfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
					return nil, sentinel
				})
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)

	ctx := context.Background()
	type runResult struct {
		surface string
		err     error
	}
	results := []runResult{}

	_, rErr := contrib.AgentCallbacks().RunBeforeAgent(ctx, &agent.BeforeAgentArgs{})
	results = append(results, runResult{"before-agent", rErr})
	_, rErr = contrib.AgentCallbacks().RunAfterAgent(ctx, &agent.AfterAgentArgs{})
	results = append(results, runResult{"after-agent", rErr})
	_, rErr = contrib.ModelCallbacks().RunBeforeModel(ctx, &model.BeforeModelArgs{})
	results = append(results, runResult{"before-model", rErr})
	_, rErr = contrib.ModelCallbacks().RunAfterModel(ctx, &model.AfterModelArgs{})
	results = append(results, runResult{"after-model", rErr})
	_, rErr = contrib.ToolCallbacks().RunBeforeTool(ctx, &tool.BeforeToolArgs{ToolName: "x"})
	results = append(results, runResult{"before-tool", rErr})
	_, rErr = contrib.ToolCallbacks().RunAfterTool(ctx, &tool.AfterToolArgs{ToolName: "x"})
	results = append(results, runResult{"after-tool", rErr})

	for _, r := range results {
		require.Error(t, r.err,
			"%s: callback errors must propagate through the merged chain unchanged in semantics", r.surface)
		assert.ErrorIs(t, r.err, sentinel,
			"%s: wrapping must use %%w so errors.Is keeps working", r.surface)
		assert.Contains(t, r.err.Error(), "ext:",
			"%s: the extension's Name must appear in the wrapped error so observability can attribute failures correctly", r.surface)
	}
}

// TestRegistry_Callbacks_PassThroughOnSuccess complements the
// error-wrapping test by covering the "callback succeeded → return
// (result, nil)" branch of every wrapper. Without this the wrappers
// stay at 50% coverage because Go reports the success-return as a
// distinct statement from the error-return.
func TestRegistry_Callbacks_PassThroughOnSuccess(t *testing.T) {
	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext",
			register: func(r *Registry) {
				r.BeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
					return &agent.BeforeAgentResult{}, nil
				})
				r.AfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
					return &agent.AfterAgentResult{}, nil
				})
				r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return &model.BeforeModelResult{}, nil
				})
				r.AfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					return &model.AfterModelResult{}, nil
				})
				r.BeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{}, nil
				})
				r.AfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
					return &tool.AfterToolResult{}, nil
				})
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)

	ctx := context.Background()
	_, rErr := contrib.AgentCallbacks().RunBeforeAgent(ctx, &agent.BeforeAgentArgs{})
	assert.NoError(t, rErr, "BeforeAgent success must surface as nil error through the wrapper")
	_, rErr = contrib.AgentCallbacks().RunAfterAgent(ctx, &agent.AfterAgentArgs{})
	assert.NoError(t, rErr, "AfterAgent success must surface as nil error through the wrapper")
	_, rErr = contrib.ModelCallbacks().RunBeforeModel(ctx, &model.BeforeModelArgs{})
	assert.NoError(t, rErr, "BeforeModel success must surface as nil error through the wrapper")
	_, rErr = contrib.ModelCallbacks().RunAfterModel(ctx, &model.AfterModelArgs{})
	assert.NoError(t, rErr, "AfterModel success must surface as nil error through the wrapper")
	_, rErr = contrib.ToolCallbacks().RunBeforeTool(ctx, &tool.BeforeToolArgs{ToolName: "x"})
	assert.NoError(t, rErr, "BeforeTool success must surface as nil error through the wrapper")
	_, rErr = contrib.ToolCallbacks().RunAfterTool(ctx, &tool.AfterToolArgs{ToolName: "x"})
	assert.NoError(t, rErr, "AfterTool success must surface as nil error through the wrapper")
}

func TestRegistry_Callbacks_IgnoreNilCallback(t *testing.T) {
	// Skip-on-nil mirrors plugin.Registry: extensions can
	// conditionally register hooks without guarding every call,
	// which keeps option-driven extension constructors readable.
	contrib, err := Collect([]Extension{
		fakeExt{
			name: "ext",
			register: func(r *Registry) {
				r.BeforeAgent(nil)
				r.AfterAgent(nil)
				r.BeforeModel(nil)
				r.AfterModel(nil)
				r.BeforeTool(nil)
				r.AfterTool(nil)
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, contrib)
	assert.True(t, contrib.IsEmpty(),
		"a Registry that received only nil callbacks must produce an empty Contributions so the consumer can take the no-op fast path")
}

func TestContributions_IsEmpty(t *testing.T) {
	assert.True(t, (*Contributions)(nil).IsEmpty(),
		"a nil *Contributions is empty by convention (Collect returns nil on no input)")
	assert.True(t, (&Contributions{}).IsEmpty(),
		"zero-value Contributions with nil callback containers must be empty")

	withTool := &Contributions{tools: []tool.Tool{fakeTool{name: "x"}}}
	assert.False(t, withTool.IsEmpty(),
		"a Contributions that carries any tool is non-empty even if no callbacks were registered")

	withModelCB := &Contributions{modelCallbacks: model.NewCallbacks()}
	withModelCB.modelCallbacks.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		return nil, nil
	})
	assert.False(t, withModelCB.IsEmpty(),
		"a Contributions that carries any model callback is non-empty")

	// Cover the agent + tool callback branches separately so
	// IsEmpty's per-surface short-circuits each receive at least
	// one observation. A non-empty AgentCallbacks must keep the
	// Contributions non-empty even when ModelCallbacks/ToolCallbacks/
	// Tools are all empty, and vice versa for ToolCallbacks.
	withAgentCB := &Contributions{agentCallbacks: agent.NewCallbacks()}
	withAgentCB.agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		return nil, nil
	})
	assert.False(t, withAgentCB.IsEmpty(),
		"a Contributions that carries any agent callback is non-empty")

	withToolCB := &Contributions{toolCallbacks: tool.NewCallbacks()}
	withToolCB.toolCallbacks.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		return nil, nil
	})
	assert.False(t, withToolCB.IsEmpty(),
		"a Contributions that carries any tool callback is non-empty")
}

func TestRegistry_Name_RoundTrip(t *testing.T) {
	var got string
	_, err := Collect([]Extension{
		fakeExt{
			name: "my-extension",
			register: func(r *Registry) {
				got = r.Name()
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "my-extension", got,
		"Registry.Name must return the same string Collect read from Extension.Name so extensions can use it as a metric/state-key prefix")
}
