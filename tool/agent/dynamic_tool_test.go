//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// dynStubTool is a minimal tool.Tool used to exercise the dynamic tool's
// selection logic without a runtime.
type dynStubTool struct {
	name        string
	description string
	inputSchema *tool.Schema
}

func (s dynStubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: s.description,
		InputSchema: s.inputSchema,
	}
}

func stubTools(names ...string) []tool.Tool {
	tools := make([]tool.Tool, 0, len(names))
	for _, n := range names {
		tools = append(tools, dynStubTool{name: n})
	}
	return tools
}

func selectedNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, declarationName(t))
	}
	sort.Strings(names)
	return names
}

func TestNewDynamicTool_Defaults(t *testing.T) {
	at := NewDynamicTool()

	decl := at.Declaration()
	require.Equal(t, DefaultDynamicToolName, decl.Name)
	require.Equal(t, "dynamic_agent", decl.Name)
	require.NotEmpty(t, decl.Description)
	require.True(t, at.dynamic)

	require.NotNil(t, decl.InputSchema)
	props := decl.InputSchema.Properties
	require.Contains(t, props, fieldRequest)
	require.Contains(t, props, fieldInstruction) // exposed by default
	require.Contains(t, props, fieldTools)       // exposed by default
	require.NotContains(t, props, fieldSkills)   // not exposed by default
	require.Equal(t, []string{fieldRequest}, decl.InputSchema.Required)

	require.NotNil(t, decl.OutputSchema)
	require.Equal(t, "string", decl.OutputSchema.Type)
}

func TestNewDynamicTool_TypesRemainComparable(t *testing.T) {
	_ = map[Tool]struct{}{}
	_ = map[agentToolOptions]struct{}{}
}

func TestNewDynamicTool_WarnsWhenPersistentHistoryEnabled(t *testing.T) {
	original := agentlog.Default
	logger := &dynTestWarnLogger{}
	agentlog.Default = logger
	t.Cleanup(func() {
		agentlog.Default = original
	})

	_ = NewDynamicTool(WithPersistentHistory())
	require.Equal(t, 1, logger.warnfCalls)
}

func TestNewDynamicTool_WithNameAndDescription(t *testing.T) {
	at := NewDynamicTool(
		WithName("explore"),
		WithDescription("custom desc"),
	)
	decl := at.Declaration()
	require.Equal(t, "explore", decl.Name)
	require.Equal(t, "custom desc", decl.Description)
	// Schema description should reference the configured name.
	require.Contains(t, decl.InputSchema.Description, "explore")
}

// TestNewDynamicTool_DescriptionReflectsExposedFields verifies the model-facing
// description advertises exactly the per-call fields present in the schema, so
// the model discovers configurability from the description+schema (not the
// tool name) and is never told about a field the schema omits.
func TestNewDynamicTool_DescriptionReflectsExposedFields(t *testing.T) {
	// Defaults: instruction + tools exposed, skills not.
	def := NewDynamicTool().Declaration().Description
	require.Contains(t, def, "'instruction'")
	require.Contains(t, def, "'tools'")
	require.NotContains(t, def, "'skills'")

	// All exposed.
	all := NewDynamicTool(WithExposeSkillSelection(true)).Declaration().Description
	require.Contains(t, all, "'instruction'")
	require.Contains(t, all, "'tools'")
	require.Contains(t, all, "'skills'")

	// None exposed: only the fixed boundary statement, no field hints.
	none := NewDynamicTool(
		WithExposeInstruction(false),
		WithExposeToolSelection(false),
	).Declaration().Description
	require.NotContains(t, none, "'instruction'")
	require.NotContains(t, none, "'tools'")
	require.NotContains(t, none, "'skills'")
	require.Contains(t, none, "short-lived", "boundary statement must remain")
}

// TestNewDynamicTool_DescriptionReflectsHistoryScope verifies the model-facing
// description matches the configured history scope (isolated by default,
// parent-branch when opted in) so it never tells the model "no memory" when the
// sub-agent actually inherits the parent conversation. It also checks the
// capability-boundary wording covers code-defined surfaces, not only the parent
// agent's currently-allowed tools.
func TestNewDynamicTool_DescriptionReflectsHistoryScope(t *testing.T) {
	def := NewDynamicTool().Declaration().Description
	require.Contains(t, def, "no memory of this conversation")
	require.NotContains(t, def, "see the current conversation's history")
	require.Contains(t, def, "code-defined capability boundary")
	require.Contains(t, def, "parent conversation focused")

	pb := NewDynamicTool(
		WithHistoryScope(HistoryScopeParentBranch),
	).Declaration().Description
	require.Contains(t, pb, "see the current conversation's history")
	require.NotContains(t, pb, "no memory of this conversation")
	require.NotContains(t, pb, "parent conversation focused")
}

// TestNewDynamicTool_CapabilityToolsEnumerated verifies a statically configured
// capability surface (WithCapabilityTools) is enumerated in the schema so the
// model selects from known names, while the parent-derived and provider
// surfaces (resolved per call) are not enumerated at schema-build time.
func TestNewDynamicTool_CapabilityToolsEnumerated(t *testing.T) {
	// WithCapabilityTools: names appear as enum and in the default description.
	at := NewDynamicTool(WithCapabilityTools(stubTools("search_code", "read_file")))
	ts := at.Declaration().InputSchema.Properties[fieldTools]
	require.NotNil(t, ts.Items)
	require.ElementsMatch(t, []any{"read_file", "search_code"}, ts.Items.Enum)
	require.Contains(t, ts.Description, "read_file")
	require.Contains(t, ts.Description, "search_code")

	// Duplicate names are de-duplicated; the enum holds unique names only and
	// the size bound is applied after de-duplication.
	atDup := NewDynamicTool(WithCapabilityTools(stubTools("dup", "dup", "other")))
	tsDup := atDup.Declaration().InputSchema.Properties[fieldTools]
	require.ElementsMatch(t, []any{"dup", "other"}, tsDup.Items.Enum)

	// A custom tools description is respected (no auto-append) but enum stays.
	at2 := NewDynamicTool(
		WithCapabilityTools(stubTools("a")),
		WithToolsDescription("pick wisely"),
	)
	ts2 := at2.Declaration().InputSchema.Properties[fieldTools]
	require.Equal(t, "pick wisely", ts2.Description)
	require.Equal(t, []any{"a"}, ts2.Items.Enum)

	// Default parent-derived surface: not enumerable at schema-build time.
	ts3 := NewDynamicTool().Declaration().InputSchema.Properties[fieldTools]
	require.Nil(t, ts3.Items.Enum)

	// Provider-based surface: resolved per call, not enumerated at build time.
	at4 := NewDynamicTool(WithCapabilityProvider(
		func(context.Context, *agent.Invocation) ([]tool.Tool, map[string]bool) {
			return stubTools("x"), nil
		}))
	ts4 := at4.Declaration().InputSchema.Properties[fieldTools]
	require.Nil(t, ts4.Items.Enum)

	// Provider takes runtime precedence over static tools, so the schema must
	// not expose the static enum when both options are configured.
	at4b := NewDynamicTool(
		WithCapabilityTools(stubTools("static_only")),
		WithCapabilityProvider(
			func(context.Context, *agent.Invocation) ([]tool.Tool, map[string]bool) {
				return stubTools("runtime_only"), nil
			},
		),
	)
	ts4b := at4b.Declaration().InputSchema.Properties[fieldTools]
	require.Nil(t, ts4b.Items.Enum)

	// Structured provider: also resolved per call, not enumerable at schema-build time.
	at5 := NewDynamicTool(WithCapabilitySurfaceProvider(
		func(context.Context, *agent.Invocation) CapabilitySurface {
			return CapabilitySurface{Tools: stubTools("x")}
		}))
	ts5 := at5.Declaration().InputSchema.Properties[fieldTools]
	require.Nil(t, ts5.Items.Enum)

	at5b := NewDynamicTool(
		WithCapabilityTools(stubTools("static_only")),
		WithCapabilitySurfaceProvider(
			func(context.Context, *agent.Invocation) CapabilitySurface {
				return CapabilitySurface{Tools: stubTools("runtime_only")}
			},
		),
	)
	ts5b := at5b.Declaration().InputSchema.Properties[fieldTools]
	require.Nil(t, ts5b.Items.Enum)
}

