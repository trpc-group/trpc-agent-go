//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// DefaultNudgeFormatter is the only piece of model-facing prompt
// text this package owns. These tests pin its shape so a casual
// refactor cannot silently break the contract that the package
// doc / README advertise — specifically:
//
//   - the attempt/MaxRetries header is always present
//   - the in-progress + pending sub-lists appear only when
//     their slices are non-empty (no awkward empty headers)
//   - the escape-hatch name defaults correctly when the caller
//     left DeclareBlockerToolName empty
//   - both the todo-write tool and the declare-blocker tool name
//     appear in the trailing instruction (so the model knows the
//     exact identifiers it should call)

func TestDefaultNudgeFormatter_BothBuckets(t *testing.T) {
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          2,
		MaxRetries:             5,
		TodoToolName:           todo.DefaultToolName,
		DeclareBlockerToolName: "todo_declare_blocker",
		InProgress: []todo.Item{
			{Content: "Inspect pods", ActiveForm: "Inspecting pods"},
		},
		Pending: []todo.Item{
			{Content: "Propose fix"},
		},
	})

	assert.Contains(t, out, "attempt 2 of 5",
		"header must surface the attempt/MaxRetries pair verbatim")
	assert.Contains(t, out, "Currently in progress:")
	assert.Contains(t, out, "Inspecting pods (Inspect pods)")
	assert.Contains(t, out, "Still pending:")
	assert.Contains(t, out, "Propose fix")
	assert.Contains(t, out, todo.DefaultToolName,
		"trailing instruction must name todo_write")
	assert.Contains(t, out, "todo_declare_blocker",
		"trailing instruction must name the declare-blocker tool")
}

func TestDefaultNudgeFormatter_OnlyPending_OmitsInProgressHeader(t *testing.T) {
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          1,
		MaxRetries:             3,
		TodoToolName:           todo.DefaultToolName,
		DeclareBlockerToolName: "todo_declare_blocker",
		Pending: []todo.Item{
			{Content: "Run tests"},
		},
	})

	assert.NotContains(t, out, "Currently in progress:",
		"empty InProgress slice must skip the header entirely (no empty section)")
	assert.Contains(t, out, "Still pending:")
	assert.Contains(t, out, "Run tests")
}

func TestDefaultNudgeFormatter_OnlyInProgress_OmitsPendingHeader(t *testing.T) {
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          3,
		MaxRetries:             3,
		TodoToolName:           todo.DefaultToolName,
		DeclareBlockerToolName: "todo_declare_blocker",
		InProgress: []todo.Item{
			{Content: "Deploy staging", ActiveForm: "Deploying staging"},
		},
	})

	assert.Contains(t, out, "Currently in progress:")
	assert.NotContains(t, out, "Still pending:",
		"empty Pending slice must skip the header entirely")
}

func TestDefaultNudgeFormatter_BothEmpty_StillProducesValidNudge(t *testing.T) {
	// In practice AfterModel only invokes the formatter when at
	// least one list is non-empty (open items is the very
	// definition of "should block"), but the formatter must still
	// be robust to empty inputs — guards against a future caller
	// that forgets the precondition.
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          1,
		MaxRetries:             1,
		TodoToolName:           todo.DefaultToolName,
		DeclareBlockerToolName: "todo_declare_blocker",
	})

	assert.NotContains(t, out, "Currently in progress:")
	assert.NotContains(t, out, "Still pending:")
	// Trailing instruction is independent of list shape so it
	// must still appear.
	assert.Contains(t, out, todo.DefaultToolName)
	assert.Contains(t, out, "todo_declare_blocker")
}

func TestDefaultNudgeFormatter_EmptyToolName_FallsBackToDefault(t *testing.T) {
	// The caller (Enforcer) always passes a non-empty
	// DeclareBlockerToolName, but the formatter defends against
	// callers that forget. The branch is small and the default
	// is the source-of-truth constant, so we assert the constant
	// rather than re-derive the string here.
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber: 1,
		MaxRetries:    3,
		Pending:       []todo.Item{{Content: "x"}},
	})

	assert.Contains(t, out, DefaultDeclareBlockerToolName,
		"empty DeclareBlockerToolName must fall back to the package default")
	assert.Contains(t, out, todo.DefaultToolName,
		"empty TodoToolName must fall back to todo.DefaultToolName")
}

