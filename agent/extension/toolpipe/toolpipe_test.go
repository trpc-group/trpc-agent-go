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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
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
	assert.LessOrEqual(t, len(result.Content), 10)
}

func TestEngine_MaxOutput_HeadTailWindow(t *testing.T) {
	cfg := defaultConfig()
	cfg.maxOutput = 200 // big enough to exercise head+tail
	engine := NewEngine(cfg)

	// 500 chars, exceeds 200 maxOutput.
	input := strings.Repeat("x", 500)
	result, err := engine.Apply(context.Background(), input, "head -1")
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Equal(t, 500, result.TotalBytes)
	assert.Contains(t, result.Content, "bytes omitted")
	assert.LessOrEqual(t, len(result.Content), 200)
}

func TestEngine_MaxInput_Signals(t *testing.T) {
	cfg := defaultConfig()
	cfg.maxInput = 50
	engine := NewEngine(cfg)

	// 100 chars, exceeds maxInput=50.
	input := strings.Repeat("a", 100)
	result, err := engine.Apply(context.Background(), input, "head -5")
	require.NoError(t, err)
	assert.True(t, result.InputTruncated)
	assert.Equal(t, 100, result.InputTotalBytes)
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

	// Should remain unchanged — same pointer.
	assert.Same(t, other, req.Tools["other_tool"])
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

func TestToolPipe_AfterTool_AugmentedNoFilter_SmallResult(t *testing.T) {
	tp := New(WithToolNames("test_tool"), WithMaxOutputBytes(1024))

	// Augmented tool (in context set), no filter, small result → no wrapping.
	ctx := ctxWithAugmented("test_tool")
	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "small output",
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	assert.Nil(t, result) // Small enough, no truncation needed.
}

func TestToolPipe_AfterTool_AugmentedNoFilter_LargeResult(t *testing.T) {
	tp := New(WithToolNames("test_tool"), WithMaxOutputBytes(100))

	// Augmented tool (in context set), no filter, large result → head+tail windowed.
	ctx := ctxWithAugmented("test_tool")
	largeContent := strings.Repeat("line of text\n", 50) // ~650 bytes > 100
	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   largeContent,
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.True(t, fr.Truncated)
	assert.Equal(t, len(largeContent), fr.TotalBytes)
	assert.Contains(t, fr.Content, "bytes omitted")
	// Verify head+tail: content should contain the beginning and end.
	assert.True(t, strings.HasPrefix(fr.Content, "line of text"))
	assert.True(t, strings.HasSuffix(fr.Content, "line of text\n"))
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

func TestEngine_GrepRejectOutputModifiers(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// Flags that change output semantics should be rejected (fail-closed).
	for _, flag := range []string{"-n", "-c", "-l", "-o", "-P", "-F"} {
		_, err := engine.Apply(context.Background(), "data", "grep "+flag+" pattern")
		assert.Error(t, err, "flag %s should be rejected", flag)
		assert.Contains(t, err.Error(), "unsupported flag", "flag %s", flag)
	}
}

func TestEngine_GrepDoubleDash(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// grep -- -pattern should search for literal "-pattern".
	input := "-pattern matched\nother line\n-pattern again"
	result, err := engine.Apply(context.Background(), input, "grep -- -pattern")
	require.NoError(t, err)
	assert.Equal(t, "-pattern matched\n-pattern again", result.Content)
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

func TestEngine_JQIterationTruncation(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	// Generate a range expression that produces more than iterLimit results.
	// [range(20000)] generates 20000 numbers.
	input := `null`
	result, err := engine.Apply(context.Background(), input, "jq '[range(20000)] | .[]'")
	require.NoError(t, err)
	assert.Contains(t, result.Content, "truncated: iteration limit 10000 reached")
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

func TestEngine_RejectBackground(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "grep foo &")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "background")
}

func TestEngine_RejectNegation(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "! grep foo")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "negation")
}

func TestWithAllowedOps_IgnoresUnknown(t *testing.T) {
	tp := New(
		WithToolNames("test_tool"),
		WithAllowedOps(OpGrep, OpType("sed"), OpType("awk")),
	)
	// Only OpGrep should be in allowedOps.
	assert.True(t, tp.cfg.allowedOps[OpGrep])
	assert.False(t, tp.cfg.allowedOps[OpType("sed")])
	assert.False(t, tp.cfg.allowedOps[OpType("awk")])
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
	// Context should carry parse error state.
	require.NotNil(t, result.Context)
	state, ok := result.Context.Value(filterContextKey{}).(*filterState)
	require.True(t, ok)
	assert.NotEmpty(t, state.parseError)
	assert.Equal(t, "rm -rf /", state.filterExpr)
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

// --- Wrapper Type Tests ---

// mockStreamableTool is a tool that implements StreamableTool.
type mockStreamableTool struct {
	mockTool
}

func (m *mockStreamableTool) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	return nil, fmt.Errorf("not implemented in test")
}