func TestNewDynamicTool_ExposeToggles(t *testing.T) {
	at := NewDynamicTool(
		WithExposeInstruction(false),
		WithExposeToolSelection(false),
		WithExposeSkillSelection(true),
	)
	props := at.Declaration().InputSchema.Properties
	require.Contains(t, props, fieldRequest)
	require.NotContains(t, props, fieldInstruction)
	require.NotContains(t, props, fieldTools)
	require.Contains(t, props, fieldSkills)
}

func TestDynamicTool_CapabilitySkillsProviderOverridesFixed(t *testing.T) {
	fixed := newDynTestSkillRepo(t, "fixed")
	dynamic := newDynTestSkillRepo(t, "dynamic")
	calls := 0
	at := NewDynamicTool(
		WithCapabilitySkills(fixed),
		WithCapabilitySkillsProvider(
			func(context.Context, *agent.Invocation) skill.Repository {
				calls++
				return dynamic
			},
		),
	)

	got := at.dynamicMaxSkillRepo(
		context.Background(),
		agent.NewInvocation(),
	)
	require.Equal(t, dynamic.Summaries(), got.Summaries())
	require.Equal(t, 1, calls)
}

func TestCapabilitySearchTool_SearchesToolsAndSkills(t *testing.T) {
	repo := newDynTestSkillRepo(t, "coding", "weather")
	parent := agent.NewInvocation()
	search := NewCapabilitySearchTool(
		WithCapabilitySearchProvider(
			func(
				_ context.Context,
				got *agent.Invocation,
			) ([]tool.Tool, map[string]bool) {
				require.Same(t, parent, got)
				return stubTools("read_file", "search_web"), nil
			},
		),
		WithCapabilitySearchSkillsProvider(
			func(
				_ context.Context,
				got *agent.Invocation,
			) skill.Repository {
				require.Same(t, parent, got)
				return repo
			},
		),
	)
	callable, ok := search.(tool.CallableTool)
	require.True(t, ok)

	out, err := callable.Call(
		agent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"query":"read","limit":10}`),
	)
	require.NoError(t, err)
	got := out.(CapabilitySearchResult)
	require.Equal(t, []CapabilityToolSummary{{Name: "read_file"}}, got.Tools)
	require.Empty(t, got.Skills)
	require.False(t, got.Truncated)
	require.Contains(t, got.Note, "dynamic_agent")

	out, err = callable.Call(
		agent.NewInvocationContext(context.Background(), parent),
		[]byte(`{"query":"","limit":2}`),
	)
	require.NoError(t, err)
	got = out.(CapabilitySearchResult)
	require.Len(t, got.Tools, 2)
	require.Empty(t, got.Skills)
	require.Equal(t, "catalog", got.SearchMode)
	require.Equal(t, 4, got.Total)
	require.True(t, got.Truncated)
	require.Equal(t, []CapabilityNameGroup{
		{Kind: "tools", Names: []string{"read_file", "search_web"}},
		{Kind: "skills", Names: []string{"coding", "weather"}},
	}, got.Groups)
}

func TestCapabilitySearchTool_SelectsExactNames(t *testing.T) {
	repo := newDynTestSkillRepo(t, "coding", "weather")
	search := NewCapabilitySearchTool(
		WithCapabilitySearchProvider(
			func(
				context.Context,
				*agent.Invocation,
			) ([]tool.Tool, map[string]bool) {
				return stubTools("read_file", "search_web"), nil
			},
		),
		WithCapabilitySearchSkillsProvider(
			func(context.Context, *agent.Invocation) skill.Repository {
				return repo
			},
		),
	)
	callable := search.(tool.CallableTool)

	out, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"select:search_web,coding,missing"}`),
	)
	require.NoError(t, err)
	got := out.(CapabilitySearchResult)
	require.Equal(t, "select", got.SearchMode)
	require.Equal(t, []CapabilityToolSummary{{Name: "search_web"}}, got.Tools)
	require.Equal(t, []CapabilitySkillSummary{{
		Name:        "coding",
		Description: "test skill",
	}}, got.Skills)
	require.Equal(t, []string{"missing"}, got.Missing)
	require.False(t, got.Truncated)
}

func TestCapabilitySearchTool_ResolvesToolAliases(t *testing.T) {
	search := NewCapabilitySearchTool(
		WithCapabilitySearchProvider(
			func(
				context.Context,
				*agent.Invocation,
			) ([]tool.Tool, map[string]bool) {
				return []tool.Tool{dynStubTool{
					name:        "browser",
					description: "Control a real browser.",
				}}, nil
			},
		),
		WithCapabilitySearchToolAliases(map[string]string{
			"trpc-claw-browser-runtime": "browser",
			"browser-runtime":           "browser",
		}),
	)
	callable := search.(tool.CallableTool)

	out, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"trpc-claw-browser-runtime","limit":5}`),
	)
	require.NoError(t, err)
	got := out.(CapabilitySearchResult)
	require.Equal(t, "bm25", got.SearchMode)
	require.Equal(t, []CapabilityToolSummary{{
		Name:        "browser",
		Description: "Control a real browser.",
	}}, got.Tools)

	out, err = callable.Call(
		context.Background(),
		[]byte(`{"query":"select:trpc-claw-browser-runtime,browser-runtime"}`),
	)
	require.NoError(t, err)
	got = out.(CapabilitySearchResult)
	require.Equal(t, "select", got.SearchMode)
	require.Equal(t, []CapabilityToolSummary{{
		Name:        "browser",
		Description: "Control a real browser.",
	}}, got.Tools)
	require.Empty(t, got.Missing)
}

func TestCapabilitySearchTool_SearchesSchemaMetadataWithBM25(
	t *testing.T,
) {
	search := NewCapabilitySearchTool(
		WithCapabilitySearchProvider(
			func(
				context.Context,
				*agent.Invocation,
			) ([]tool.Tool, map[string]bool) {
				return []tool.Tool{
					dynStubTool{
						name:        "write_file",
						description: "Modify file contents.",
						inputSchema: &tool.Schema{
							Type: "object",
							Properties: map[string]*tool.Schema{
								"path": {
									Type:        "string",
									Description: "Workspace path to update.",
								},
							},
						},
					},
					dynStubTool{
						name:        "search_web",
						description: "Search public web pages.",
					},
				}, nil
			},
		),
	)
	callable := search.(tool.CallableTool)

	out, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"workspace path","limit":5}`),
	)
	require.NoError(t, err)
	got := out.(CapabilitySearchResult)
	require.Equal(t, "bm25", got.SearchMode)
	require.Equal(t, []CapabilityToolSummary{{
		Name:        "write_file",
		Description: "Modify file contents.",
	}}, got.Tools)
	require.Empty(t, got.Skills)
	require.Equal(t, 1, got.Total)
}

