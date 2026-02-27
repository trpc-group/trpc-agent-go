//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
)

const (
	defaultChannelBufferSize = 256

	// BranchFilterModePrefix Prefix matching pattern
	BranchFilterModePrefix = processor.BranchFilterModePrefix
	// BranchFilterModeAll include all
	BranchFilterModeAll = processor.BranchFilterModeAll
	// BranchFilterModeExact exact match
	BranchFilterModeExact = processor.BranchFilterModeExact

	// TimelineFilterAll includes all historical message records
	// Suitable for scenarios requiring full conversation context
	TimelineFilterAll = processor.TimelineFilterAll
	// TimelineFilterCurrentRequest only includes messages within the current request cycle
	// Filters out previous historical records, keeping only messages related to this request
	TimelineFilterCurrentRequest = processor.TimelineFilterCurrentRequest
	// TimelineFilterCurrentInvocation only includes messages within the current invocation session
	// Suitable for scenarios requiring isolation between different invocation cycles in long-running sessions
	TimelineFilterCurrentInvocation = processor.TimelineFilterCurrentInvocation

	// ReasoningContentModeKeepAll keeps all reasoning_content in messages.
	// Use this for debugging or when you need to retain thinking chains.
	ReasoningContentModeKeepAll = processor.ReasoningContentModeKeepAll
	// ReasoningContentModeDiscardPreviousTurns discards reasoning_content from previous
	// request turns while keeping the current request's reasoning_content.
	// This is the default mode, recommended for DeepSeek thinking mode.
	ReasoningContentModeDiscardPreviousTurns = processor.ReasoningContentModeDiscardPreviousTurns
	// ReasoningContentModeDiscardAll discards all reasoning_content from all messages.
	ReasoningContentModeDiscardAll = processor.ReasoningContentModeDiscardAll
)

// MessageFilterMode is the mode for filtering messages.
type MessageFilterMode int

const (
	// FullContext Includes all messages with prefix matching (including historical messages).
	// equivalent to TimelineFilterAll + BranchFilterModePrefix.
	FullContext MessageFilterMode = iota
	// RequestContext includes only messages from the current request cycle that match the branch prefix.
	// equivalent to TimelineFilterCurrentRequest + BranchFilterModePrefix.
	RequestContext
	// IsolatedRequest includes only messages from the current request cycle that exactly match the branch.
	// equivalent to TimelineFilterCurrentRequest + BranchFilterModeExact.
	IsolatedRequest
	// IsolatedInvocation includes only messages from current invocation session that exactly match the branch,
	// equivalent to TimelineFilterCurrentInvocation + BranchFilterModeExact.
	IsolatedInvocation
)

// Option is a function that configures a GraphAgent.
type Option func(*Options)

// Options contains configuration options for creating a GraphAgent.
type Options struct {
	// Description is a description of the agent.
	Description string
	// SubAgents is the list of sub-agents available to this agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.Callbacks
	// InitialState is the initial state for graph execution.
	InitialState graph.State
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// MaxConcurrency is the maximum number of graph tasks executed in
	// parallel. When <= 0, GraphAgent uses the Executor default.
	MaxConcurrency int
	// CheckpointSaver is the checkpoint saver for the executor.
	CheckpointSaver graph.CheckpointSaver
	// ExecutionEngine controls how the graph is scheduled and executed.
	// When empty, GraphAgent uses the Executor default (BSP).
	ExecutionEngine graph.ExecutionEngine

	// AddSessionSummary controls whether to prepend the current branch summary
	// as a system message when available.
	AddSessionSummary bool
	// MaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
	// When 0 (default), no limit is applied.
	MaxHistoryRuns int
	// summaryFormatter allows custom formatting of session summary content.
	// When nil (default), uses default formatSummaryContent function.
	summaryFormatter func(summary string) string

	// MessageTimelineFilterMode is the message timeline filter mode.
	messageTimelineFilterMode string
	// MessageBranchFilterMode is the message branch filter mode.
	messageBranchFilterMode string
	// ReasoningContentMode controls how reasoning_content is handled in multi-turn
	// conversations. This is useful for models like DeepSeek that output reasoning_content
	// in thinking mode.
	ReasoningContentMode string

	// ExecutorOptions allows passing additional executor options directly.
	// These options are applied after the mapped options (ChannelBufferSize,
	// MaxConcurrency, CheckpointSaver), so they can override those settings if needed.
	// This provides access to advanced executor configurations like MaxSteps,
	// StepTimeout, NodeTimeout, CheckpointSaveTimeout, and DefaultRetryPolicy.
	ExecutorOptions []graph.ExecutorOption
}

var (
	defaultOptions = Options{ChannelBufferSize: defaultChannelBufferSize}
)

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.Callbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
	}
}

