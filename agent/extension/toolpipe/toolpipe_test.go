//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolpipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- Engine Tests ---

func TestEngine_GrepBasic(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "line1 ERROR something\nline2 INFO ok\nline3 ERROR again"
	result, err := engine.Apply(context.Background(), input, "grep ERROR")
	require.NoError(t, err)
	assert.NotEmpty(t, result.Filter)
	assert.Equal(t, "grep ERROR", result.Filter)
	assert.Equal(t, "line1 ERROR something\nline3 ERROR again", result.Content)
}

func TestEngine_GrepCaseInsensitive(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "Error occurred\nINFO message\nerror again"
	result, err := engine.Apply(context.Background(), input, "grep -i error")
	require.NoError(t, err)
	assert.Equal(t, "Error occurred\nerror again", result.Content)
}

func TestEngine_GrepInvert(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "DEBUG line1\nERROR line2\nDEBUG line3"
	result, err := engine.Apply(context.Background(), input, "grep -v DEBUG")
	require.NoError(t, err)
	assert.Equal(t, "ERROR line2", result.Content)
}

func TestEngine_Head(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	lines := "line1\nline2\nline3\nline4\nline5"
	result, err := engine.Apply(context.Background(), lines, "head -3")
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3", result.Content)
}

func TestEngine_Tail(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	lines := "line1\nline2\nline3\nline4\nline5"
	result, err := engine.Apply(context.Background(), lines, "tail -2")
	require.NoError(t, err)
	assert.Equal(t, "line4\nline5", result.Content)
}

func TestEngine_Pipeline(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "ERROR a\nINFO b\nERROR c\nERROR d\nINFO e"
	result, err := engine.Apply(context.Background(), input, "grep ERROR | head -2")
	require.NoError(t, err)
	assert.Equal(t, "ERROR a\nERROR c", result.Content)
}

func TestEngine_JQ(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	input := `{"items":[{"name":"a","status":"ok"},{"name":"b","status":"failed"}]}`
	result, err := engine.Apply(context.Background(), input, `jq '.items[] | select(.status=="failed") | .name'`)
	require.NoError(t, err)
	assert.Equal(t, `"b"`, result.Content)
}

func TestEngine_DisallowedOp(t *testing.T) {
	cfg := defaultConfig()
	// jq is not in allowedOps by default
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "{}", "jq '.'")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestEngine_RejectRedirect(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep foo > /tmp/x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redirect")
}

func TestEngine_RejectUnknownCommand(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "curl http://evil.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

func TestEngine_MaxOutput(t *testing.T) {
	cfg := defaultConfig()
	cfg.maxOutput = 10
	engine := NewEngine(cfg)

	input := "0123456789abcdef"
	result, err := engine.Apply(context.Background(), input, "head -1")
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Len(t, result.Content, 10)
}

func TestEngine_EmptyFilter(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	result, err := engine.Apply(context.Background(), "hello", "")
	require.NoError(t, err)
	assert.Empty(t, result.Filter)
	assert.Equal(t, "hello", result.Content)
}

// --- ToolPipe Extension Tests ---

type mockTool struct {
	decl   *tool.Declaration
	result any
	err    error
}

func (m *mockTool) Declaration() *tool.Declaration { return m.decl }
func (m *mockTool) Call(_ context.Context, _ []byte) (any, error) {
	return m.result, m.err
}

// ctxWithAugmented creates a context that simulates BeforeModel having
// augmented the given tool names. Used by BeforeTool tests.
func ctxWithAugmented(names ...string) context.Context {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return context.WithValue(context.Background(), augmentedSetCtxKey{}, set)
}

func TestToolPipe_ShouldWrap(t *testing.T) {
	tp := New(WithToolNames("allowed_tool"))

	allowed := &mockTool{decl: &tool.Declaration{Name: "allowed_tool"}}
	notAllowed := &mockTool{decl: &tool.Declaration{Name: "other_tool"}}

	assert.True(t, tp.shouldWrap(allowed))
	assert.False(t, tp.shouldWrap(notAllowed))
}

func TestToolPipe_ShouldWrapPredicate(t *testing.T) {
	tp := New(WithToolScope(func(t tool.Tool) bool {
		return t.Declaration().Name == "dynamic_tool"
	}))

	inner := &mockTool{decl: &tool.Declaration{Name: "dynamic_tool"}}
	assert.True(t, tp.shouldWrap(inner))
}

// --- BeforeModel Tests ---

func TestToolPipe_BeforeModel_AugmentsSchema(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	inner := &mockTool{
		decl: &tool.Declaration{
			Name: "test_tool",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query": {Type: "string"},
				},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"test_tool": inner,
		},
	}
	args := &model.BeforeModelArgs{Request: req}
	_, err := tp.beforeModel(context.Background(), args)
	require.NoError(t, err)

	augmented := req.Tools["test_tool"]
	decl := augmented.Declaration()
	assert.NotNil(t, decl.InputSchema.Properties["result_filter"])
	assert.Equal(t, "string", decl.InputSchema.Properties["result_filter"].Type)
	// Original field preserved.
	assert.NotNil(t, decl.InputSchema.Properties["query"])
}