func TestCapabilitySearchTool_RebuildsCachedIndexOnCapabilityChange(
	t *testing.T,
) {
	tools := []tool.Tool{dynStubTool{
		name:        "alpha_tool",
		description: "Alpha-only capability.",
	}}
	search := NewCapabilitySearchTool(
		WithCapabilitySearchProvider(
			func(
				context.Context,
				*agent.Invocation,
			) ([]tool.Tool, map[string]bool) {
				return tools, nil
			},
		),
	)
	callable := search.(tool.CallableTool)

	out, err := callable.Call(
		context.Background(),
		[]byte(`{"query":"alpha"}`),
	)
	require.NoError(t, err)
	got := out.(CapabilitySearchResult)
	require.Equal(t, []CapabilityToolSummary{{
		Name:        "alpha_tool",
		Description: "Alpha-only capability.",
	}}, got.Tools)

	tools = []tool.Tool{dynStubTool{
		name:        "beta_tool",
		description: "Beta-only capability.",
	}}
	out, err = callable.Call(
		context.Background(),
		[]byte(`{"query":"beta"}`),
	)
	require.NoError(t, err)
	got = out.(CapabilitySearchResult)
	require.Equal(t, []CapabilityToolSummary{{
		Name:        "beta_tool",
		Description: "Beta-only capability.",
	}}, got.Tools)
}

func TestNewDynamicTool_CustomFieldDescriptions(t *testing.T) {
	at := NewDynamicTool(
		WithRequestDescription("req-desc"),
		WithInstructionDescription("inst-desc"),
		WithToolsDescription("tools-desc"),
		WithExposeSkillSelection(true),
		WithSkillsDescription("skills-desc"),
	)
	props := at.Declaration().InputSchema.Properties
	require.Equal(t, "req-desc", props[fieldRequest].Description)
	require.Equal(t, "inst-desc", props[fieldInstruction].Description)
	require.Equal(t, "tools-desc", props[fieldTools].Description)
	require.Equal(t, "skills-desc", props[fieldSkills].Description)
}

func TestParseDynamicArgs(t *testing.T) {
	t.Run("full args with everything exposed", func(t *testing.T) {
		at := NewDynamicTool(WithExposeSkillSelection(true))
		spec := at.parseDynamicArgs([]byte(
			`{"request":" do it ","instruction":" be brief ","tools":["a","b"],"skills":["s1"]}`,
		))
		require.Equal(t, "do it", spec.request)
		require.Equal(t, "be brief", spec.instruction)
		require.True(t, spec.toolsProvided)
		require.Equal(t, []string{"a", "b"}, spec.tools)
		require.True(t, spec.skillsProvided)
		require.Equal(t, []string{"s1"}, spec.skills)
	})

	t.Run("omitted tools means not provided", func(t *testing.T) {
		at := NewDynamicTool()
		spec := at.parseDynamicArgs([]byte(`{"request":"x"}`))
		require.False(t, spec.toolsProvided)
		require.Empty(t, spec.tools)
	})

	t.Run("empty tools array means provided-but-none", func(t *testing.T) {
		at := NewDynamicTool()
		spec := at.parseDynamicArgs([]byte(`{"request":"x","tools":[]}`))
		require.True(t, spec.toolsProvided)
		require.Empty(t, spec.tools)
	})

	t.Run("fields ignored when not exposed", func(t *testing.T) {
		at := NewDynamicTool(
			WithExposeInstruction(false),
			WithExposeToolSelection(false),
		)
		spec := at.parseDynamicArgs([]byte(
			`{"request":"x","instruction":"ignored","tools":["a"],"skills":["s"]}`,
		))
		require.Equal(t, "x", spec.request)
		require.Empty(t, spec.instruction)
		require.False(t, spec.toolsProvided)
		require.False(t, spec.skillsProvided) // skills not exposed by default
	})

	t.Run("invalid json falls back to raw request", func(t *testing.T) {
		at := NewDynamicTool()
		spec := at.parseDynamicArgs([]byte(`just do the thing`))
		require.Equal(t, "just do the thing", spec.request)
	})

	t.Run("tool names are trimmed and de-duplicated", func(t *testing.T) {
		at := NewDynamicTool()
		spec := at.parseDynamicArgs([]byte(`{"request":"x","tools":[" a ","a","","b"]}`))
		require.Equal(t, []string{"a", "b"}, spec.tools)
	})
}

func TestSelectDynamicTools_ExcludesSelfAndTransfer(t *testing.T) {
	at := NewDynamicTool() // name == dynamic_agent
	maxTools := stubTools(
		"file_read",
		"file_write",
		at.name,                   // self must be excluded
		transfer.TransferToolName, // transfer must be excluded
	)
	// No selection => all candidates minus excluded.
	selected, warnings, err := at.selectDynamicTools(maxTools, toolNameSet(maxTools), nil, nil, dynamicSpec{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"file_read", "file_write"}, selectedNames(selected))
}

func TestSelectDynamicTools_OnlyUserTools(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("file_read", "framework_tool")
	userTools := map[string]bool{"file_read": true} // framework_tool not a user tool
	selected, warnings, err := at.selectDynamicTools(maxTools, userTools, nil, nil, dynamicSpec{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"file_read"}, selectedNames(selected))
}

func TestSelectDynamicTools_SubsetSelection(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("a", "b", "c")
	spec := dynamicSpec{toolsProvided: true, tools: []string{"a", "c"}}
	selected, warnings, err := at.selectDynamicTools(maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"a", "c"}, selectedNames(selected))
}

func TestSelectDynamicTools_ResolvesAliases(t *testing.T) {
	at := NewDynamicTool(WithCapabilityToolAliases(map[string]string{
		"browser-runtime": "browser",
		"runtime":         "browser",
		"blank":           "",
		"same":            "same",
	}))
	maxTools := stubTools("browser", "web_fetch")
	spec := dynamicSpec{
		toolsProvided: true,
		tools:         []string{"browser-runtime", "browser", "web_fetch"},
	}
	selected, warnings, err := at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"browser", "web_fetch"}, selectedNames(selected))

	spec = dynamicSpec{toolsProvided: true, tools: []string{"runtime"}}
	selected, warnings, err = at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"browser"}, selectedNames(selected))
}

func TestSelectDynamicTools_UnknownRequestedToolWarns(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("a", "b")
	spec := dynamicSpec{toolsProvided: true, tools: []string{"a", "nope"}}
	selected, warnings, err := at.selectDynamicTools(maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, selectedNames(selected))
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "nope")
}

func TestSelectDynamicTools_UnavailableRequestedToolWarnsWithReason(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("available", "secret")
	unavailable := map[string]UnavailableCapability{
		"secret": {
			Name:   "secret",
			Reason: CapabilityUnavailableReasonMissingCredential,
			Detail: "configure SECRET_TOKEN",
		},
	}

	// No selection: unavailable tools are simply not mounted and do not add noise.
	selected, warnings, err := at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), nil, unavailable, dynamicSpec{})
	require.NoError(t, err)
	require.Equal(t, []string{"available"}, selectedNames(selected))
	require.Empty(t, warnings)

	// Explicit selection: the parent gets an actionable reason instead of a
	// generic "not available" warning.
	spec := dynamicSpec{toolsProvided: true, tools: []string{"available", "secret"}}
	selected, warnings, err = at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), nil, unavailable, spec)
	require.NoError(t, err)
	require.Equal(t, []string{"available"}, selectedNames(selected))
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "secret")
	require.Contains(t, warnings[0], string(CapabilityUnavailableReasonMissingCredential))
	require.Contains(t, warnings[0], "SECRET_TOKEN")
}