func TestWrapper_NonStreamable_DoesNotSatisfyStreamableTool(t *testing.T) {
	// A normal (non-streaming) tool, when wrapped, must NOT satisfy tool.StreamableTool.
	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "normal_tool",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	wrapped := newDeclaredCallableTool(inner, inner.decl)
	_, isStreamable := wrapped.(tool.StreamableTool)
	assert.False(t, isStreamable, "non-streaming tool wrapper must not satisfy StreamableTool")
}

func TestWrapper_Streamable_SatisfiesStreamableTool(t *testing.T) {
	// A streaming tool, when wrapped, must satisfy tool.StreamableTool.
	inner := &mockStreamableTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "stream_tool",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	wrapped := newDeclaredCallableTool(inner, inner.decl)
	st, isStreamable := wrapped.(tool.StreamableTool)
	assert.True(t, isStreamable, "streaming tool wrapper must satisfy StreamableTool")
	assert.NotNil(t, st)
}

// mockNamedToolLike simulates NamedTool behavior: implements StreamableTool
// and Original() but wraps a non-streamable tool.
type mockNamedToolLike struct {
	original tool.Tool
	decl     *tool.Declaration
}

func (m *mockNamedToolLike) Declaration() *tool.Declaration { return m.decl }
func (m *mockNamedToolLike) Call(ctx context.Context, args []byte) (any, error) {
	if callable, ok := m.original.(tool.CallableTool); ok {
		return callable.Call(ctx, args)
	}
	return nil, fmt.Errorf("not callable")
}
func (m *mockNamedToolLike) StreamableCall(ctx context.Context, args []byte) (*tool.StreamReader, error) {
	// NamedTool always implements this, delegating to original.
	if st, ok := m.original.(tool.StreamableTool); ok {
		return st.StreamableCall(ctx, args)
	}
	return nil, fmt.Errorf("tool is not streamable")
}
func (m *mockNamedToolLike) Original() tool.Tool { return m.original }

func TestWrapper_NamedToolWrappingNonStreamable_DoesNotSatisfyStreamableTool(t *testing.T) {
	// This is the critical NamedTool/MCP regression test.
	// NamedTool always implements StreamableTool on its wrapper,
	// but the Original() tool is NOT streamable. After toolpipe
	// wrapping, the result must NOT satisfy tool.StreamableTool.
	nonStreamableOriginal := &mockTool{
		decl: &tool.Declaration{
			Name:        "mcp_query",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	namedTool := &mockNamedToolLike{
		original: nonStreamableOriginal,
		decl:     nonStreamableOriginal.decl,
	}

	// Verify precondition: namedTool itself satisfies StreamableTool.
	_, namedIsStreamable := tool.Tool(namedTool).(tool.StreamableTool)
	require.True(t, namedIsStreamable, "precondition: NamedTool-like wrapper satisfies StreamableTool")

	// After toolpipe wrapping, it must NOT satisfy StreamableTool.
	wrapped := newDeclaredCallableTool(namedTool, namedTool.decl)
	_, wrappedIsStreamable := wrapped.(tool.StreamableTool)
	assert.False(t, wrappedIsStreamable,
		"NamedTool wrapping non-streamable tool must NOT satisfy StreamableTool after toolpipe wrap")
}

func TestWrapper_NamedToolWrappingStreamable_SatisfiesStreamableTool(t *testing.T) {
	// When NamedTool wraps a truly streamable tool, toolpipe should preserve that.
	streamableOriginal := &mockStreamableTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "mcp_stream_query",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	namedTool := &mockNamedToolLike{
		original: streamableOriginal,
		decl:     streamableOriginal.decl,
	}

	wrapped := newDeclaredCallableTool(namedTool, namedTool.decl)
	_, wrappedIsStreamable := wrapped.(tool.StreamableTool)
	assert.True(t, wrappedIsStreamable,
		"NamedTool wrapping streamable tool must satisfy StreamableTool after toolpipe wrap")
}

// --- Schema Augmentation Guard Tests ---

func TestToolPipe_SkipsNonObjectSchema(t *testing.T) {
	tp := New(WithToolNames("array_tool"))

	arrayTool := &mockTool{
		decl: &tool.Declaration{
			Name: "array_tool",
			InputSchema: &tool.Schema{
				Type: "array",
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"array_tool": arrayTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Tool with array schema should NOT be augmented.
	assert.Same(t, arrayTool, req.Tools["array_tool"])
}

// mockFrameworkTool simulates an AgentTool-like framework tool
// that implements StreamInner().
type mockFrameworkTool struct {
	mockTool
}

func (m *mockFrameworkTool) StreamInner() bool { return true }
func (m *mockFrameworkTool) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestToolPipe_SkipsFrameworkTool(t *testing.T) {
	tp := New(WithToolNames("agent_tool"))

	agentTool := &mockFrameworkTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "agent_tool",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"agent_tool": agentTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Framework tool should NOT be augmented even if in allowlist.
	assert.Same(t, agentTool, req.Tools["agent_tool"])
}

// mockLongRunningTool simulates a tool that returns LongRunning() = true.
type mockLongRunningTool struct {
	mockTool
}

func (m *mockLongRunningTool) LongRunning() bool { return true }

// mockNormalFunctionTool simulates a function tool that has LongRunning() = false (default).
type mockNormalFunctionTool struct {
	mockTool
}

func (m *mockNormalFunctionTool) LongRunning() bool { return false }

func TestToolPipe_SkipsLongRunningTool(t *testing.T) {
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))

	longTool := &mockLongRunningTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "long_tool",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"long_tool": longTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// LongRunning=true tool should be skipped.
	assert.Same(t, longTool, req.Tools["long_tool"])
}

func TestToolPipe_DoesNotSkipNormalFunctionTool(t *testing.T) {
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))

	// LongRunning()=false should NOT cause skip.
	normalTool := &mockNormalFunctionTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "my_function",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"my_function": normalTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Normal function tool (LongRunning=false) should be augmented.
	assert.NotSame(t, normalTool, req.Tools["my_function"])
	assert.NotNil(t, req.Tools["my_function"].Declaration().InputSchema.Properties["result_filter"])
}

// mockStateDeltaTool simulates a tool implementing StateDelta.
type mockStateDeltaTool struct {
	mockTool
}

func (m *mockStateDeltaTool) StateDelta(toolCallID string, args []byte, result []byte) map[string][]byte {
	return nil
}

func TestToolPipe_SkipsStateDeltaTool(t *testing.T) {
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))

	stateTool := &mockStateDeltaTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "todo_tool",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"todo_tool": stateTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// StateDelta tool should be skipped.
	assert.Same(t, stateTool, req.Tools["todo_tool"])
}

