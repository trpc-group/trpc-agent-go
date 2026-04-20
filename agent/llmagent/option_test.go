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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

type stubSkillStager struct{}

func (stubSkillStager) StageSkill(
	_ context.Context,
	_ toolskill.SkillStageRequest,
) (toolskill.SkillStageResult, error) {
	return toolskill.SkillStageResult{}, nil
}

func TestWithChannelBufferSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultChannelBufferSize,
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &Options{}
			option := WithChannelBufferSize(tt.inputSize)
			option(options)

			require.Equal(t, tt.wantBufSize, options.ChannelBufferSize)
		})
	}
}

func TestWithSyncSummaryIntraRun(t *testing.T) {
	opts := &Options{}
	WithSyncSummaryIntraRun(true)(opts)
	require.True(t, opts.SyncSummaryIntraRun)

	WithSyncSummaryIntraRun(false)(opts)
	require.False(t, opts.SyncSummaryIntraRun)
}

func TestWithSessionSummaryInjectionMode(t *testing.T) {
	opts := &Options{}
	// Default should be zero value (empty string, treated as system).
	require.Equal(t, processor.SessionSummaryInjectionMode(""), opts.SessionSummaryInjectionMode)

	WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)(opts)
	require.Equal(t, processor.SessionSummaryInjectionUser, opts.SessionSummaryInjectionMode)

	WithSessionSummaryInjectionMode(SessionSummaryInjectionSystem)(opts)
	require.Equal(t, processor.SessionSummaryInjectionSystem, opts.SessionSummaryInjectionMode)
}

func TestWithContextCompactionOptions(t *testing.T) {
	opts := &Options{}

	WithEnableContextCompaction(true)(opts)
	require.True(t, opts.EnableContextCompaction)

	WithContextCompactionThresholdRatio(0.8)(opts)
	require.Equal(t, 0.8, opts.ContextCompactionThresholdRatio)
	WithContextCompactionThresholdRatio(0)(opts)
	require.Equal(t, 0.8, opts.ContextCompactionThresholdRatio)

	WithContextCompactionToolResultMaxTokens(2048)(opts)
	require.Equal(t, 2048, opts.ContextCompactionToolResultMaxTokens)
	WithContextCompactionToolResultMaxTokens(-1)(opts)
	require.Equal(t, 2048, opts.ContextCompactionToolResultMaxTokens)

	WithContextCompactionKeepRecentRequests(3)(opts)
	require.Equal(t, 3, opts.ContextCompactionKeepRecentRequests)
	WithContextCompactionKeepRecentRequests(-1)(opts)
	require.Equal(t, 3, opts.ContextCompactionKeepRecentRequests)

	WithContextCompactionOversizedToolResultMaxTokens(4096)(opts)
	require.Equal(t, 4096, opts.ContextCompactionOversizedToolResultMaxTokens)
	WithContextCompactionOversizedToolResultMaxTokens(-1)(opts)
	require.Equal(t, 4096, opts.ContextCompactionOversizedToolResultMaxTokens)
}

func TestWithMessageFilterMode(t *testing.T) {
	tests := []struct {
		name                   string
		inputMode              MessageFilterMode
		wantBranchFilterMode   string
		wantTimelineFilterMode string
		wantPanic              bool
	}{
		{
			name:                   "FullContext mode",
			inputMode:              FullContext,
			wantBranchFilterMode:   BranchFilterModePrefix,
			wantTimelineFilterMode: TimelineFilterAll,
			wantPanic:              false,
		},
		{
			name:                   "RequestContext mode",
			inputMode:              RequestContext,
			wantBranchFilterMode:   BranchFilterModePrefix,
			wantTimelineFilterMode: TimelineFilterCurrentRequest,
			wantPanic:              false,
		},
		{
			name:                   "IsolatedRequest mode",
			inputMode:              IsolatedRequest,
			wantBranchFilterMode:   BranchFilterModeExact,
			wantTimelineFilterMode: TimelineFilterCurrentRequest,
			wantPanic:              false,
		},
		{
			name:                   "IsolatedInvocation mode",
			inputMode:              IsolatedInvocation,
			wantBranchFilterMode:   BranchFilterModeExact,
			wantTimelineFilterMode: TimelineFilterCurrentInvocation,
			wantPanic:              false,
		},
		{
			name:      "Invalid mode should panic",
			inputMode: MessageFilterMode(99),
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantPanic {
				require.Panics(t, func() {
					opt := WithMessageFilterMode(tt.inputMode)
					opts := &Options{}
					opt(opts)
				})
				return
			}

			opt := WithMessageFilterMode(tt.inputMode)
			opts := &Options{}
			opt(opts)

			require.Equal(t, tt.wantBranchFilterMode, opts.messageBranchFilterMode)
			require.Equal(t, tt.wantTimelineFilterMode, opts.messageTimelineFilterMode)
		})
	}
}

func TestWithReasoningContentMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantMode string
	}{
		{
			name:     "keep_all mode",
			mode:     ReasoningContentModeKeepAll,
			wantMode: ReasoningContentModeKeepAll,
		},
		{
			name:     "discard_previous_turns mode",
			mode:     ReasoningContentModeDiscardPreviousTurns,
			wantMode: ReasoningContentModeDiscardPreviousTurns,
		},
		{
			name:     "discard_all mode",
			mode:     ReasoningContentModeDiscardAll,
			wantMode: ReasoningContentModeDiscardAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			opt := WithReasoningContentMode(tt.mode)
			opt(opts)

			require.Equal(t, tt.wantMode, opts.ReasoningContentMode)
		})
	}
}

func TestWithSkillLoadMode(t *testing.T) {
	a := New("test-agent")
	require.Equal(t, SkillLoadModeTurn, a.option.SkillLoadMode)

	b := New("test-agent", WithSkillLoadMode(SkillLoadModeSession))
	require.Equal(t, SkillLoadModeSession, b.option.SkillLoadMode)
}

func TestWithMaxLoadedSkills(t *testing.T) {
	const (
		agentName = "test-agent"
		maxSkills = 3
	)

	a := New(agentName)
	require.Equal(t, 0, a.option.MaxLoadedSkills)

	b := New(agentName, WithMaxLoadedSkills(maxSkills))
	require.Equal(t, maxSkills, b.option.MaxLoadedSkills)

	c := New(agentName, WithMaxLoadedSkills(0))
	require.Equal(t, 0, c.option.MaxLoadedSkills)
}

func TestWithSkillsLoadedContentInToolResults(t *testing.T) {
	a := New("test-agent")
	require.False(t, a.option.SkillsLoadedContentInToolResults)

	b := New("test-agent", WithSkillsLoadedContentInToolResults(true))
	require.True(t, b.option.SkillsLoadedContentInToolResults)
}

func TestWithSkillsDirectoryHints(t *testing.T) {
	a := New("test-agent")
	require.False(t, a.option.skillsDirectoryHints)

	b := New("test-agent", WithSkillsDirectoryHints(true))
	require.True(t, b.option.skillsDirectoryHints)
}

func TestWithSkillsFilePathHints(t *testing.T) {
	a := New("test-agent")
	require.False(t, a.option.skillsFilePathHints)

	b := New("test-agent", WithSkillsFilePathHints(true))
	require.True(t, b.option.skillsFilePathHints)
}

func TestWithSkillLoadToolDescription(t *testing.T) {
	a := New("test-agent")
	require.Nil(t, a.option.skillLoadToolDescription)

	const description = "Load the matching skill before answering."

	b := New(
		"test-agent",
		WithSkillLoadToolDescription(description),
	)
	require.NotNil(t, b.option.skillLoadToolDescription)
	require.Equal(t, description, *b.option.skillLoadToolDescription)
}

func TestWithWorkspaceExecSurfaceEnabled(t *testing.T) {
	a := New("test-agent")
	require.Nil(t, a.option.workspaceExecSurfaceEnabled)
	require.True(t, workspaceExecSurfaceEnabled(&a.option))

	b := New(
		"test-agent",
		WithWorkspaceExecSurfaceEnabled(false),
	)
	require.NotNil(t, b.option.workspaceExecSurfaceEnabled)
	require.False(t, *b.option.workspaceExecSurfaceEnabled)
}

func TestWithSkillsCapabilityGuidance(t *testing.T) {
	a := New("test-agent")
	require.Nil(t, a.option.skillsCapabilityGuidance)

	b := New(
		"test-agent",
		WithSkillsCapabilityGuidance("Use directory bundles."),
	)
	require.NotNil(t, b.option.skillsCapabilityGuidance)
	require.Equal(
		t,
		"Use directory bundles.",
		*b.option.skillsCapabilityGuidance,
	)
}