// WithInitialState sets the initial state for graph execution.
func WithInitialState(state graph.State) Option {
	return func(opts *Options) {
		opts.InitialState = state
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		if size < 0 {
			size = defaultChannelBufferSize
		}
		opts.ChannelBufferSize = size
	}
}

// WithMaxConcurrency sets the maximum number of graph tasks executed in
// parallel.
//
// When max <= 0, GraphAgent uses the Executor default.
func WithMaxConcurrency(max int) Option {
	return func(opts *Options) {
		opts.MaxConcurrency = max
	}
}

// WithSubAgents sets the list of sub-agents available to this agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithCheckpointSaver sets the checkpoint saver for the executor.
func WithCheckpointSaver(saver graph.CheckpointSaver) Option {
	return func(opts *Options) {
		opts.CheckpointSaver = saver
	}
}

// WithExecutionEngine sets the execution engine for graph scheduling.
//
// When not set, GraphAgent uses the Executor default (BSP).
func WithExecutionEngine(engine graph.ExecutionEngine) Option {
	return func(opts *Options) {
		opts.ExecutionEngine = engine
	}
}

// WithAddSessionSummary controls whether to prepend the current-branch summary
// as a system message when available (default: false).
func WithAddSessionSummary(addSummary bool) Option {
	return func(opts *Options) {
		opts.AddSessionSummary = addSummary
	}
}

// WithMaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
// When 0 (default), no limit is applied.
func WithMaxHistoryRuns(maxRuns int) Option {
	return func(opts *Options) {
		opts.MaxHistoryRuns = maxRuns
	}
}

// WithMessageTimelineFilterMode sets the message timeline filter mode.
func WithMessageTimelineFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageTimelineFilterMode = mode
	}
}

// WithMessageBranchFilterMode sets the message branch filter mode.
func WithMessageBranchFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageBranchFilterMode = mode
	}
}

// WithMessageFilterMode sets the message filter mode.
func WithMessageFilterMode(mode MessageFilterMode) Option {
	return func(opts *Options) {
		switch mode {
		case FullContext:
			opts.messageBranchFilterMode = BranchFilterModePrefix
			opts.messageTimelineFilterMode = TimelineFilterAll
		case RequestContext:
			opts.messageBranchFilterMode = BranchFilterModePrefix
			opts.messageTimelineFilterMode = TimelineFilterCurrentRequest
		case IsolatedRequest:
			opts.messageBranchFilterMode = BranchFilterModeExact
			opts.messageTimelineFilterMode = TimelineFilterCurrentRequest
		case IsolatedInvocation:
			opts.messageBranchFilterMode = BranchFilterModeExact
			opts.messageTimelineFilterMode = TimelineFilterCurrentInvocation
		default:
			panic("invalid option value")
		}
	}
}

// WithReasoningContentMode sets the reasoning content mode for handling reasoning_content
// in multi-turn conversations. This is useful for models like DeepSeek that output
// reasoning_content in thinking mode.
func WithReasoningContentMode(mode string) Option {
	return func(opts *Options) {
		opts.ReasoningContentMode = mode
	}
}

// WithSummaryFormatter sets a custom formatter for session summary content.
// This allows users to customize how summaries are presented to the model.
// Example:
//
//	graphagent.WithSummaryFormatter(func(summary string) string {
//	    return fmt.Sprintf("## Previous Context\n\n%s", summary)
//	})
func WithSummaryFormatter(formatter func(summary string) string) Option {
	return func(opts *Options) {
		opts.summaryFormatter = formatter
	}
}

// WithExecutorOptions allows passing executor options directly to the underlying graph executor.
// These options are applied after the mapped options (ChannelBufferSize, MaxConcurrency,
// CheckpointSaver), so they can override those settings if needed.
//
// This provides access to advanced executor configurations that are not directly exposed
// through GraphAgent options, such as:
//   - graph.WithMaxSteps() - Maximum number of steps for graph execution
//   - graph.WithStepTimeout() - Timeout for each step
//   - graph.WithNodeTimeout() - Timeout for individual node execution
//   - graph.WithCheckpointSaveTimeout() - Timeout for checkpoint save operations
//   - graph.WithDefaultRetryPolicy() - Default retry policies for nodes
//
// Example:
//
//	graphagent.New("my-agent", g,
//	    graphagent.WithExecutorOptions(
//	        graph.WithMaxSteps(50),
//	        graph.WithStepTimeout(5*time.Minute),
//	        graph.WithNodeTimeout(2*time.Minute),
//	    ),
//	)
func WithExecutorOptions(opts ...graph.ExecutorOption) Option {
	return func(options *Options) {
		options.ExecutorOptions = append(options.ExecutorOptions, opts...)
	}
}