func TestFormatUnavailableToolWarning_SanitizesDetail(t *testing.T) {
	warning := formatUnavailableToolWarning("secret", UnavailableCapability{
		Reason: CapabilityUnavailableReasonPermissionDenied,
		Detail: "  policy\n\tdenied  ",
	})
	require.Contains(t, warning, "detail: policy denied")
	require.NotContains(t, warning, "\n")
	require.Contains(t, warning, string(CapabilityUnavailableReasonPermissionDenied))

	longDetail := strings.Repeat("x", maxUnavailableDetailRunes+5)
	warning = formatUnavailableToolWarning("secret", UnavailableCapability{
		Detail: longDetail,
	})
	require.Contains(t, warning, string(CapabilityUnavailableReasonUnknown))
	require.Contains(t, warning, strings.Repeat("x", maxUnavailableDetailRunes)+"...")
	require.NotContains(t, warning, strings.Repeat("x", maxUnavailableDetailRunes+1))
}

func TestSelectDynamicTools_AllUnknownWarnsNoUserTools(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("a", "b")
	spec := dynamicSpec{toolsProvided: true, tools: []string{"x", "y"}}
	selected, warnings, err := at.selectDynamicTools(maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.Empty(t, selected)
	require.NotEmpty(t, warnings)
	require.Error(t, err)
	require.Contains(t, err.Error(), "none of the requested tools")
	require.Contains(t, err.Error(), "a, b")
}

// TestSelectDynamicTools_ExcludesExternalTools verifies that caller-executed
// external tools are never selectable for the child (fix 2): they are
// visible-but-not-executed for the parent and have no execution channel in a
// synchronous sub-agent.
func TestSelectDynamicTools_ExcludesExternalTools(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("tool_a", "ext_tool")
	externalNames := map[string]bool{"ext_tool": true}

	// No selection: external tool must not appear among the candidates.
	selected, warnings, err := at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), externalNames, nil, dynamicSpec{})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, []string{"tool_a"}, selectedNames(selected))

	// Explicit selection of an external tool is rejected as unavailable.
	spec := dynamicSpec{toolsProvided: true, tools: []string{"ext_tool"}}
	selected, warnings, err = at.selectDynamicTools(
		maxTools, toolNameSet(maxTools), externalNames, nil, spec)
	require.Empty(t, selected)
	require.NotEmpty(t, warnings)
	require.Error(t, err)
}

// TestSelectDynamicTools_EmptyArrayAllowsNoneWithoutWarning verifies that an
// explicit empty "tools": [] is treated as a deliberate tool-free run, not an
// error (fix 6).
func TestSelectDynamicTools_EmptyArrayAllowsNoneWithoutWarning(t *testing.T) {
	at := NewDynamicTool()
	maxTools := stubTools("a", "b")
	spec := dynamicSpec{toolsProvided: true, tools: nil} // provided, but empty
	selected, warnings, err := at.selectDynamicTools(maxTools, toolNameSet(maxTools), nil, nil, spec)
	require.NoError(t, err)
	require.Empty(t, selected)
	require.Empty(t, warnings, "an explicit empty selection must not warn")
}

func TestDynamicMaxToolSurface_DetailedProviderWinsAndReportsUnavailable(t *testing.T) {
	legacyCalled := false
	at := NewDynamicTool(
		WithCapabilityTools(stubTools("static")),
		WithCapabilityProvider(func(context.Context, *agent.Invocation) ([]tool.Tool, map[string]bool) {
			legacyCalled = true
			return stubTools("legacy"), nil
		}),
		WithCapabilitySurfaceProvider(func(context.Context, *agent.Invocation) CapabilitySurface {
			return CapabilitySurface{
				Tools:             stubTools("available"),
				UserToolNames:     map[string]bool{"available": true},
				ExternalToolNames: map[string]bool{"external": true},
				UnavailableTools: []UnavailableCapability{{
					Name:   " unavailable ",
					Reason: CapabilityUnavailableReasonExecutorUnavailable,
					Detail: " no sandbox ",
				}},
			}
		}),
	)

	tools, users, externals, unavailable := at.dynamicMaxToolSurface(context.Background(), nil)
	require.False(t, legacyCalled, "structured provider should take precedence")
	require.Equal(t, []string{"available"}, selectedNames(tools))
	require.True(t, users["available"])
	require.True(t, externals["external"])
	got := unavailable["unavailable"]
	require.Equal(t, CapabilityUnavailableReasonExecutorUnavailable, got.Reason)
	require.Equal(t, "no sandbox", got.Detail)
}

func TestUnavailableCapabilityMap_NormalizesEntries(t *testing.T) {
	values := unavailableCapabilityMap([]UnavailableCapability{
		{Name: "  ", Reason: CapabilityUnavailableReasonPermissionDenied},
		{Name: " tool_a ", Detail: "  access\n\tdenied  "},
	})
	require.Len(t, values, 1)
	got := values["tool_a"]
	require.Equal(t, CapabilityUnavailableReasonUnknown, got.Reason)
	require.Equal(t, "access denied", got.Detail)
	require.Nil(t, unavailableCapabilityMap(nil))
	require.Nil(t, unavailableCapabilityMap([]UnavailableCapability{{Name: " "}}))
}

func TestFormatUnavailableToolWarning_WithoutDetail(t *testing.T) {
	warning := formatUnavailableToolWarning("tool_a", UnavailableCapability{
		Reason: CapabilityUnavailableReasonNetworkDisabled,
	})
	require.Contains(t, warning, "tool_a")
	require.Contains(t, warning, string(CapabilityUnavailableReasonNetworkDisabled))
	require.NotContains(t, warning, "detail:")
}

func TestDynamicMaxToolSurface_LegacyProviderAndNilParent(t *testing.T) {
	at := NewDynamicTool(WithCapabilityProvider(
		func(context.Context, *agent.Invocation) ([]tool.Tool, map[string]bool) {
			return stubTools("legacy"), map[string]bool{"legacy": true}
		},
	))
	tools, users, externals, unavailable := at.dynamicMaxToolSurface(context.Background(), nil)
	require.Equal(t, []string{"legacy"}, selectedNames(tools))
	require.True(t, users["legacy"])
	require.Nil(t, externals)
	require.Nil(t, unavailable)

	tools, users, externals, unavailable = NewDynamicTool().dynamicMaxToolSurface(context.Background(), nil)
	require.Nil(t, tools)
	require.Nil(t, users)
	require.Nil(t, externals)
	require.Nil(t, unavailable)
}

func TestDedupeNonEmpty(t *testing.T) {
	require.Nil(t, dedupeNonEmpty(nil))
	require.Equal(t,
		[]string{"a", "b"},
		dedupeNonEmpty([]string{" a", "a ", "", "  ", "b"}),
	)
}

func TestDeclarationNameAndToolNameSet(t *testing.T) {
	require.Equal(t, "", declarationName(nil))
	require.Equal(t, "foo", declarationName(dynStubTool{name: "foo"}))

	set := toolNameSet(stubTools("a", "b", "a"))
	require.True(t, set["a"])
	require.True(t, set["b"])
	require.Len(t, set, 2)
}

func TestFormatResponseWithWarnings(t *testing.T) {
	at := NewDynamicTool()
	require.Equal(t, "hello", at.formatResponseWithWarnings("hello", nil))

	out := at.formatResponseWithWarnings("hello", []string{"w1", "w2"})
	require.Contains(t, out, at.name)
	require.Contains(t, out, "w1")
	require.Contains(t, out, "w2")
	require.Contains(t, out, "hello")
}

func TestDynamicSurfaceNodeID_UniqueWithPrefix(t *testing.T) {
	a := dynamicSurfaceNodeID("dynamic_agent")
	b := dynamicSurfaceNodeID("dynamic_agent")
	require.NotEqual(t, a, b)
	require.Contains(t, a, "dynamic_agent/dynamic-")
}