// mockStateDeltaForInvocationTool simulates a tool implementing only
// StateDeltaForInvocation (like todo.go in the real codebase).
type mockStateDeltaForInvocationTool struct {
	mockTool
}

func (m *mockStateDeltaForInvocationTool) StateDeltaForInvocation(
	_ *agent.Invocation, _ string, _ []byte, _ []byte,
) map[string][]byte {
	return nil
}

func TestToolPipe_SkipsStateDeltaForInvocationTool(t *testing.T) {
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))

	stateTool := &mockStateDeltaForInvocationTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "todo_manage",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"todo_manage": stateTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// StateDeltaForInvocation tool should be skipped.
	assert.Same(t, stateTool, req.Tools["todo_manage"])
}

func TestToolPipe_SkipsFrameworkToolBehindNamedTool(t *testing.T) {
	tp := New(WithToolNames("mcp_agent"))

	// NamedTool wrapping a framework tool (has StreamInner on original).
	frameworkOriginal := &mockFrameworkTool{
		mockTool: mockTool{
			decl: &tool.Declaration{
				Name:        "agent",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}
	namedTool := &mockNamedToolLike{
		original: frameworkOriginal,
		decl: &tool.Declaration{
			Name:        "mcp_agent",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"mcp_agent": namedTool},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Should NOT be augmented — original behind NamedTool is a framework tool.
	assert.Same(t, namedTool, req.Tools["mcp_agent"])
}

func TestToolPipe_SkipsTransferAndAwaitTools(t *testing.T) {
	// Even with broad scope, transfer_to_agent and await_user_reply are skipped.
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))

	transferTool := &mockTool{
		decl: &tool.Declaration{
			Name:        "transfer_to_agent",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}
	awaitTool := &mockTool{
		decl: &tool.Declaration{
			Name:        "await_user_reply",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}
	dataTool := &mockTool{
		decl: &tool.Declaration{
			Name:        "web_fetch",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{
			"transfer_to_agent": transferTool,
			"await_user_reply":  awaitTool,
			"web_fetch":         dataTool,
		},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Framework tools skipped.
	assert.Same(t, transferTool, req.Tools["transfer_to_agent"])
	assert.Same(t, awaitTool, req.Tools["await_user_reply"])
	// Data tool augmented.
	assert.NotSame(t, dataTool, req.Tools["web_fetch"])
	assert.NotNil(t, req.Tools["web_fetch"].Declaration().InputSchema.Properties["result_filter"])
}

// --- suffixUTF8 / windowOutput Tests ---

func TestSuffixUTF8_Basic(t *testing.T) {
	assert.Equal(t, "world", suffixUTF8("hello world", 5))
	assert.Equal(t, "hello world", suffixUTF8("hello world", 100))
	assert.Equal(t, "d", suffixUTF8("hello world", 1))
}

func TestSuffixUTF8_MultiByte(t *testing.T) {
	// "你好世界" = 12 bytes (3 per char). Request 7 bytes → can fit 2 chars (6 bytes).
	s := "你好世界"
	result := suffixUTF8(s, 7)
	// Should skip partial rune, start at a valid boundary.
	assert.True(t, len(result) <= 7)
	// Must be valid UTF-8 and end with the original ending.
	assert.True(t, strings.HasSuffix(s, result))
}

func TestWindowOutput_PreservesTail(t *testing.T) {
	// Verify the windowed output ends with the actual end of the input.
	input := strings.Repeat("A", 200) + "THE_END"
	result := windowOutput(input, 120)
	assert.True(t, strings.HasSuffix(result, "THE_END"),
		"windowed output must preserve the end of original content")
	assert.Contains(t, result, "bytes omitted")
	assert.LessOrEqual(t, len(result), 120)
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

// ========================================================================
// Coverage supplement tests
// ========================================================================

// --- Option Coverage ---

func TestWithFilterField(t *testing.T) {
	tp := New(WithToolNames("t"), WithFilterField("custom_filter"))
	assert.Equal(t, "custom_filter", tp.cfg.filterField)

	// Empty string should not change the field.
	tp2 := New(WithToolNames("t"), WithFilterField(""))
	assert.Equal(t, "result_filter", tp2.cfg.filterField)
}

func TestWithMaxInputBytes(t *testing.T) {
	tp := New(WithToolNames("t"), WithMaxInputBytes(512))
	assert.Equal(t, int64(512), tp.cfg.maxInput)

	// Zero/negative should not change default.
	tp2 := New(WithToolNames("t"), WithMaxInputBytes(0))
	assert.Equal(t, int64(2<<20), tp2.cfg.maxInput)
}

func TestWithPrompt(t *testing.T) {
	// Custom prompt.
	tp := New(WithToolNames("t"), WithPrompt("custom"))
	require.NotNil(t, tp.cfg.customPrompt)
	assert.Equal(t, "custom", *tp.cfg.customPrompt)

	// Disable prompt.
	tp2 := New(WithToolNames("t"), WithPrompt(""))
	require.NotNil(t, tp2.cfg.customPrompt)
	assert.Equal(t, "", *tp2.cfg.customPrompt)
}

// --- Name / Register / Prompt() ---

func TestToolPipe_Name(t *testing.T) {
	tp := New(WithToolNames("t"))
	assert.Equal(t, "toolpipe", tp.Name())
}

func TestToolPipe_Register(t *testing.T) {
	tp := New(WithToolNames("t"))

	// Use extension.Collect which calls Register internally.
	contrib, err := extension.Collect([]extension.Extension{tp})
	require.NoError(t, err)
	require.NotNil(t, contrib)
	// Verify callbacks were registered (non-empty contributions).
	assert.False(t, contrib.IsEmpty())
}

func TestToolPipe_Prompt_WithNames(t *testing.T) {
	tp := New(WithToolNames("web_fetch", "search"))
	prompt := tp.Prompt()
	assert.Contains(t, prompt, "web_fetch")
	assert.Contains(t, prompt, "search")
	assert.Contains(t, prompt, "result_filter")
}

func TestToolPipe_Prompt_EmptyOps(t *testing.T) {
	tp := New(WithToolNames("t"), WithAllowedOps())
	assert.Equal(t, "", tp.Prompt())
}

func TestToolPipe_Prompt_WithScope(t *testing.T) {
	tp := New(WithToolScope(func(_ tool.Tool) bool { return true }))
	prompt := tp.Prompt()
	assert.Contains(t, prompt, "<tools matching scope>")
}

func TestToolPipe_Prompt_CustomPrompt(t *testing.T) {
	tp := New(WithToolNames("t"), WithPrompt("my custom"))
	// resolvePrompt should return custom.
	names := []string{"t"}
	assert.Equal(t, "my custom", tp.resolvePrompt(names))
}

func TestToolPipe_Prompt_DisabledPrompt(t *testing.T) {
	tp := New(WithToolNames("t"), WithPrompt(""))
	names := []string{"t"}
	assert.Equal(t, "", tp.resolvePrompt(names))
}

// --- collectPipelineCalls (deep pipeline) ---

func TestEngine_DeepPipeline(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	// 5-stage pipeline exercises collectPipelineCalls recursion.
	input := `{"a":"ERROR one","b":"info","c":"ERROR two","d":"ERROR three","e":"other"}`
	result, err := engine.Apply(context.Background(), input, "jq -r '.a, .b, .c, .d, .e' | grep ERROR | head -2 | tail -1")
	require.NoError(t, err)
	assert.Equal(t, "ERROR two", result.Content)
}

// --- wordToString (double-quoted string) ---

func TestEngine_DoubleQuotedArg(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "hello world\ngoodbye world"
	result, err := engine.Apply(context.Background(), input, `grep "hello world"`)
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Content)
}

// --- windowOutput edge cases ---

func TestWindowOutput_SmallBudget(t *testing.T) {
	// Very small maxBytes → prefix truncate fallback.
	input := strings.Repeat("x", 200)
	result := windowOutput(input, 20)
	assert.LessOrEqual(t, len(result), 20)
}

func TestWindowOutput_ExactFit(t *testing.T) {
	// Content exactly at budget → no windowing needed (but function is only
	// called when content > maxBytes, so test slightly over).
	input := strings.Repeat("A", 101)
	result := windowOutput(input, 100)
	assert.LessOrEqual(t, len(result), 100)
	assert.Contains(t, result, "bytes omitted")
}

// --- parseHeadOp / parseTailOp bare number ---

func TestEngine_HeadBareNumber(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "a\nb\nc\nd\ne"
	result, err := engine.Apply(context.Background(), input, "head 3")
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc", result.Content)
}

func TestEngine_TailBareNumber(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	input := "a\nb\nc\nd\ne"
	result, err := engine.Apply(context.Background(), input, "tail 2")
	require.NoError(t, err)
	assert.Equal(t, "d\ne", result.Content)
}

func TestEngine_HeadInvalidFlag(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "head -abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestEngine_TailInvalidFlag(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	_, err := engine.Apply(context.Background(), "data", "tail -xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

// --- afterTool: tool error passthrough ---

func TestToolPipe_AfterTool_ToolError(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	ctx := ctxWithAugmented("test_tool")
	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "some result",
		Error:    fmt.Errorf("tool failed"),
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	assert.Nil(t, result) // Tool error → skip filtering entirely.
}

// --- afterTool: filter produces empty output from non-empty result ---

func TestToolPipe_AfterTool_FilterEmpty(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	pipeline, err := tp.engine.parse("grep NOTFOUND")
	require.NoError(t, err)
	ctx := context.WithValue(ctxWithAugmented("test_tool"), filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: "grep NOTFOUND",
	})

	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   "this line exists\nbut no match",
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.Equal(t, "", fr.Content)
	assert.NotEmpty(t, fr.EmptyReason)
	assert.NotEmpty(t, fr.OriginalPreview)
}

// --- injectSystemPrompt: no existing system message ---

func TestToolPipe_InjectSystemPrompt_NoExisting(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "test_tool",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"test_tool": inner},
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	}

	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// System message should be prepended.
	require.True(t, len(req.Messages) >= 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "toolpipe")
}

// --- injectSystemPrompt: existing system message ---

func TestToolPipe_InjectSystemPrompt_AppendExisting(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "test_tool",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"test_tool": inner},
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
			model.NewUserMessage("hello"),
		},
	}

	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Should append to existing system message, not create new.
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "You are helpful.")
	assert.Contains(t, req.Messages[0].Content, "toolpipe")
}