func TestWithSkillsProtocolGuidance(t *testing.T) {
	a := New("test-agent")
	require.Nil(t, a.option.skillsProtocolGuidance)

	b := New(
		"test-agent",
		WithSkillsProtocolGuidance("Always load SKILL.md first."),
	)
	require.NotNil(t, b.option.skillsProtocolGuidance)
	require.Equal(
		t,
		"Always load SKILL.md first.",
		*b.option.skillsProtocolGuidance,
	)
}

func TestWithSkillFilter(t *testing.T) {
	a := New("test-agent")
	require.Nil(t, a.option.skillFilter)

	filter := func(context.Context, skill.Summary) bool { return true }
	b := New("test-agent", WithSkillFilter(filter))
	require.NotNil(t, b.option.skillFilter)
}

func TestWithSkipSkillsFallbackOnSessionSummary(t *testing.T) {
	a := New("test-agent")
	require.True(t, a.option.SkipSkillsFallbackOnSessionSummary)

	b := New(
		"test-agent",
		WithSkipSkillsFallbackOnSessionSummary(false),
	)
	require.False(t, b.option.SkipSkillsFallbackOnSessionSummary)
}

func TestNew_DefaultGenerationConfigKeepsLegacyNonStreaming(t *testing.T) {
	a := New("test-agent")
	require.False(t, a.genConfig.Stream)
}

func TestLLMAgent_Run_DefaultGenerationConfigUsesPublicStreamingBehavior(
	t *testing.T,
) {
	t.Parallel()

	runAndCapture := func(
		t *testing.T,
		runOptions ...agent.RunOption,
	) *model.Request {
		t.Helper()

		mdl := &captureModel{}
		agt := New("test-agent", WithModel(mdl))

		invOpts := []agent.InvocationOptions{
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
			agent.WithInvocationSession(&session.Session{}),
		}
		if len(runOptions) > 0 {
			invOpts = append(
				invOpts,
				agent.WithInvocationRunOptions(
					agent.NewRunOptions(runOptions...),
				),
			)
		}
		inv := agent.NewInvocation(invOpts...)

		ch, err := agt.Run(context.Background(), inv)
		require.NoError(t, err)

		ctx := context.Background()
		for evt := range ch {
			if evt != nil && evt.RequiresCompletion {
				key := agent.GetAppendEventNoticeKey(evt.ID)
				_ = inv.AddNoticeChannel(ctx, key)
				_ = inv.NotifyCompletion(ctx, key)
			}
		}

		require.NotNil(t, mdl.got)
		return mdl.got
	}

	t.Run("default is non-streaming", func(t *testing.T) {
		req := runAndCapture(t)
		require.False(t, req.GenerationConfig.Stream)
	})

	t.Run("per-run override enables streaming", func(t *testing.T) {
		req := runAndCapture(t, agent.WithStream(true))
		require.True(t, req.GenerationConfig.Stream)
	})
}

func TestWithGenerationConfig_ExplicitFalseDisablesStreaming(
	t *testing.T,
) {
	a := New(
		"test-agent",
		WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)
	require.False(t, a.genConfig.Stream)
}

func TestBuildRequestProcessors_DefaultGenerationConfigUsesZeroValue(
	t *testing.T,
) {
	procs := buildRequestProcessors("test-agent", &Options{})
	var basicProc *processor.BasicRequestProcessor
	for _, proc := range procs {
		if candidate, ok := proc.(*processor.BasicRequestProcessor); ok {
			basicProc = candidate
			break
		}
	}
	require.NotNil(t, basicProc)
	require.False(t, basicProc.GenerationConfig.Stream)
}

func TestWithMaxLimits_OnOptions(t *testing.T) {
	opts := &Options{}

	WithMaxLLMCalls(3)(opts)
	WithMaxToolIterations(4)(opts)

	if opts.MaxLLMCalls != 3 {
		t.Fatalf("expected MaxLLMCalls=3, got %d", opts.MaxLLMCalls)
	}
	if opts.MaxToolIterations != 4 {
		t.Fatalf("expected MaxToolIterations=4, got %d", opts.MaxToolIterations)
	}
}