func TestCallDynamic_RequiresParentInvocation(t *testing.T) {
	at := NewDynamicTool()
	_, err := at.Call(context.Background(), []byte(`{"request":"x"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parent invocation")
}

func TestCallDynamic_RequiresRequest(t *testing.T) {
	at := NewDynamicTool()
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)
	_, err := at.Call(ctx, []byte(`{"request":"  "}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "request")
}

func TestCallDynamic_TimeoutBoundsSubAgent(t *testing.T) {
	t.Parallel()

	main := llmagent.New("main", llmagent.WithModel(&dynBlockingModel{}))
	at := NewDynamicTool(WithDynamicTimeout(10 * time.Millisecond))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	start := time.Now()
	_, err := at.Call(ctx, []byte(`{"request":"wait"}`))

	require.Error(t, err)
	require.Contains(t, err.Error(), context.DeadlineExceeded.Error())
	require.Less(t, time.Since(start), time.Second)
}

// dynRecordingModel records the set of tool names visible in each request.
type dynRecordingModel struct {
	name     string
	response string
	mu       sync.Mutex
	seen     [][]string
}

func (m *dynRecordingModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	names := make([]string, 0, len(request.Tools))
	for n := range request.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	m.mu.Lock()
	m.seen = append(m.seen, names)
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(m.response),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *dynRecordingModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *dynRecordingModel) snapshot() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]string, len(m.seen))
	copy(out, m.seen)
	return out
}

type dynBlockingModel struct{}

func (m *dynBlockingModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *dynBlockingModel) Info() model.Info {
	return model.Info{Name: "blocking"}
}

func newDynTestTool(name string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (string, error) { return name, nil },
		function.WithName(name),
		function.WithDescription("test tool "+name),
	)
}

// TestNewDynamicTool_Integration_RestrictsTools runs an actual dynamic
// sub-agent (derived from the parent agent) and asserts the model selection
// narrows the child's tool surface.
func TestNewDynamicTool_Integration_RestrictsTools(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "child-done"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			newDynTestTool("tool_b"),
		}),
	)

	at := NewDynamicTool() // derive base agent + surface from the parent

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"use tool a","tools":["tool_a"]}`))
	require.NoError(t, err)
	require.Equal(t, "child-done", got)

	seen := recModel.snapshot()
	require.Len(t, seen, 1, "child should have run exactly once")
	require.Equal(t, []string{"tool_a"}, seen[0],
		"child must see only the selected user tool")
}

// TestNewDynamicTool_Integration_DefaultAllTools verifies that omitting the
// tools field lets the child use the full permitted (user) surface.
func TestNewDynamicTool_Integration_DefaultAllTools(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			newDynTestTool("tool_b"),
		}),
	)

	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"do something"}`))
	require.NoError(t, err)
	require.Equal(t, "ok", got)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a", "tool_b"}, seen[0])
}

func TestNewDynamicTool_Integration_UnavailableReasonReturnedToParent(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "child-done"}
	main := llmagent.New("main", llmagent.WithModel(recModel))
	at := NewDynamicTool(WithCapabilitySurfaceProvider(
		func(context.Context, *agent.Invocation) CapabilitySurface {
			return CapabilitySurface{
				Tools: stubTools("available"),
				UnavailableTools: []UnavailableCapability{{
					Name:   "secret",
					Reason: CapabilityUnavailableReasonMissingCredential,
					Detail: "configure SECRET_TOKEN",
				}},
			}
		},
	))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(
		`{"request":"use both tools","tools":["available","secret"]}`,
	))
	require.NoError(t, err)
	out, ok := got.(string)
	require.True(t, ok)
	require.Contains(t, out, "Note from "+at.name)
	require.Contains(t, out, "secret")
	require.Contains(t, out, string(CapabilityUnavailableReasonMissingCredential))
	require.Contains(t, out, "configure SECRET_TOKEN")
	require.Contains(t, out, "child-done")

	seen := recModel.snapshot()
	require.Len(t, seen, 1, "child should have run exactly once")
	require.Equal(t, []string{"available"}, seen[0],
		"child must not see known-but-unavailable tools")
}

func TestNewDynamicTool_Integration_AllRequestedToolsUnavailableErrors(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "child-done"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(
		`{"request":"use unavailable tool","tools":["web_search"]}`,
	))
	require.Error(t, err)
	require.Contains(t, err.Error(), "none of the requested tools")
	require.Empty(t, recModel.snapshot(), "child must not run without requested tools")
}

// TestNewDynamicTool_Integration_WithTemplateAgent verifies that a distinctly
// named, tool-less template agent still receives the tools selected from the
// parent surface (injected via the surface patch), and that the template agent
// is the one that actually runs the child.
func TestNewDynamicTool_Integration_WithTemplateAgent(t *testing.T) {
	parentModel := &dynRecordingModel{name: "parent", response: "parent-should-not-run"}
	templateModel := &dynRecordingModel{name: "template", response: "child-done"}

	main := llmagent.New(
		"main",
		llmagent.WithModel(parentModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			newDynTestTool("tool_b"),
		}),
	)
	// Template defines the execution boundary (identity + model) but has no
	// tools of its own; the selected tools are injected by the patch.
	subTemplate := llmagent.New(
		"subagent",
		llmagent.WithModel(templateModel),
		llmagent.WithInstruction("focused worker"),
	)

	at := NewDynamicTool(WithTemplateAgent(subTemplate))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"use tool a","tools":["tool_a"]}`))
	require.NoError(t, err)
	require.Equal(t, "child-done", got)

	require.Empty(t, parentModel.snapshot(), "parent model must not run the child")
	seen := templateModel.snapshot()
	require.Len(t, seen, 1, "template agent should run exactly once")
	require.Equal(t, []string{"tool_a"}, seen[0],
		"tools selected from the parent surface must be injected into the template")
}

// TestNewDynamicTool_Integration_ExcludesSelf ensures the dynamic tool never
// leaks itself into the child surface, preventing runaway recursion.
func TestNewDynamicTool_Integration_ExcludesSelf(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	at := NewDynamicTool()
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			at, // the dynamic tool is also registered on the parent
		}),
	)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a"}, seen[0],
		"the dynamic tool must not appear in its own child surface")
}

func TestNewDynamicTool_Integration_ExcludesSiblingAgentTools(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	at := NewDynamicTool()
	otherDynamic := NewDynamicTool(WithName("other_dynamic"))
	wrappedAgent := llmagent.New(
		"wrapped_agent",
		llmagent.WithModel(&dynRecordingModel{name: "wrapped"}),
	)
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			at,
			otherDynamic,
			NewTool(wrappedAgent),
		}),
	)

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a"}, seen[0],
		"dynamic child surface must exclude all AgentTool entrypoints")
}

func TestNewDynamicTool_RestoresLiveSessionFromParallelClone(t *testing.T) {
	const liveOnlyContent = "dynamic-live-only-history"
	liveSess, frozenSess := liveAndFrozenSessionsForTest(t, liveOnlyContent)

	child := &liveSessionHistoryAgent{
		name:            "dynamic-live-session-child",
		liveOnlyContent: liveOnlyContent,
	}
	at := NewDynamicTool()
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(child),
		agent.WithInvocationSession(frozenSess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	livesession.Attach(parent, liveSess)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	res, err := at.Call(ctx, []byte(`{"request":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "saw-live-session", res)
	require.Same(t, liveSess, child.seenSession)
	require.True(t, child.sawLiveOnly)
}

// --- Fix 5: WithName is dynamic-only ---------------------------------------

// TestNewTool_IgnoresWithName ensures WithName does not rename a wrapped-agent
// AgentTool, so its model-facing name never diverges from its identity (filter
// key, team node id, recursion guards), and the misuse is surfaced.
func TestNewTool_IgnoresWithName(t *testing.T) {
	original := agentlog.Default
	logger := &dynTestWarnLogger{}
	agentlog.Default = logger
	t.Cleanup(func() {
		agentlog.Default = original
	})

	wrapped := llmagent.New("wrapped", llmagent.WithModel(&dynRecordingModel{name: "m"}))
	at := NewTool(wrapped, WithName("renamed"))
	require.Equal(t, "wrapped", at.Declaration().Name,
		"NewTool keeps the wrapped agent's name; WithName is dynamic-only")
	require.Equal(t, 1, logger.warnfCalls)
}