// --- promptInjected dedup ---

func TestToolPipe_PromptNotDuplicated(t *testing.T) {
	tp := New(WithToolNames("test_tool"))

	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "test_tool",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"test_tool": inner},
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
		},
	}

	// Call beforeModel twice.
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	_, err = tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Prompt should only appear once (check marker, not the word "toolpipe").
	count := strings.Count(req.Messages[0].Content, toolpipeMarker)
	assert.Equal(t, 1, count, "prompt should not be injected twice")
}

// --- Call / SkipSummarization / StreamableCall delegation ---

func TestDeclaredCallableTool_Call(t *testing.T) {
	inner := &mockTool{
		decl:   &tool.Declaration{Name: "t", InputSchema: &tool.Schema{Type: "object"}},
		result: "hello",
	}
	wrapped := newDeclaredCallableTool(inner, inner.decl)
	result, err := wrapped.Call(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestDeclaredCallableTool_SkipSummarization(t *testing.T) {
	inner := &mockTool{
		decl: &tool.Declaration{Name: "t", InputSchema: &tool.Schema{Type: "object"}},
	}
	wrapped := newDeclaredCallableTool(inner, inner.decl)

	// mockTool doesn't implement SkipSummarization → returns false.
	type skipper interface{ SkipSummarization() bool }
	if s, ok := wrapped.(skipper); ok {
		assert.False(t, s.SkipSummarization())
	}
}

func TestDeclaredStreamableCallableTool_StreamableCall(t *testing.T) {
	inner := &mockStreamableTool{
		mockTool: mockTool{
			decl: &tool.Declaration{Name: "st", InputSchema: &tool.Schema{Type: "object"}},
		},
	}
	wrapped := newDeclaredCallableTool(inner, inner.decl)
	st, ok := wrapped.(tool.StreamableTool)
	require.True(t, ok)

	// StreamableCall delegates to inner (which returns error in our mock).
	_, err := st.StreamableCall(context.Background(), nil)
	assert.Error(t, err)
}

// --- truncateUTF8 / truncateForPreview ---

func TestTruncateUTF8_NoTruncation(t *testing.T) {
	assert.Equal(t, "short", truncateUTF8("short", 100))
}

func TestTruncateUTF8_MultiByte(t *testing.T) {
	s := "你好世界" // 12 bytes
	result := truncateUTF8(s, 7)
	// Should cut at rune boundary: "你好" = 6 bytes (fits in 7).
	assert.Equal(t, "你好", result)
	assert.LessOrEqual(t, len(result), 7)
}

func TestTruncateForPreview_Short(t *testing.T) {
	assert.Equal(t, "hello", truncateForPreview("hello", 100))
}

func TestTruncateForPreview_Long(t *testing.T) {
	s := strings.Repeat("x", 200)
	result := truncateForPreview(s, 50)
	assert.True(t, strings.HasSuffix(result, "...(truncated)"))
	assert.LessOrEqual(t, len(result), 50+len("...(truncated)"))
}

// --- canAugmentSchema ---

func TestCanAugmentSchema_NilDeclaration(t *testing.T) {
	inner := &mockTool{decl: nil}
	assert.False(t, canAugmentSchema(inner))
}

func TestCanAugmentSchema_NilSchema(t *testing.T) {
	inner := &mockTool{decl: &tool.Declaration{Name: "t"}}
	assert.True(t, canAugmentSchema(inner))
}

func TestCanAugmentSchema_StringType(t *testing.T) {
	inner := &mockTool{decl: &tool.Declaration{
		Name:        "t",
		InputSchema: &tool.Schema{Type: "string"},
	}}
	assert.False(t, canAugmentSchema(inner))
}

// --- copySchema edge cases ---

func TestCopySchema_Nil(t *testing.T) {
	assert.Nil(t, copySchema(nil))
}

func TestCopySchema_WithRequired(t *testing.T) {
	orig := &tool.Schema{
		Type:     "object",
		Required: []string{"a", "b"},
		Properties: map[string]*tool.Schema{
			"a": {Type: "string"},
		},
	}
	cp := copySchema(orig)
	// Mutating copy should not affect original.
	cp.Required = append(cp.Required, "c")
	cp.Properties["new"] = &tool.Schema{Type: "number"}
	assert.Len(t, orig.Required, 2)
	assert.NotContains(t, orig.Properties, "new")
}

// --- Execute context cancellation ---

func TestPipeline_Execute_ContextCancelled(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	pipeline, err := engine.parse("grep foo | head -1")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = pipeline.Execute(ctx, "foo\nbar")
	assert.Error(t, err)
}

// --- WithCustomFilterField in BeforeModel/BeforeTool ---

func TestToolPipe_CustomFilterField(t *testing.T) {
	tp := New(WithToolNames("t"), WithFilterField("pipe"))

	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "t",
			InputSchema: &tool.Schema{Type: "object"},
		},
	}
	req := &model.Request{
		Tools: map[string]tool.Tool{"t": inner},
	}
	bmResult, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// Schema should have "pipe" field, not "result_filter".
	decl := req.Tools["t"].Declaration()
	assert.NotNil(t, decl.InputSchema.Properties["pipe"])
	assert.Nil(t, decl.InputSchema.Properties["result_filter"])

	// BeforeTool should extract from "pipe" field.
	rawArgs, _ := json.Marshal(map[string]string{"pipe": "grep hello"})
	btResult, err := tp.beforeTool(bmResult.Context, &tool.BeforeToolArgs{
		ToolName:  "t",
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, btResult)
	require.NotNil(t, btResult.Context)

	state := btResult.Context.Value(filterContextKey{}).(*filterState)
	assert.Equal(t, "grep hello", state.filterExpr)
}

// --- BeforeModel SkipsNotAllowed also checks result_filter not injected ---

func TestToolPipe_BeforeModel_SkipsNotAllowed_NoInjection(t *testing.T) {
	tp := New(WithToolNames("allowed_only"))

	other := &mockTool{
		decl: &tool.Declaration{
			Name:        "other_tool",
			InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"q": {Type: "string"}}},
		},
	}

	req := &model.Request{
		Tools: map[string]tool.Tool{"other_tool": other},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	assert.Same(t, other, req.Tools["other_tool"])
	assert.Nil(t, req.Tools["other_tool"].Declaration().InputSchema.Properties["result_filter"])
}

