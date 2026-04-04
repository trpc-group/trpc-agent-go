//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Option is a function that configures a SessionSummarizer.
type Option func(*sessionSummarizer)

// WithName sets a logical name for the summarizer instance.
// This name is used for telemetry tagging (e.g., gen_ai.task_type) to help
// distinguish different summarization tasks.
func WithName(name string) Option {
	return func(s *sessionSummarizer) {
		s.name = name
	}
}

// SkipRecentFunc defines a function that determines how many recent events to skip during summarization.
// It receives all events and returns the number of recent events to skip.
// Return 0 to skip no events.
type SkipRecentFunc func(events []event.Event) int

// WithPrompt sets the custom prompt for summarization.
// The prompt must include the placeholder {conversation_text}, which will be
// replaced with the extracted conversation when generating the summary. When
// WithMaxSummaryWords is configured, {max_summary_words} must be included in
// either this prompt or WithSystemPrompt.
func WithPrompt(prompt string) Option {
	return func(s *sessionSummarizer) {
		if prompt != "" {
			s.prompt = prompt
		}
	}
}

// WithSystemPrompt sets an additional system prompt for summarization.
// The prompt is rendered into a dedicated system message before the user prompt.
// It must not include the {conversation_text} placeholder; keep conversation
// content in the user prompt instead. When WithMaxSummaryWords is configured,
// {max_summary_words} may be included here instead of the user prompt.
func WithSystemPrompt(prompt string) Option {
	return func(s *sessionSummarizer) {
		if prompt != "" {
			s.systemPrompt = prompt
		}
	}
}

// WithMaxSummaryWords sets the maximum word count for summaries.
// A value <= 0 means no word limit. The word limit will be included in the
// prompt to guide the model's generation rather than truncating the output.
func WithMaxSummaryWords(maxWords int) Option {
	return func(s *sessionSummarizer) {
		if maxWords > 0 {
			s.maxSummaryWords = maxWords
		}
	}
}

// WithSkipRecent sets a custom function to determine how many of the most recent
// events (from the tail) should be skipped during summarization. The function
// receives all events and returns the count of tail events to skip. Return 0 to
// skip none.
//
// Example:
//
//	WithSkipRecent(func(events []event.Event) int {
//	    // Skip the last 3 events
//	    return 3
//	})
//
//	WithSkipRecent(func(events []event.Event) int {
//	    // Skip events from the last 5 minutes
//	    cutoff := time.Now().Add(-5 * time.Minute)
//	    skipCount := 0
//	    for i := len(events) - 1; i >= 0; i-- {
//	        if events[i].Timestamp.After(cutoff) {
//	            skipCount++
//	        } else {
//	            break
//	        }
//	    }
//	    return skipCount
//	})
func WithSkipRecent(skipFunc SkipRecentFunc) Option {
	return func(s *sessionSummarizer) {
		s.skipRecentFunc = skipFunc
	}
}

// WithTokenThreshold appends a token-based check.
// Note: all checks in a summarizer are combined with global AND semantics.
// If you call multiple threshold options (e.g. token + event), all must pass.
func WithTokenThreshold(tokenCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, CheckTokenThresholdContext(tokenCount))
	}
}

// WithEventThreshold appends an event-count-based check.
// Note: all checks in a summarizer are combined with global AND semantics.
// If you call multiple threshold options (e.g. token + event), all must pass.
func WithEventThreshold(eventCount int) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, wrapChecker(CheckEventThreshold(eventCount)))
	}
}

// WithTimeThreshold appends a time-based check.
// Note: all checks in a summarizer are combined with global AND semantics.
// If you call multiple threshold options (e.g. event + time), all must pass.
func WithTimeThreshold(interval time.Duration) Option {
	return func(s *sessionSummarizer) {
		s.checks = append(s.checks, wrapChecker(CheckTimeThreshold(interval)))
	}
}

// WithChecksAll appends a single composite check that requires all provided checks (AND logic).
func WithChecksAll(checks ...Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, wrapChecker(ChecksAll(checks)))
		}
	}
}

// WithChecksAny appends a single composite check that passes if any provided check passes (OR logic).
func WithChecksAny(checks ...Checker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, wrapChecker(ChecksAny(checks)))
		}
	}
}