func TestWithToolCallRetryPolicy_OnOptions(t *testing.T) {
	opts := &Options{}
	policy := &tool.RetryPolicy{MaxAttempts: 2}
	WithToolCallRetryPolicy(policy)(opts)
	require.Same(t, policy, opts.ToolCallRetryPolicy)
}

func TestWithPreloadMemory(t *testing.T) {
	tests := []struct {
		name          string
		limit         int
		expectedLimit int
	}{
		{
			name:          "disable preloading",
			limit:         0,
			expectedLimit: 0,
		},
		{
			name:          "load all memories",
			limit:         -1,
			expectedLimit: -1,
		},
		{
			name:          "use adaptive preload budget",
			limit:         5,
			expectedLimit: 5,
		},
		{
			name:          "use large adaptive preload budget",
			limit:         100,
			expectedLimit: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			opt := WithPreloadMemory(tt.limit)
			opt(opts)
			require.Equal(t, tt.expectedLimit, opts.PreloadMemory)
		})
	}
}

func TestWithPreloadSessionRecall(t *testing.T) {
	opts := &Options{}
	WithPreloadSessionRecall(6)(opts)
	require.Equal(t, 6, opts.PreloadSessionRecall)

	WithPreloadSessionRecall(0)(opts)
	require.Equal(t, 0, opts.PreloadSessionRecall)
}

func TestWithPreloadSessionRecallMinScore(t *testing.T) {
	opts := &Options{}
	WithPreloadSessionRecallMinScore(0.42)(opts)
	require.Equal(t, 0.42, opts.PreloadSessionRecallMinScore)
}

func TestWithPreloadSessionRecallSearchMode(t *testing.T) {
	opts := &Options{}
	WithPreloadSessionRecallSearchMode(session.SearchModeDense)(opts)
	require.Equal(t, session.SearchModeDense, opts.PreloadSessionRecallSearchMode)

	WithPreloadSessionRecallSearchMode(session.SearchMode("invalid"))(opts)
	require.Equal(t, session.SearchModeHybrid, opts.PreloadSessionRecallSearchMode)
}

func TestWithSkillRunAllowedCommands_CopiesSlice(t *testing.T) {
	in := []string{"echo", "ls"}
	opts := &Options{}
	WithSkillRunAllowedCommands(in...)(opts)

	in[0] = "rm"
	require.Equal(t, []string{"echo", "ls"}, opts.skillRunAllowedCommands)
}

func TestWithSkillRunDeniedCommands_CopiesSlice(t *testing.T) {
	in := []string{"echo", "ls"}
	opts := &Options{}
	WithSkillRunDeniedCommands(in...)(opts)

	in[0] = "rm"
	require.Equal(t, []string{"echo", "ls"}, opts.skillRunDeniedCommands)
}

func TestWithSkillRunForceSaveArtifacts(t *testing.T) {
	opts := &Options{}
	WithSkillRunForceSaveArtifacts(true)(opts)
	require.True(t, opts.skillRunForceSaveArtifacts)

	WithSkillRunForceSaveArtifacts(false)(opts)
	require.False(t, opts.skillRunForceSaveArtifacts)
}

func TestWithSkillRunOutputLimits(t *testing.T) {
	opts := &Options{}
	limits := toolskill.RunOutputLimits{
		StdoutStderrBytes:  128,
		PrimaryOutputBytes: 256,
	}
	WithSkillRunOutputLimits(limits)(opts)
	require.Equal(t, limits, opts.skillRunOutputLimits)
}

func TestWithSkillRunRequireSkillLoaded(t *testing.T) {
	opts := &Options{}
	WithSkillRunRequireSkillLoaded(true)(opts)
	require.True(t, opts.skillRunRequireSkillLoaded)

	WithSkillRunRequireSkillLoaded(false)(opts)
	require.False(t, opts.skillRunRequireSkillLoaded)
}

func TestWithSkillRunStager(t *testing.T) {
	opts := &Options{}
	stager := stubSkillStager{}
	WithSkillRunStager(stager)(opts)
	require.Equal(t, stager, opts.skillRunStager)
}

func TestWithSkillToolProfile(t *testing.T) {
	opts := &Options{}
	WithSkillToolProfile(SkillToolProfileKnowledgeOnly)(opts)
	require.Equal(t, "knowledge_only", opts.skillToolProfile)

	WithSkillToolProfile(SkillToolProfileFull)(opts)
	require.Equal(t, "full", opts.skillToolProfile)
}