// --- jq null skip and error formatting ---

func TestEngine_JQNullSkip(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	input := `{"a":null,"b":"hello"}`
	result, err := engine.Apply(context.Background(), input, "jq '.a, .b'")
	require.NoError(t, err)
	// null is skipped, only "hello" appears.
	assert.Equal(t, `"hello"`, result.Content)
}

func TestEngine_JQIterateNull(t *testing.T) {
	cfg := defaultConfig()
	cfg.allowedOps[OpJQ] = true
	engine := NewEngine(cfg)

	input := `{"items":null}`
	_, err := engine.Apply(context.Background(), input, "jq '.items[]'")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "null")
}

// --- augmentDeclaration with nil InputSchema ---

func TestAugmentDeclaration_NilInputSchema(t *testing.T) {
	orig := &tool.Declaration{Name: "t", Description: "test"}
	ops := map[OpType]bool{OpGrep: true}
	aug := augmentDeclaration(orig, "result_filter", ops)

	assert.NotNil(t, aug.InputSchema)
	assert.Equal(t, "object", aug.InputSchema.Type)
	assert.NotNil(t, aug.InputSchema.Properties["result_filter"])
}

func TestAugmentDeclaration_Nil(t *testing.T) {
	ops := map[OpType]bool{OpGrep: true}
	assert.Nil(t, augmentDeclaration(nil, "result_filter", ops))
}