func TestToolPipe_BeforeModel_SkipsNotAllowed(t *testing.T) {
	tp := New(WithToolNames("allowed_only"))

	other := &mockTool{
		decl: &tool.Declaration{
			Name:        "other_tool",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"other_tool": other,
		},
	}
	args := &model.BeforeModelArgs{Request: req}
	_, err := tp.beforeModel(context.Background(), args)
	require.NoError(t, err)

	// Should remain unchanged.
	assert.Equal(t, other, req.Tools["other_tool"])
}

// --- BeforeTool + AfterTool Integration ---

func TestToolPipe_BeforeTool_ExtractsFilter(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	rawArgs, _ := json.Marshal(map[string]string{
		"query":         "test",
		"result_filter": "grep ERROR",
	})

	args := &tool.BeforeToolArgs{
		ToolName:  "test_tool",
		Arguments: rawArgs,
	}

	result, err := tp.beforeTool(ctxWithAugmented("test_tool"), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)
	require.NotNil(t, result.Context)

	// Modified args should not have result_filter.
	var cleaned map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &cleaned))
	assert.Equal(t, "test", cleaned["query"])
	assert.NotContains(t, cleaned, "result_filter")

	// Context should carry the filter state.
	state, ok := result.Context.Value(filterContextKey{}).(*filterState)
	assert.True(t, ok)
	assert.NotNil(t, state.pipeline)
	assert.Equal(t, "grep ERROR", state.filterExpr)
}

func TestToolPipe_AfterTool_AppliesFilter(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	// Simulate context from BeforeTool.
	pipeline, _ := tp.engine.parse("grep ERROR | head -1")
	ctx := context.WithValue(context.Background(), filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: "grep ERROR | head -1",
	})

	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "ERROR one\nINFO two\nERROR three",
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.NotEmpty(t, fr.Filter)
	assert.Equal(t, "grep ERROR | head -1", fr.Filter)
	assert.Equal(t, "ERROR one", fr.Content)
}

func TestToolPipe_AfterTool_NoFilterInContext(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "raw output",
	}

	result, err := tp.afterTool(context.Background(), args)
	require.NoError(t, err)
	assert.Nil(t, result) // No filtering applied.
}

// --- ExtractFilter Tests ---

func TestExtractFilter_Present(t *testing.T) {
	args := `{"query":"test","result_filter":"grep ERROR"}`
	clean, filter, err := extractFilter([]byte(args), "result_filter")
	require.NoError(t, err)
	assert.Equal(t, "grep ERROR", filter)

	var m map[string]any
	require.NoError(t, json.Unmarshal(clean, &m))
	assert.Equal(t, "test", m["query"])
	assert.NotContains(t, m, "result_filter")
}

func TestExtractFilter_NotPresent(t *testing.T) {
	args := `{"query":"test"}`
	clean, filter, err := extractFilter([]byte(args), "result_filter")
	require.NoError(t, err)
	assert.Equal(t, "", filter)
	assert.Equal(t, args, string(clean))
}

func TestExtractFilter_EmptyArgs(t *testing.T) {
	clean, filter, err := extractFilter(nil, "result_filter")
	require.NoError(t, err)
	assert.Equal(t, "", filter)
	assert.Nil(t, clean)
}

