//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func (s *sessionSummarizer) runInputOverflowHook(
	result summaryAttemptResult,
	request *model.Request,
	source *summarySource,
	trackResponse func(*model.Response),
	ensureTimingInfo func(*model.Response),
) summaryAttemptResult {
	if s.inputOverflowHook == nil {
		return result
	}
	if err := result.ctx.Err(); err != nil {
		result.err = err
		return result
	}

	// Keep the original source intact if the business declines. A pre-summary
	// hook owns the entire selected text, so it cannot be remapped to a prefix.
	candidate := *source
	if candidate.allowPrefix {
		ends, err := completeSummaryPrefixEndsForSource(&candidate)
		if err != nil {
			result.err = fmt.Errorf("select input overflow batch: %w", err)
			return result
		}
		if len(ends) == 0 {
			return result
		}
		input := candidate.input
		input.conversationText = joinSummaryEventTexts(candidate.prefixTexts[:ends[0]])
		candidate.selectPrefix(ends[0], input)
	}

	decision, err := s.inputOverflowHook(result.ctx, InputOverflow{
		Text: candidate.input.conversationText,
	})
	result.overflowHandled = true
	if err != nil {
		result.err = fmt.Errorf("input overflow hook failed: %w", err)
		return result
	}
	if err := result.ctx.Err(); err != nil {
		result.err = err
		return result
	}
	if decision == nil {
		return result
	}
	if strings.TrimSpace(decision.Text) == "" {
		result.err = fmt.Errorf("input overflow hook returned empty text")
		return result
	}

	candidate.input.conversationText = decision.Text
	approved, err := s.buildStandaloneSummaryRequest(candidate.input)
	if err != nil {
		result.err = fmt.Errorf("build input overflow request: %w", err)
		return result
	}
	*source = candidate
	*request = *approved
	return s.runSummaryAttempt(
		result.ctx,
		request,
		callModeStandalone,
		source.input,
		result.budget,
		true,
		trackResponse,
		ensureTimingInfo,
	)
}