// --- windowOutput post-trim loop ---

func TestWindowOutput_PostTrimLoop(t *testing.T) {
	// Create content where first-pass marker estimate is slightly off,
	// triggering the post-trim loop.
	input := strings.Repeat("中", 200) // 600 bytes (3 bytes per char)
	result := windowOutput(input, 150)
	assert.LessOrEqual(t, len(result), 150)
	assert.Contains(t, result, "bytes omitted")
}

// --- tail.Apply context (75% branch) ---

func TestEngine_TailSingleLine(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// Single line (no split needed), tail -5 returns as-is.
	result, err := engine.Apply(context.Background(), "single", "tail -5")
	require.NoError(t, err)
	assert.Equal(t, "single", result.Content)
}

// --- extractFilterEx: filter field is non-string ---

func TestExtractFilter_NonStringFilter(t *testing.T) {
	args := `{"query":"test","result_filter":123}`
	clean, filter, err := extractFilter([]byte(args), "result_filter")
	require.NoError(t, err)
	assert.Equal(t, "", filter) // Non-string → removed but no expression.
	var m map[string]any
	require.NoError(t, json.Unmarshal(clean, &m))
	assert.NotContains(t, m, "result_filter")
}

// --- extractFilterEx: non-JSON input ---

func TestExtractFilter_NonJSON(t *testing.T) {
	args := []byte("not json at all")
	clean, filter, err := extractFilter(args, "result_filter")
	require.NoError(t, err)
	assert.Equal(t, "", filter)
	assert.Equal(t, args, clean) // Pass through unchanged.
}