// --- Grep Combined Flags Tests ---

func TestEngine_GrepCombinedFlags_Ei(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "Error line\nINFO line\nERROR line"
	result, err := engine.Apply(context.Background(), input, "grep -Ei error")
	require.NoError(t, err)
	assert.Equal(t, "Error line\nERROR line", result.Content)
}

func TestEngine_GrepCombinedFlags_iv(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "DEBUG msg\nerror msg\nINFO msg"
	result, err := engine.Apply(context.Background(), input, "grep -iv error")
	require.NoError(t, err)
	assert.Equal(t, "DEBUG msg\nINFO msg", result.Content)
}

func TestEngine_GrepFlag_E_Alone(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "foo123\nbar456\nbaz"
	result, err := engine.Apply(context.Background(), input, "grep -E '[0-9]+'")
	require.NoError(t, err)
	assert.Equal(t, "foo123\nbar456", result.Content)
}

func TestEngine_GrepUnsupportedFlag(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep -z pattern")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported flag")
}

func TestEngine_GrepNoMatch(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "line1\nline2\nline3"
	result, err := engine.Apply(context.Background(), input, "grep NOTFOUND")
	require.NoError(t, err)
	assert.Equal(t, "", result.Content)
}

func TestEngine_GrepEmptyInput(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	result, err := engine.Apply(context.Background(), "", "grep foo")
	require.NoError(t, err)
	assert.Equal(t, "", result.Content)
}

func TestEngine_GrepRegexSpecialChars(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "price: $100\nprice: $200\nno price"
	result, err := engine.Apply(context.Background(), input, `grep '\$[0-9]+'`)
	require.NoError(t, err)
	assert.Equal(t, "price: $100\nprice: $200", result.Content)
}

func TestEngine_GrepPatternTooLong(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	longPattern := strings.Repeat("a", 1025)
	_, err := engine.Apply(context.Background(), "data", "grep "+longPattern)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

// --- Head/Tail Edge Cases ---

func TestEngine_HeadDefault(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// 15 lines, head without -N should default to 10
	lines := strings.Join(makeLines(15), "\n")
	result, err := engine.Apply(context.Background(), lines, "head")
	require.NoError(t, err)
	assert.Equal(t, strings.Join(makeLines(10), "\n"), result.Content)
}

func TestEngine_HeadDashN(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	lines := "a\nb\nc\nd\ne"
	result, err := engine.Apply(context.Background(), lines, "head -n 3")
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc", result.Content)
}

func TestEngine_HeadFewerLines(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "only\ntwo"
	result, err := engine.Apply(context.Background(), input, "head -100")
	require.NoError(t, err)
	assert.Equal(t, "only\ntwo", result.Content)
}

func TestEngine_TailDefault(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	lines := strings.Join(makeLines(15), "\n")
	result, err := engine.Apply(context.Background(), lines, "tail")
	require.NoError(t, err)
	// Default tail 10: lines 6-15
	expected := strings.Join(makeLines(15)[5:], "\n")
	assert.Equal(t, expected, result.Content)
}

func TestEngine_TailDashN(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	lines := "a\nb\nc\nd\ne"
	result, err := engine.Apply(context.Background(), lines, "tail -n 2")
	require.NoError(t, err)
	assert.Equal(t, "d\ne", result.Content)
}

func TestEngine_HeadEmptyInput(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	result, err := engine.Apply(context.Background(), "", "head -5")
	require.NoError(t, err)
	assert.Equal(t, "", result.Content)
}

// --- JQ Tests ---

func TestEngine_JQRawOutput(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	input := `{"name":"hello world"}`
	result, err := engine.Apply(context.Background(), input, "jq -r '.name'")
	require.NoError(t, err)
	// -r: no quotes around string output
	assert.Equal(t, "hello world", result.Content)
}

func TestEngine_JQRawOutputLongFlag(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	input := `{"msg":"test"}`
	result, err := engine.Apply(context.Background(), input, "jq --raw-output '.msg'")
	require.NoError(t, err)
	assert.Equal(t, "test", result.Content)
}

func TestEngine_JQRawOutputNonString(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	// -r on non-string values should still JSON-encode them.
	input := `{"count":42}`
	result, err := engine.Apply(context.Background(), input, "jq -r '.count'")
	require.NoError(t, err)
	assert.Equal(t, "42", result.Content)
}

func TestEngine_JQPipeline_WebFetchScenario(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	// Simulates real web_fetch JSON result → jq extract → grep headings.
	input := `{"results":[{"retrieved_url":"https://example.com","content":"# Title\nSome text\n## Section One\nMore text\n## Section Two\nEnd"}]}`
	result, err := engine.Apply(context.Background(), input, "jq -r '.results[0].content' | grep '^#'")
	require.NoError(t, err)
	assert.Equal(t, "# Title\n## Section One\n## Section Two", result.Content)
}

func TestEngine_JQInvalidInput(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "not json", "jq '.'")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestEngine_JQExprTooLong(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	longExpr := "." + strings.Repeat("a", 2048)
	_, err := engine.Apply(context.Background(), "{}", "jq '"+longExpr+"'")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

func TestEngine_JQNoExpression(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "{}", "jq")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expression required")
}

// --- Security / Rejection Tests ---

func TestEngine_RejectSemicolon(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep foo; rm -rf /")
	assert.Error(t, err)
}

func TestEngine_RejectAndAnd(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep foo && cat /etc/passwd")
	assert.Error(t, err)
}

func TestEngine_RejectCommandSubstitution(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep $(whoami)")
	assert.Error(t, err)
}

func TestEngine_RejectBacktickSubstitution(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep `whoami`")
	assert.Error(t, err)
}

func TestEngine_RejectVariableAssignment(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "X=1 grep foo")
	assert.Error(t, err)
}