type dynTestWarnLogger struct {
	warnfCalls int
}

func (l *dynTestWarnLogger) Debug(args ...any)                 {}
func (l *dynTestWarnLogger) Debugf(format string, args ...any) {}
func (l *dynTestWarnLogger) Info(args ...any)                  {}
func (l *dynTestWarnLogger) Infof(format string, args ...any)  {}
func (l *dynTestWarnLogger) Warn(args ...any)                  {}
func (l *dynTestWarnLogger) Warnf(format string, args ...any)  { l.warnfCalls++ }
func (l *dynTestWarnLogger) Error(args ...any)                 {}
func (l *dynTestWarnLogger) Errorf(format string, args ...any) {}
func (l *dynTestWarnLogger) Fatal(args ...any)                 {}
func (l *dynTestWarnLogger) Fatalf(format string, args ...any) {}

// --- Fix 4: stream-mode warnings reach the parent model --------------------

func TestWarningsNoteAndPrefixStreamResult(t *testing.T) {
	at := NewDynamicTool()
	require.Equal(t, "", at.warningsNote(nil))
	note := at.warningsNote([]string{"w1", "w2"})
	require.Contains(t, note, at.name)
	require.Contains(t, note, "w1")
	require.Contains(t, note, "w2")

	// Empty prefix leaves the result untouched (non-dynamic streaming path).
	require.Equal(t, "x", prefixStreamResult("", "x"))
	// String/nil results are prefixed; other types are left intact.
	require.Equal(t, "p", prefixStreamResult("p", nil))
	require.Equal(t, "p", prefixStreamResult("p", ""))
	require.Equal(t, "p\nx", prefixStreamResult("p", "x"))
	require.Equal(t, 42, prefixStreamResult("p", 42))
}

// --- Fixes 2/3/7: skills default to parent boundary, executor + context ----

func newDynTestSkillRepo(t *testing.T, names ...string) skill.Repository {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		dir := filepath.Join(root, n)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "SKILL.md"),
			[]byte(fmt.Sprintf(
				"---\nname: %s\ndescription: test skill\n---\nbody\n", n)),
			0o644,
		))
	}
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	return repo
}

type fakeDynCodeExecutor struct{}

func (fakeDynCodeExecutor) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (fakeDynCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

// taggedDynCodeExecutor is a code executor carrying an identity tag so tests can
// assert which executor (template vs parent run-scoped) was resolved.
type taggedDynCodeExecutor struct{ tag string }

func (taggedDynCodeExecutor) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (taggedDynCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

type invocationCheckingExecutorAgent struct {
	agent.Agent
	exec      codeexecutor.CodeExecutor
	sawInv    bool
	sawAgent  bool
	sawParent bool
}

func (a *invocationCheckingExecutorAgent) InvocationCodeExecutor(
	_ context.Context,
	inv *agent.Invocation,
) codeexecutor.CodeExecutor {
	if inv == nil {
		panic("InvocationCodeExecutor received nil invocation")
	}
	a.sawInv = true
	a.sawAgent = inv.Agent == a
	a.sawParent = inv.GetParentInvocation() != nil
	return a.exec
}

// TestSelectDynamicSkills_DefaultPatchesBoundary verifies that, with no model
// selection, the child is patched to the resolved boundary repository so a
// template agent's own skills cannot leak.
func TestSelectDynamicSkills_DefaultPatchesBoundary(t *testing.T) {
	repo := newDynTestSkillRepo(t, "alpha")
	at := NewDynamicTool(WithCapabilitySkills(repo))
	got, patch, warns := at.selectDynamicSkills(context.Background(), nil, dynamicSpec{})
	require.True(t, patch)
	require.Equal(t, repo, got)
	require.Empty(t, warns)
}

// TestSelectDynamicSkills_TemplateOverridesEmpty verifies that when a template
// agent is configured but there is no boundary repository, the patch still sets
// an empty repository so the template's own skills do not leak into the child.
func TestSelectDynamicSkills_TemplateOverridesEmpty(t *testing.T) {
	tmpl := llmagent.New("subagent", llmagent.WithModel(&dynRecordingModel{name: "m"}))
	at := NewDynamicTool(WithTemplateAgent(tmpl))
	got, patch, warns := at.selectDynamicSkills(context.Background(), nil, dynamicSpec{})
	require.True(t, patch, "template configured: must override with empty repo")
	require.Nil(t, got)
	require.Empty(t, warns)
}

// TestSelectDynamicSkills_NoTemplateNoRepoSkipsPatch verifies that with neither
// a template nor a boundary repository the skill surface is left untouched.
func TestSelectDynamicSkills_NoTemplateNoRepoSkipsPatch(t *testing.T) {
	at := NewDynamicTool()
	got, patch, warns := at.selectDynamicSkills(context.Background(), nil, dynamicSpec{})
	require.False(t, patch)
	require.Nil(t, got)
	require.Empty(t, warns)
}

// TestSelectDynamicSkills_SelectionWarnsUnknownAndMissingExecutor covers the
// model-selection path: unknown skills are dropped with a warning, and a
// missing code executor is surfaced as a (best-effort) warning.
func TestSelectDynamicSkills_SelectionWarnsUnknownAndMissingExecutor(t *testing.T) {
	repo := newDynTestSkillRepo(t, "alpha")
	at := NewDynamicTool(WithExposeSkillSelection(true), WithCapabilitySkills(repo))
	spec := dynamicSpec{skillsProvided: true, skills: []string{"alpha", "ghost"}}
	got, patch, warns := at.selectDynamicSkills(context.Background(), nil, spec)
	require.True(t, patch)
	require.NotNil(t, got)
	require.Len(t, warns, 2)
	require.Contains(t, warns[0], "ghost")
	require.Contains(t, warns[1], "code executor")
}

// TestSelectDynamicSkills_SelectionWithExecutorNoExecWarning verifies that a
// run-scoped executor on the parent invocation suppresses the executor warning.
func TestSelectDynamicSkills_SelectionWithExecutorNoExecWarning(t *testing.T) {
	repo := newDynTestSkillRepo(t, "alpha")
	at := NewDynamicTool(WithExposeSkillSelection(true), WithCapabilitySkills(repo))
	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(agent.WithInvocationSession(sess))
	parent.RunOptions.CodeExecutor = fakeDynCodeExecutor{}
	spec := dynamicSpec{skillsProvided: true, skills: []string{"alpha"}}
	got, patch, warns := at.selectDynamicSkills(context.Background(), parent, spec)
	require.True(t, patch)
	require.NotNil(t, got)
	require.Empty(t, warns)
}

// TestSelectDynamicSkills_SelectionIgnoredNoRepo covers the model-selection
// path when no boundary repository is available: the selection is dropped with
// a warning. A template call must still override skills with none (patch=true)
// so the template agent's own skills cannot leak past the dynamic boundary,
// while a non-template call leaves the parent's skill surface untouched.
func TestSelectDynamicSkills_SelectionIgnoredNoRepo(t *testing.T) {
	spec := dynamicSpec{skillsProvided: true, skills: []string{"alpha"}}

	// With a template: override with an empty repo even though selection is
	// ignored, otherwise the template's own skills would leak to the child.
	tmpl := llmagent.New("subagent", llmagent.WithModel(&dynRecordingModel{name: "m"}))
	atTmpl := NewDynamicTool(WithExposeSkillSelection(true), WithTemplateAgent(tmpl))
	got, patch, warns := atTmpl.selectDynamicSkills(context.Background(), nil, spec)
	require.True(t, patch, "template configured: must override with empty repo")
	require.Nil(t, got)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "no skill repository")

	// Without a template: leave the base (== parent) skill surface untouched.
	atNoTmpl := NewDynamicTool(WithExposeSkillSelection(true))
	got, patch, warns = atNoTmpl.selectDynamicSkills(context.Background(), nil, spec)
	require.False(t, patch, "no template: leave parent skill surface untouched")
	require.Nil(t, got)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "no skill repository")
}