// --- SkipSummarization with a tool that implements it ---

type mockSkipSumTool struct {
	mockTool
}

func (m *mockSkipSumTool) SkipSummarization() bool { return true }

func TestDeclaredCallableTool_SkipSummarization_True(t *testing.T) {
	inner := &mockSkipSumTool{
		mockTool: mockTool{
			decl: &tool.Declaration{Name: "t", InputSchema: &tool.Schema{Type: "object"}},
		},
	}
	wrapped := newDeclaredCallableTool(inner, inner.decl)
	type skipper interface{ SkipSummarization() bool }
	s, ok := wrapped.(skipper)
	require.True(t, ok)
	assert.True(t, s.SkipSummarization())
}

// --- shouldWrap: nil tool / nil decl ---

func TestToolPipe_ShouldWrap_Nil(t *testing.T) {
	tp := New(WithToolNames("any"))
	assert.False(t, tp.shouldWrap(nil))
	assert.False(t, tp.shouldWrap(&mockTool{decl: nil}))
}

// --- truncateUnfilteredResult: []byte type fast path ---

func TestToolPipe_AfterTool_AugmentedNoFilter_ByteResult(t *testing.T) {
	tp := New(WithToolNames("test_tool"), WithMaxOutputBytes(100))

	ctx := ctxWithAugmented("test_tool")
	// Small []byte result → no truncation.
	args := &tool.AfterToolArgs{
		ToolName: "test_tool",
		Result:   []byte("small"),
	}
	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	assert.Nil(t, result)
}