func TestEngine_RejectMultipleStatements(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep foo\ngrep bar")
	// Shell parser may interpret this differently but should reject.
	assert.Error(t, err)
}

func TestEngine_RejectPipelineTooLong(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// Build 11-stage pipeline (max is 10).
	stages := make([]string, 11)
	for i := range stages {
		stages[i] = "head -1"
	}
	expr := strings.Join(stages, " | ")
	_, err := engine.Apply(context.Background(), "data", expr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

// --- MaxInput Tests ---

func TestEngine_MaxInput(t *testing.T) {
	cfg := defaultConfig()
	cfg.maxInput = 20
	engine := NewEngine(cfg)

	// Input is 26 chars, maxInput=20 truncates before filtering.
	input := "abcdefghij\nklmnopqrst\nuvwxyz"
	result, err := engine.Apply(context.Background(), input, "head -5")
	require.NoError(t, err)
	// After truncation to 20 bytes: "abcdefghij\nklmnopqrs" (partial second line)
	assert.NotEmpty(t, result.Filter)
	assert.LessOrEqual(t, len(result.Content), 20)
}

// --- resultToString Tests ---

func TestResultToString_String(t *testing.T) {
	assert.Equal(t, "hello", resultToString("hello"))
}

func TestResultToString_Bytes(t *testing.T) {
	assert.Equal(t, "bytes", resultToString([]byte("bytes")))
}

func TestResultToString_Nil(t *testing.T) {
	assert.Equal(t, "", resultToString(nil))
}

func TestResultToString_Struct(t *testing.T) {
	type resp struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	s := resultToString(resp{Name: "test", Count: 5})
	// Should be pretty-printed JSON.
	assert.Contains(t, s, "\"name\": \"test\"")
	assert.Contains(t, s, "\"count\": 5")
	assert.Contains(t, s, "\n") // Multi-line.
}

// --- BeforeTool Edge Cases ---

func TestToolPipe_BeforeTool_SkipsNonAllowedTool(t *testing.T) {
	tp := New(WithToolNames("allowed_tool"))

	rawArgs, _ := json.Marshal(map[string]string{
		"query":         "test",
		"result_filter": "grep foo",
	})
	args := &tool.BeforeToolArgs{
		ToolName:  "other_tool",
		Arguments: rawArgs,
	}

	result, err := tp.beforeTool(ctxWithAugmented("test_tool"), args)
	require.NoError(t, err)
	assert.Nil(t, result) // Should not process.
}

func TestToolPipe_BeforeTool_InvalidFilterSkips(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	rawArgs, _ := json.Marshal(map[string]string{
		"query":         "test",
		"result_filter": "rm -rf /", // Not in allowed ops.
	})
	args := &tool.BeforeToolArgs{
		ToolName:  "test_tool",
		Arguments: rawArgs,
	}

	// Should strip filter field (so it doesn't leak to tool) and store
	// parse error state for AfterTool to report.
	result, err := tp.beforeTool(ctxWithAugmented("test_tool"), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Filter field should be stripped from args.
	require.NotNil(t, result.ModifiedArguments)
	var cleaned map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &cleaned))
	assert.NotContains(t, cleaned, "result_filter")
	assert.Equal(t, "test", cleaned["query"])
}

func TestToolPipe_BeforeTool_EmptyFilter(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	rawArgs, _ := json.Marshal(map[string]string{
		"query":         "test",
		"result_filter": "",
	})
	args := &tool.BeforeToolArgs{
		ToolName:  "test_tool",
		Arguments: rawArgs,
	}

	result, err := tp.beforeTool(ctxWithAugmented("test_tool"), args)
	require.NoError(t, err)
	// Empty filter still strips the field to prevent leaking to strict-schema tools.
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)
	var cleaned map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &cleaned))
	assert.NotContains(t, cleaned, "result_filter")
}