// WithChecksAllContext appends a single composite context-aware check that
// requires all provided checks (AND logic).
func WithChecksAllContext(checks ...ContextChecker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, allContextChecks(checks))
		}
	}
}

// WithChecksAnyContext appends a single composite context-aware check that
// passes if any provided check passes (OR logic).
func WithChecksAnyContext(checks ...ContextChecker) Option {
	return func(s *sessionSummarizer) {
		if len(checks) > 0 {
			s.checks = append(s.checks, anyContextChecks(checks))
		}
	}
}

// WithPreSummaryHook sets a pre-summary hook to modify text before the model call.
func WithPreSummaryHook(h PreSummaryHook) Option {
	return func(s *sessionSummarizer) {
		s.preHook = h
	}
}

// WithPostSummaryHook sets a post-summary hook to modify the summary before returning.
func WithPostSummaryHook(h PostSummaryHook) Option {
	return func(s *sessionSummarizer) {
		s.postHook = h
	}
}

// WithModelCallbacks sets model callbacks for summarization.
//
// Note: Only structured callback signatures are supported.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(s *sessionSummarizer) {
		s.modelCallbacks = callbacks
	}
}

// WithSummaryHookAbortOnError decides whether to abort when a hook returns an error.
// Default false: ignore hook errors and use original text/summary; true: return error.
func WithSummaryHookAbortOnError(abort bool) Option {
	return func(s *sessionSummarizer) {
		s.hookAbortOnError = abort
	}
}

// WithToolCallFormatter sets a custom formatter for tool calls in the summary input.
// The formatter receives a ToolCall and returns a formatted string.
// Return empty string to exclude the tool call from the summary.
//
// Example:
//
//	WithToolCallFormatter(func(tc model.ToolCall) string {
//	    // Only include tool name, exclude arguments.
//	    return fmt.Sprintf("[Called tool: %s]", tc.Function.Name)
//	})
func WithToolCallFormatter(f ToolCallFormatter) Option {
	return func(s *sessionSummarizer) {
		s.toolCallFormatter = f
	}
}

// WithToolResultFormatter sets a custom formatter for tool results in the summary input.
// The formatter receives the Message containing the tool result and returns a formatted string.
// Return empty string to exclude the tool result from the summary.
//
// Example:
//
//	WithToolResultFormatter(func(msg model.Message) string {
//	    // Truncate long results.
//	    content := msg.Content
//	    if len(content) > 200 {
//	        content = content[:200] + "..."
//	    }
//	    return fmt.Sprintf("[%s: %s]", msg.ToolName, content)
//	})
func WithToolResultFormatter(f ToolResultFormatter) Option {
	return func(s *sessionSummarizer) {
		s.toolResultFormatter = f
	}
}

// WithContextThreshold enables automatic summarization based on the model's
// context window, resolved dynamically at runtime from the invocation
// context. This is the recommended zero-configuration option for most
// use cases.
//
// The summarizer does not need to know the model name at creation time.
// When the user switches models mid-session, the threshold adjusts
// automatically because the checker reads the current model from the
// invocation attached to each request's context.
//
// Usage:
//
//	summary.NewSummarizer(model, summary.WithContextThreshold())
//	summary.NewSummarizer(model, summary.WithContextThreshold(
//	    summary.WithContextThresholdRatio(0.6)))
func WithContextThreshold(opts ...ContextThresholdOption) Option {
	return func(s *sessionSummarizer) {
		// If no explicit fallback window is configured, try to resolve one
		// from the summarizer's own model. This ensures that when invocation
		// context is unavailable (e.g. manual CreateSessionSummary calls),
		// the checker uses the summarizer model's context window instead of
		// the conservative framework default (8192).
		o := contextThresholdOptions{
			fallbackContextWindow: defaultContextThresholdFallbackWindow,
		}
		for _, opt := range opts {
			opt(&o)
		}
		if o.fallbackContextWindow == defaultContextThresholdFallbackWindow && s.model != nil {
			name := s.model.Info().Name
			if w, ok := model.LookupModelContextWindow(name); ok {
				opts = append(opts, WithContextThresholdFallbackWindow(w))
			}
		}
		s.checks = append(s.checks, CheckContextThreshold(opts...))
	}
}