// --- Additional coverage for patch threshold ---

func TestToolPipe_BeforeTool_NilArgs(t *testing.T) {
	tp := New(WithToolNames("t"))
	result, err := tp.beforeTool(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestToolPipe_AfterTool_NilArgs(t *testing.T) {
	tp := New(WithToolNames("t"))
	result, err := tp.afterTool(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestToolPipe_BeforeModel_NilArgs(t *testing.T) {
	tp := New(WithToolNames("t"))
	result, err := tp.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, result)

	result, err = tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: nil})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestWindowOutput_PostTrimEmptyTail(t *testing.T) {
	// Trigger the tail="" branch in post-trim by using very tight budget
	// with long content where marker eats most of the budget.
	input := strings.Repeat("x", 500)
	// Budget 80: marker ~38, usable ~42, head ~21, tail ~21.
	// After trim if marker is larger, tail might go to empty.
	result := windowOutput(input, 80)
	assert.LessOrEqual(t, len(result), 80)
	assert.Contains(t, result, "bytes omitted")
}

func TestToolPipe_BeforeTool_ExtractionError(t *testing.T) {
	tp := New(WithToolNames("t"))

	// Simulate args where filter field is present but re-marshal would fail.
	// In practice this tests the extractFilterEx error branch with valid JSON
	// that just has filter field.
	args := &tool.BeforeToolArgs{
		ToolName:  "t",
		Arguments: []byte(`not-json`),
	}
	// Non-JSON args → extractFilterEx passes through, not in augmented set → nil.
	result, err := tp.beforeTool(ctxWithAugmented("t"), args)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestEngine_ParseCoprocess(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// Coprocess syntax (|&) should be rejected.
	// Note: mvdan.cc/sh may not parse |& as coprocess on all versions,
	// but ensure basic & is caught.
	_, err := engine.Apply(context.Background(), "data", "grep foo &")
	assert.Error(t, err)
}

func TestToolPipe_AfterTool_InputTruncatedOnError(t *testing.T) {
	// When filter execution fails AND input would be truncated,
	// the error result should carry input_truncated metadata.
	tp := New(
		WithToolNames("t"),
		WithAllowedOps(OpGrep, OpHead, OpTail, OpJQ),
		WithMaxInputBytes(20), // Very small to trigger truncation.
	)

	pipeline, err := tp.engine.parse("jq '.'")
	require.NoError(t, err)

	ctx := context.WithValue(ctxWithAugmented("t"), filterContextKey{}, &filterState{
		pipeline:   pipeline,
		filterExpr: "jq '.'",
	})

	// Large non-JSON content → will be truncated to 20 bytes → jq will fail.
	bigContent := strings.Repeat("x", 100)
	args := &tool.AfterToolArgs{
		ToolName: "t",
		Result:   bigContent,
	}

	result, err := tp.afterTool(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)

	fr, ok := result.CustomResult.(*ToolResult)
	require.True(t, ok)
	assert.NotEmpty(t, fr.Error)
	assert.True(t, fr.InputTruncated)
	assert.Equal(t, 100, fr.InputTotalBytes)
}

func TestToolPipe_Prompt_NoToolsNoScope(t *testing.T) {
	// No tools, no scope → empty prompt.
	tp := New()
	assert.Equal(t, "", tp.Prompt())
}

func TestEngine_GrepEndOfFlagsNoPattern(t *testing.T) {
	cfg := defaultConfig()
	engine := NewEngine(cfg)

	// grep -- (no pattern after --) should error.
	_, err := engine.Apply(context.Background(), "data", "grep --")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pattern required")
}

func TestToolPipe_ShouldWrap_EmptyOps(t *testing.T) {
	tp := New(WithToolNames("t"), WithAllowedOps())
	inner := &mockTool{decl: &tool.Declaration{Name: "t"}}
	assert.False(t, tp.shouldWrap(inner))
}

func TestResultToString_HTMLChars(t *testing.T) {
	// Verify HTML special chars are NOT escaped.
	type resp struct {
		HTML string `json:"html"`
	}
	s := resultToString(resp{HTML: "<div>&amp;</div>"})
	assert.Contains(t, s, "<div>")
	assert.Contains(t, s, "&amp;")
	assert.NotContains(t, s, "\\u003c")
}