// --- AfterTool Edge Cases ---

func TestToolPipe_AfterTool_FilterError(t *testing.T) {
	tp := New(
		WithToolNames("test_tool"),
		WithAllowedOps(OpGrep, OpHead, OpTail, OpJQ),
	)

	// Context carries a jq pipeline, but result is not valid JSON.
	pipeline, _ := tp.engine.parse("jq '.'")
	ctx := context.WithValue(context.Background(), filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: "jq '.'",
	})

	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "not json at all",
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should return error annotation, not fail the tool call.
	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.NotEmpty(t, fr.Filter)
	assert.NotEmpty(t, fr.Error)
	assert.Equal(t, "not json at all", fr.Content)
}

func TestToolPipe_AfterTool_StructResult(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	pipeline, _ := tp.engine.parse("grep hello")
	ctx := context.WithValue(context.Background(), filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: "grep hello",
	})

	// Struct result gets JSON-serialized then grepped.
	type resp struct {
		Items []string `json:"items"`
	}
	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   resp{Items: []string{"hello world", "goodbye"}},
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.NotEmpty(t, fr.Filter)
	assert.Contains(t, fr.Content, "hello")
}

// --- End-to-End Callback Flow ---

func TestToolPipe_EndToEnd(t *testing.T) {
	tp := New(
		WithToolNames("search"),
		WithAllowedOps(OpGrep, OpHead, OpTail, OpJQ),
	)

	// 1. BeforeModel augments schema and returns context with augmented set.
	searchDecl := &tool.Declaration{
		Name:        "search",
		InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"q": {Type: "string"}}},
	}
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"search": &mockTool{decl: searchDecl},
		},
	}
	bmResult, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.NotNil(t, req.Tools["search"].Declaration().InputSchema.Properties["result_filter"])

	// Use the context from BeforeModel (carries augmented set).
	bmCtx := context.Background()
	if bmResult != nil && bmResult.Context != nil {
		bmCtx = bmResult.Context
	}

	// 2. BeforeTool extracts filter.
	rawArgs, _ := json.Marshal(map[string]string{"q": "test", "result_filter": "grep important | head -3"})
	btResult, err := tp.beforeTool(bmCtx, &tool.BeforeToolArgs{
		ToolName:  "search",
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, btResult)

	// 3. AfterTool applies filter.
	atResult, err := tp.afterTool(btResult.Context, &tool.AfterToolArgs{
		ToolName: "search",
		Result:   "important result 1\nnoise\nimportant result 2\nnoise\nimportant result 3\nimportant result 4",
	})
	require.NoError(t, err)
	require.NotNil(t, atResult)

	fr, ok := atResult.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.NotEmpty(t, fr.Filter)
	assert.Equal(t, "important result 1\nimportant result 2\nimportant result 3", fr.Content)
}