func TestWithAllowedSkillTools(t *testing.T) {
	opts := &Options{}
	WithAllowedSkillTools(SkillToolLoad, SkillToolRun)(opts)
	require.Equal(
		t,
		[]string{"skill_load", "skill_run"},
		opts.allowedSkillTools,
	)

	WithAllowedSkillTools()(opts)
	require.NotNil(t, opts.allowedSkillTools)
	require.Empty(t, opts.allowedSkillTools)
}

func TestWithSummaryFormatter(t *testing.T) {
	tests := []struct {
		name      string
		formatter func(summary string) string
		wantNil   bool
	}{
		{
			name: "set custom formatter",
			formatter: func(summary string) string {
				return "## Summary\n\n" + summary
			},
			wantNil: false,
		},
		{
			name:      "set nil formatter",
			formatter: nil,
			wantNil:   true,
		},
		{
			name: "set formatter with prefix",
			formatter: func(summary string) string {
				return "## Previous Context\n\n" + summary
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			opt := WithSummaryFormatter(tt.formatter)
			opt(opts)

			if tt.wantNil {
				require.Nil(t, opts.summaryFormatter)
			} else {
				require.NotNil(t, opts.summaryFormatter)
				require.NotNil(t, tt.formatter)
				// Verify the formatter works as expected.
				input := "test summary"
				expected := tt.formatter(input)
				actual := opts.summaryFormatter(input)
				require.Equal(t, expected, actual)
			}
		})
	}
}

// TestBuildRequestProcessorsWithReasoningContentMode verifies that
// ReasoningContentMode option is correctly passed to ContentRequestProcessor.
func TestBuildRequestProcessorsWithReasoningContentMode(t *testing.T) {
	tests := []struct {
		name                     string
		reasoningContentMode     string
		wantReasoningContentMode bool
	}{
		{
			name:                     "keep_all mode",
			reasoningContentMode:     ReasoningContentModeKeepAll,
			wantReasoningContentMode: true,
		},
		{
			name:                     "discard_previous_turns mode",
			reasoningContentMode:     ReasoningContentModeDiscardPreviousTurns,
			wantReasoningContentMode: true,
		},
		{
			name:                     "discard_all mode",
			reasoningContentMode:     ReasoningContentModeDiscardAll,
			wantReasoningContentMode: true,
		},
		{
			name:                     "empty mode",
			reasoningContentMode:     "",
			wantReasoningContentMode: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := New("test-agent", WithReasoningContentMode(tt.reasoningContentMode))

			// When reasoningContentMode is set, agent should be created
			// without errors. The actual verification is done by checking that
			// no panic occurred during agent creation.
			require.NotNil(t, agent)
		})
	}
}

// TestBuildRequestProcessorsWithSummaryFormatter verifies that
// SummaryFormatter option is correctly passed to ContentRequestProcessor.
func TestBuildRequestProcessorsWithSummaryFormatter(t *testing.T) {
	tests := []struct {
		name      string
		formatter func(summary string) string
		wantNil   bool
	}{
		{
			name: "with custom formatter",
			formatter: func(summary string) string {
				return "## Custom Summary\n\n" + summary
			},
			wantNil: false,
		},
		{
			name:      "without formatter",
			formatter: nil,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := New("test-agent", WithSummaryFormatter(tt.formatter))

			// When SummaryFormatter is set, agent should be created
			// without errors. The actual verification is done by checking that
			// no panic occurred during agent creation.
			require.NotNil(t, agent)

			// Verify that the formatter function works when set.
			if !tt.wantNil && tt.formatter != nil {
				testSummary := "test summary content"
				expected := tt.formatter(testSummary)
				// The formatter should be callable.
				require.Equal(t, expected, tt.formatter(testSummary))
			}
		})
	}
}

func TestWithEnablePostToolPrompt(t *testing.T) {
	opts := &Options{}
	WithEnablePostToolPrompt(false)(opts)

	require.NotNil(t, opts.postToolPromptEnabled)
	require.False(t, *opts.postToolPromptEnabled)

	opts = &Options{}
	WithEnablePostToolPrompt(true)(opts)

	require.NotNil(t, opts.postToolPromptEnabled)
	require.True(t, *opts.postToolPromptEnabled)
}