// TestNewDynamicTool_StreamableCall_ForwardsChildResponse exercises the
// streaming entry point (streamDynamic): the dynamic child runs and its
// response is forwarded to the parent as stream chunks, mirroring the Call
// path (same capability boundary, surfaced via the shared sub-invocation).
func TestNewDynamicTool_StreamableCall_ForwardsChildResponse(t *testing.T) {
	recModel := &dynRecordingModel{name: "main", response: "streamed-ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	reader, err := at.StreamableCall(ctx, []byte(`{"request":"do something"}`))
	require.NoError(t, err)
	defer reader.Close()

	var sb strings.Builder
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		switch c := chunk.Content.(type) {
		case *event.Event:
			require.Nil(t, c.Error)
			if c.Response != nil && len(c.Response.Choices) > 0 {
				sb.WriteString(c.Response.Choices[0].Message.Content)
			}
		case string:
			sb.WriteString(c)
		}
	}
	require.Contains(t, sb.String(), "streamed-ok")
	require.NotEmpty(t, recModel.snapshot(), "stream path must actually run the child")
}

// TestNewDynamicTool_StreamableCall_BuildErrorSurfaced verifies the streaming
// error path: a request that fails to build a sub-invocation surfaces the error
// on the stream (as a chunk) instead of panicking or silently dropping it.
func TestNewDynamicTool_StreamableCall_BuildErrorSurfaced(t *testing.T) {
	recModel := &dynRecordingModel{name: "main"}
	main := llmagent.New("main", llmagent.WithModel(recModel))
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// A blank request fails buildDynamicSubInvocation.
	reader, err := at.StreamableCall(ctx, []byte(`{"request":"  "}`))
	require.NoError(t, err)
	defer reader.Close()

	var sb strings.Builder
	for {
		chunk, recvErr := reader.Recv()
		if recvErr == io.EOF {
			break
		}
		require.NoError(t, recvErr)
		switch c := chunk.Content.(type) {
		case string:
			sb.WriteString(c)
		case *event.Event:
			if c.Error != nil {
				sb.WriteString(c.Error.Message)
			}
		}
	}
	require.Contains(t, sb.String(), "dynamic sub-agent error")
	require.Empty(t, recModel.snapshot(), "no child should run for an invalid request")
}

// --- Fix 1: child surface = parent's CURRENT effective user surface --------

// TestNewDynamicTool_Integration_AppliesParentToolFilter ensures the child
// surface honors the parent run's ToolFilter (a tool hidden this turn must not
// reappear in the child).
func TestNewDynamicTool_Integration_AppliesParentToolFilter(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{
			newDynTestTool("tool_a"),
			newDynTestTool("tool_b"),
		}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	parent.RunOptions.ToolFilter = func(_ context.Context, tl tool.Tool) bool {
		return tl.Declaration().Name != "tool_b"
	}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a"}, seen[0],
		"child surface must reflect the parent's run-scoped ToolFilter")
}

// TestNewDynamicTool_Integration_IncludesRunOptionAdditionalTools ensures the
// child surface includes tools the parent run temporarily appended via
// RunOptions.AdditionalTools.
func TestNewDynamicTool_Integration_IncludesRunOptionAdditionalTools(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	parent.RunOptions.AdditionalTools = []tool.Tool{newDynTestTool("extra_tool")}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"extra_tool", "tool_a"}, seen[0],
		"child surface must include run-scoped AdditionalTools")
}