// --- Helper ---

func makeLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i+1)
	}
	return lines
}

// --- Regression Tests ---

// TestRegression_NativeResultFilterNotStripped verifies that a tool which
// natively has a "result_filter" field in its schema is NOT augmented and
// NOT stripped by BeforeTool, even if it's in the WithToolNames allowlist.
func TestRegression_NativeResultFilterNotStripped(t *testing.T) {
	tp := New(WithToolNames("my_tool"))

	// Tool natively defines result_filter in its schema.
	nativeTool := &mockTool{
		decl: &tool.Declaration{
			Name: "my_tool",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query":         {Type: "string"},
					"result_filter": {Type: "string", Description: "native field"},
				},
			},
		},
	}

	// 1. BeforeModel should NOT augment this tool (field collision).
	req := &model.Request{
		Tools: map[string]tool.Tool{"my_tool": nativeTool},
	}
	bmResult, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Verify schema was NOT changed (still has "native field" description).
	decl := req.Tools["my_tool"].Declaration()
	assert.Equal(t, "native field", decl.InputSchema.Properties["result_filter"].Description)

	// 2. BeforeTool should NOT strip the native result_filter.
	// Context from BeforeModel should NOT include "my_tool" in augmented set.
	bmCtx := context.Background()
	if bmResult != nil && bmResult.Context != nil {
		bmCtx = bmResult.Context
	}

	rawArgs, _ := json.Marshal(map[string]string{
		"query":         "test",
		"result_filter": "native value that must not be stripped",
	})
	btResult, err := tp.beforeTool(bmCtx, &tool.BeforeToolArgs{
		ToolName:  "my_tool",
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	// Should return nil — tool not in augmented set, so nothing happens.
	assert.Nil(t, btResult)
}

// TestRegression_WithToolScope_EndToEnd verifies that tools matched by
// WithToolScope (not WithToolNames) are correctly augmented in BeforeModel
// AND correctly processed in BeforeTool/AfterTool via context-passed set.
func TestRegression_WithToolScope_EndToEnd(t *testing.T) {
	tp := New(
		WithToolScope(func(t tool.Tool) bool {
			return strings.HasPrefix(t.Declaration().Name, "mcp_")
		}),
		WithAllowedOps(OpGrep, OpHead),
	)

	// 1. BeforeModel augments the scope-matched tool.
	mcpTool := &mockTool{
		decl: &tool.Declaration{
			Name:        "mcp_search",
			InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"q": {Type: "string"}}},
		},
	}
	req := &model.Request{
		Tools: map[string]tool.Tool{"mcp_search": mcpTool},
	}
	bmResult, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Verify schema was augmented.
	assert.NotNil(t, req.Tools["mcp_search"].Declaration().InputSchema.Properties["result_filter"])

	// Context must carry augmented set.
	require.NotNil(t, bmResult)
	require.NotNil(t, bmResult.Context)

	// 2. BeforeTool correctly strips filter using context from BeforeModel.
	rawArgs, _ := json.Marshal(map[string]string{
		"q":             "test query",
		"result_filter": "grep important | head -3",
	})
	btResult, err := tp.beforeTool(bmResult.Context, &tool.BeforeToolArgs{
		ToolName:  "mcp_search",
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, btResult)
	require.NotNil(t, btResult.ModifiedArguments)

	// Verify filter was stripped from args.
	var cleaned map[string]any
	require.NoError(t, json.Unmarshal(btResult.ModifiedArguments, &cleaned))
	assert.Equal(t, "test query", cleaned["q"])
	assert.NotContains(t, cleaned, "result_filter")

	// 3. AfterTool applies the filter.
	atResult, err := tp.afterTool(btResult.Context, &tool.AfterToolArgs{
		ToolName: "mcp_search",
		Result:   "important line 1\nnoise\nimportant line 2\nnoise\nimportant line 3\nimportant line 4",
	})
	require.NoError(t, err)
	require.NotNil(t, atResult)

	fr, ok := atResult.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.Equal(t, "important line 1\nimportant line 2\nimportant line 3", fr.Content)
}