func TestDefaultNudgeFormatter_CustomTodoToolName(t *testing.T) {
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          1,
		MaxRetries:             3,
		TodoToolName:           "plan_update",
		DeclareBlockerToolName: "todo_declare_blocker",
		Pending:                []todo.Item{{Content: "Run tests"}},
	})

	assert.Contains(t, out, "plan_update",
		"default nudge must quote the configured todo tool name")
	assert.NotContains(t, out, "call "+todo.DefaultToolName,
		"default nudge must not point the model at an unavailable default tool")
}

// TestDefaultNudgeFormatter_ListEntryFormatting nails down the
// exact textual shape per item — the AfterForm/Content rendering
// is what the model has to map back to its plan items, so any
// drift here would degrade the nudge's effectiveness.
func TestDefaultNudgeFormatter_ListEntryFormatting(t *testing.T) {
	out := DefaultNudgeFormatter(NudgeContext{
		AttemptNumber:          1,
		MaxRetries:             3,
		TodoToolName:           todo.DefaultToolName,
		DeclareBlockerToolName: "todo_declare_blocker",
		InProgress: []todo.Item{
			{Content: "step one", ActiveForm: "doing step one"},
		},
		Pending: []todo.Item{
			{Content: "step two"},
			{Content: "step three"},
		},
	})

	// in-progress: "  - <ActiveForm> (<Content>)\n"
	assert.True(t,
		strings.Contains(out, "  - doing step one (step one)\n"),
		"in-progress entry must be rendered as '  - <ActiveForm> (<Content>)' with a trailing newline; got: %q", out)
	// pending: "  - <Content>\n"
	assert.True(t, strings.Contains(out, "  - step two\n"))
	assert.True(t, strings.Contains(out, "  - step three\n"))
}

// TestSplitByStatus_DropsCompletedAndSurfacesUnknown documents the
// visibility contract: completed items disappear from the nudge,
// but an unexpected non-completed status is still shown as pending
// so enforcement cannot block without giving the model an
// actionable item.
func TestSplitByStatus_DropsCompletedAndSurfacesUnknown(t *testing.T) {
	in := []todo.Item{
		{Content: "a", Status: todo.StatusInProgress},
		{Content: "b", Status: todo.StatusCompleted},
		{Content: "c", Status: todo.StatusPending},
		{Content: "d", Status: todo.StatusInProgress},
		{Content: "e", Status: todo.Status("blocked")},
	}
	inProg, pending := splitByStatus(in)
	assert.Equal(t, []todo.Item{
		{Content: "a", Status: todo.StatusInProgress},
		{Content: "d", Status: todo.StatusInProgress},
	}, inProg)
	assert.Equal(t, []todo.Item{
		{Content: "c", Status: todo.StatusPending},
		{Content: "e", Status: todo.Status("blocked")},
	}, pending)
}

// TestHasOpenItems pins the "no open" predicate; the enforcer
// uses it as the cheap fast-path before any allocation.
func TestHasOpenItems(t *testing.T) {
	assert.False(t, hasOpenItems(nil),
		"nil slice must be treated as no-open-items")
	assert.False(t, hasOpenItems([]todo.Item{}),
		"empty slice must be treated as no-open-items")
	assert.False(t, hasOpenItems([]todo.Item{
		{Content: "x", Status: todo.StatusCompleted},
	}), "all-completed list must be treated as no-open-items")
	assert.True(t, hasOpenItems([]todo.Item{
		{Content: "x", Status: todo.StatusPending},
	}))
	assert.True(t, hasOpenItems([]todo.Item{
		{Content: "x", Status: todo.StatusInProgress},
	}))
	assert.True(t, hasOpenItems([]todo.Item{
		{Content: "x", Status: todo.Status("blocked")},
	}), "unknown non-completed status must be treated as open and surfaced by splitByStatus")
}