// TestNewDynamicTool_Integration_ModelSelectionDropsParentAdditionalTool is the
// fix 1 regression: when the model narrows the selection, a parent
// RunOptions.AdditionalTools tool the model did NOT pick must not be re-appended
// to the child by the child flow. The surface patch is the only authority.
func TestNewDynamicTool_Integration_ModelSelectionDropsParentAdditionalTool(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	parent.RunOptions.AdditionalTools = []tool.Tool{newDynTestTool("extra_tool")}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	// Model selects only tool_a; extra_tool was offered but not chosen.
	_, err := at.Call(ctx, []byte(`{"request":"go","tools":["tool_a"]}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a"}, seen[0],
		"a parent AdditionalTool the model did not select must not leak into the child")
}

// TestNewDynamicTool_Integration_ExcludesParentExternalTools is the fix 2
// regression: caller-executed external tools declared on the parent run must
// not be exposed to (or executable by) the child.
func TestNewDynamicTool_Integration_ExcludesParentExternalTools(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New(
		"main",
		llmagent.WithModel(recModel),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool()

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	parent.RunOptions.ExternalTools = []tool.Tool{newDynTestTool("ext_tool")}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"tool_a"}, seen[0],
		"external (caller-executed) tools must not be exposed to the child")
}

// TestNewDynamicTool_Integration_SuppressesFrameworkTransfer is the fix 3
// regression: even when the base/template agent has sub-agents of its own, the
// child must not be offered transfer_to_agent.
func TestNewDynamicTool_Integration_SuppressesFrameworkTransfer(t *testing.T) {
	templateModel := &dynRecordingModel{name: "template", response: "child-done"}
	leaf := llmagent.New("leaf", llmagent.WithModel(&dynRecordingModel{name: "leaf"}))
	// Template has a sub-agent, so the framework would normally auto-add
	// transfer_to_agent to its tool surface.
	subTemplate := llmagent.New(
		"subagent",
		llmagent.WithModel(templateModel),
		llmagent.WithSubAgents([]agent.Agent{leaf}),
	)
	main := llmagent.New(
		"main",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	at := NewDynamicTool(WithTemplateAgent(subTemplate))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)
	require.Equal(t, "child-done", got)

	seen := templateModel.snapshot()
	require.Len(t, seen, 1)
	require.NotContains(t, seen[0], transfer.TransferToolName,
		"the child must not be offered framework transfer_to_agent")
	require.Equal(t, []string{"tool_a"}, seen[0])
}

// TestNewDynamicTool_Integration_TemplateModelBoundary is the fix 4 regression:
// when a template agent defines the boundary, a parent run-scoped model override
// must not leak into the child; the template's own model runs it.
func TestNewDynamicTool_Integration_TemplateModelBoundary(t *testing.T) {
	overrideModel := &dynRecordingModel{name: "override", response: "override-ran"}
	templateModel := &dynRecordingModel{name: "template", response: "child-done"}

	main := llmagent.New(
		"main",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	subTemplate := llmagent.New(
		"subagent",
		llmagent.WithModel(templateModel),
	)
	at := NewDynamicTool(WithTemplateAgent(subTemplate))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	// Parent run pins a model override; it must not cross the template boundary.
	parent.RunOptions.Model = overrideModel
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)
	require.Equal(t, "child-done", got)
	require.Empty(t, overrideModel.snapshot(),
		"parent RunOptions.Model must not override the template boundary")
	require.Len(t, templateModel.snapshot(), 1,
		"the template's own model must run the child")
}

// TestChildCodeExecutor_TemplateIgnoresParentRunOption verifies that, with a
// template agent, the template's own executor is authoritative and a parent
// run-scoped executor is ignored (it is cleared on the child clone).
func TestChildCodeExecutor_TemplateIgnoresParentRunOption(t *testing.T) {
	tmpl := llmagent.New(
		"subagent",
		llmagent.WithModel(&dynRecordingModel{name: "m"}),
		llmagent.WithCodeExecutor(taggedDynCodeExecutor{tag: "template"}),
	)
	at := NewDynamicTool(WithTemplateAgent(tmpl))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(agent.WithInvocationSession(sess))
	parent.RunOptions.CodeExecutor = taggedDynCodeExecutor{tag: "parent"}

	got := at.childCodeExecutor(context.Background(), parent)
	tagged, ok := got.(taggedDynCodeExecutor)
	require.True(t, ok)
	require.Equal(t, "template", tagged.tag,
		"with a template, the template's executor wins over a parent run-scoped one")
}

func TestChildCodeExecutor_TemplatePassesNonNilInvocation(t *testing.T) {
	base := llmagent.New(
		"subagent",
		llmagent.WithModel(&dynRecordingModel{name: "m"}),
	)
	tmpl := &invocationCheckingExecutorAgent{
		Agent: base,
		exec:  taggedDynCodeExecutor{tag: "template"},
	}
	at := NewDynamicTool(WithTemplateAgent(tmpl))

	got := at.childCodeExecutor(context.Background(), nil)

	tagged, ok := got.(taggedDynCodeExecutor)
	require.True(t, ok)
	require.Equal(t, "template", tagged.tag)
	require.True(t, tmpl.sawInv)
	require.True(t, tmpl.sawAgent)
	require.False(t, tmpl.sawParent)
}

// --- RunOptions sanitization (fixes 1/2/3 of this round) -------------------

func fullyPopulatedChildRunOptions() agent.RunOptions {
	return agent.RunOptions{
		AdditionalTools:   stubTools("extra"),
		ExternalTools:     stubTools("ext"),
		ExternalToolNames: map[string]bool{"ext": true},
		ToolFilter:        func(context.Context, tool.Tool) bool { return true },
		Model:             &dynRecordingModel{name: "m"},
		ModelName:         "mname",
		ModelSelector: func(context.Context, *agent.Invocation) (model.Model, error) {
			return nil, nil
		},
		Instruction:         "inst",
		GlobalInstruction:   "ginst",
		CodeExecutor:        fakeDynCodeExecutor{},
		ToolExecutionFilter: func(context.Context, tool.Tool) bool { return true },
		ToolPermissionPolicy: tool.PermissionPolicyFunc(
			func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.AllowPermission(), nil
			},
		),
	}
}

// TestSanitizeChildRunOptions_TemplateBoundaryClearsAll verifies that with a
// template boundary every run-scoped tool input AND every model/prompt/
// execution-policy override inherited from the parent clone is cleared, so the
// template is a true strong boundary.
func TestSanitizeChildRunOptions_TemplateBoundaryClearsAll(t *testing.T) {
	at := NewDynamicTool()
	runOpts := fullyPopulatedChildRunOptions()
	at.sanitizeChildRunOptions(&runOpts, true)

	// Tool surface: patch is the single source of truth.
	require.Nil(t, runOpts.AdditionalTools)
	require.Nil(t, runOpts.ExternalTools)
	require.Nil(t, runOpts.ExternalToolNames)
	require.Nil(t, runOpts.ToolFilter)
	// Model / prompt boundary.
	require.Nil(t, runOpts.Model)
	require.Empty(t, runOpts.ModelName)
	require.Nil(t, runOpts.ModelSelector)
	require.Empty(t, runOpts.Instruction)
	require.Empty(t, runOpts.GlobalInstruction)
	// Execution boundary.
	require.Nil(t, runOpts.CodeExecutor)
	require.Nil(t, runOpts.ToolExecutionFilter)
	require.Nil(t, runOpts.ToolPermissionPolicy)
}

// TestSanitizeChildRunOptions_NoTemplateKeepsBoundaryFields verifies that
// without a template the child IS the parent agent, so only the tool-surface
// inputs are cleared; model/prompt/execution overrides are inherited.
func TestSanitizeChildRunOptions_NoTemplateKeepsBoundaryFields(t *testing.T) {
	at := NewDynamicTool()
	runOpts := fullyPopulatedChildRunOptions()
	at.sanitizeChildRunOptions(&runOpts, false)

	// Tool surface is always cleared (patch is authoritative; parent filter
	// already applied while deriving the candidate surface).
	require.Nil(t, runOpts.AdditionalTools)
	require.Nil(t, runOpts.ExternalTools)
	require.Nil(t, runOpts.ExternalToolNames)
	require.Nil(t, runOpts.ToolFilter)
	// Everything else is inherited unchanged.
	require.NotNil(t, runOpts.Model)
	require.Equal(t, "mname", runOpts.ModelName)
	require.NotNil(t, runOpts.ModelSelector)
	require.Equal(t, "inst", runOpts.Instruction)
	require.Equal(t, "ginst", runOpts.GlobalInstruction)
	require.NotNil(t, runOpts.CodeExecutor)
	require.NotNil(t, runOpts.ToolExecutionFilter)
	require.NotNil(t, runOpts.ToolPermissionPolicy)
}

// TestNewDynamicTool_Integration_CapabilityToolsBypassParentFilter is this
// round's fix 1 regression: a fixed WithCapabilityTools surface must not be
// re-filtered by the parent run's ToolFilter in the child flow.
func TestNewDynamicTool_Integration_CapabilityToolsBypassParentFilter(t *testing.T) {
	recModel := &dynRecordingModel{name: "rec", response: "ok"}
	main := llmagent.New("main", llmagent.WithModel(recModel))
	// cap_tool is a code-defined capability tool; it never passes through the
	// parent ToolFilter when deriving the surface.
	at := NewDynamicTool(WithCapabilityTools([]tool.Tool{newDynTestTool("cap_tool")}))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	// A parent ToolFilter that would reject cap_tool must not reach the child.
	parent.RunOptions.ToolFilter = func(_ context.Context, tl tool.Tool) bool {
		return tl.Declaration().Name != "cap_tool"
	}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)

	seen := recModel.snapshot()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"cap_tool"}, seen[0],
		"a code-defined capability tool must survive in the child even when the "+
			"parent ToolFilter would reject it")
}

// TestNewDynamicTool_Integration_TemplateIgnoresParentModelSelector is this
// round's fix 2 regression: a parent run-scoped ModelSelector must not cross
// the template boundary and re-pick the child's model.
func TestNewDynamicTool_Integration_TemplateIgnoresParentModelSelector(t *testing.T) {
	overrideModel := &dynRecordingModel{name: "override", response: "override-ran"}
	templateModel := &dynRecordingModel{name: "template", response: "child-done"}

	main := llmagent.New(
		"main",
		llmagent.WithModel(&dynRecordingModel{name: "parent"}),
		llmagent.WithTools([]tool.Tool{newDynTestTool("tool_a")}),
	)
	subTemplate := llmagent.New("subagent", llmagent.WithModel(templateModel))
	at := NewDynamicTool(WithTemplateAgent(subTemplate))

	sess := session.NewSession("app", "user", "session")
	parent := agent.NewInvocation(
		agent.WithInvocationAgent(main),
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("main"),
	)
	parent.RunOptions.ModelSelector = func(
		context.Context, *agent.Invocation,
	) (model.Model, error) {
		return overrideModel, nil
	}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	got, err := at.Call(ctx, []byte(`{"request":"go"}`))
	require.NoError(t, err)
	require.Equal(t, "child-done", got)
	require.Empty(t, overrideModel.snapshot(),
		"parent RunOptions.ModelSelector must not override the template boundary")
	require.Len(t, templateModel.snapshot(), 1)
}
